package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type Server struct {
	simulationMu                    sync.Mutex
	diagnostics                     *Diagnostics
	simulation                      bool
	liveRelease                     func()
	ctx                             context.Context
	index                           *Index
	exports                         *ExportManager
	app, params, firmware, upstream string
	client                          *http.Client
	liveMu                          sync.Mutex
	liveOwned                       bool
	liveTouched                     time.Time
}

func newServer(ctx context.Context, index *Index, exports *ExportManager, app, params, firmware, upstream string) *Server {
	server := &Server{ctx: ctx, index: index, exports: exports, app: app, params: params, firmware: firmware, upstream: strings.TrimRight(upstream, "/"), client: &http.Client{Timeout: 35 * time.Second}}
	go func() {
		lastState := index.state()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		defer server.stopLive()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if state := index.state(); state != lastState {
					log.Printf("driving state: %s -> %s", lastState, state)
					lastState = state
				}
				server.liveMu.Lock()
				stop := server.liveOwned && (index.state() != "parked" || time.Since(server.liveTouched) > 12*time.Second || !memoryAvailable())
				server.liveMu.Unlock()
				if stop {
					server.stopLive()
				}
			}
		}
	}()
	return server
}
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func fail(w http.ResponseWriter, status int, err error) {
	log.Printf("HTTP failure status=%d: %s", status, strings.ReplaceAll(err.Error(), "\n", " "))
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host != r.Host {
			fail(w, 403, errors.New("open Dash from this backend's own address"))
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Method == http.MethodPost {
			s.post(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			fail(w, 405, errors.New("method not allowed"))
			return
		}
		s.getAPI(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/files/") {
		s.files(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	file, err := safePath(s.app, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(file, "index.html") || strings.HasSuffix(file, "sw.js") {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFile(w, r, file)
}
func (s *Server) getAPI(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/diagnostics/download":
		s.downloadDiagnostics(w, r)
	case "/api/info":
		_, ffmpeg := exec.LookPath("ffmpeg")
		writeJSON(w, 200, map[string]any{"simulation": s.simulation, "kind": "dash-backend", "version": 1, "metadata": true, "ffmpeg": ffmpeg == nil, "recordings": "/files/", "driving": s.index.state(), "managedLive": s.managedLive(), "resources": map[string]any{"heavyJobs": 1, "heapMiB": 128, "ffmpegRSSMiB": 192, "reservedRAMMiB": 512}})
	case "/api/index":
		snapshot, err := s.index.snapshot()
		if err != nil {
			fail(w, 500, err)
			return
		}
		writeJSON(w, 200, snapshot)
	case "/api/metadata":
		result, err := s.index.driveMetadata(r.URL.Query().Get("drive"))
		if err != nil {
			fail(w, 400, err)
			return
		}
		writeJSON(w, 200, result)
	case "/api/video":
		s.video(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, "/api/thumbnail/") {
			name := strings.TrimPrefix(r.URL.Path, "/api/thumbnail/")
			if !regexp.MustCompile(`^[a-f0-9]{64}\.jpg$`).MatchString(name) {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, filepath.Join(s.index.cache, name))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/exports/") {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/exports/"), "/")
			job, err := s.exports.snapshot(parts[0])
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if len(parts) == 2 && parts[1] == "download" {
				if job.Status != "complete" {
					fail(w, 409, errors.New("export not complete"))
					return
				}
				w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", job.Name))
				http.ServeFile(w, r, job.output)
				return
			}
			writeJSON(w, 200, job)
			return
		}
		http.NotFound(w, r)
	}
}
func (s *Server) files(w http.ResponseWriter, r *http.Request) {
	relative := strings.TrimPrefix(r.URL.Path, "/files/")
	file, err := safePath(s.index.root, relative)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if info.IsDir() {
		if !r.URL.Query().Has("ls") {
			http.NotFound(w, r)
			return
		}
		entries, err := os.ReadDir(file)
		if err != nil {
			fail(w, 500, err)
			return
		}
		dirs, files := []map[string]any{}, []map[string]any{}
		for _, entry := range entries {
			safe, err := safePath(s.index.root, filepath.Join(relative, entry.Name()))
			if err != nil {
				continue
			}
			stat, err := os.Stat(safe)
			if err != nil {
				continue
			}
			href := url.PathEscape(entry.Name())
			if stat.IsDir() {
				href += "/"
			}
			item := map[string]any{"href": href, "sz": stat.Size(), "ts": float64(stat.ModTime().UnixNano()) / 1e9}
			if stat.IsDir() {
				dirs = append(dirs, item)
			} else if stat.Mode().IsRegular() {
				files = append(files, item)
			}
		}
		writeJSON(w, 200, map[string]any{"dirs": dirs, "files": files})
		return
	}
	if !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Has("dl") {
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filepath.Base(file)))
	}
	http.ServeFile(w, r, file)
}
func (s *Server) video(w http.ResponseWriter, r *http.Request) {
	file, err := safePath(s.index.root, r.URL.Query().Get("file"))
	if err != nil || !cameras[filepath.Base(file)] {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(file)
	if err != nil || info.Size() > 256<<20 {
		fail(w, 400, errors.New("preview segment missing or exceeds 256 MiB"))
		return
	}
	codec := "copy"
	if r.URL.Query().Get("codec") == "h264" {
		codec = "h264"
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%s", file, info.Size(), info.ModTime().UnixNano(), codec)))
	folder := filepath.Join(s.index.cache, "previews")
	_ = os.MkdirAll(folder, 0700)
	target := filepath.Join(folder, fmt.Sprintf("%x.mp4", hash))
	if _, err := os.Stat(target); err != nil {
		if s.index.state() != "parked" {
			fail(w, 409, errors.New("preview preparation requires confirmed parked state"))
			return
		}
		ctx, cancel := parkedContext(r.Context(), s.index.state)
		defer cancel()
		ctx, timeout := context.WithTimeout(ctx, 3*time.Minute)
		defer timeout()
		release, err := acquireCompute(ctx)
		if err != nil {
			fail(w, 503, err)
			return
		}
		defer release()
		if _, err := os.Stat(target); err != nil {
			s.prunePreviews(folder, 256<<20)
			var disk unix.Statfs_t
			if err := unix.Statfs(folder, &disk); err != nil || uint64(disk.Bavail)*uint64(disk.Bsize) < 2<<30 {
				fail(w, 507, errors.New("preview preparation requires 2 GiB free space"))
				return
			}
			partial := target + ".partial.mp4"
			defer os.Remove(partial)
			args := []string{}
			if strings.HasSuffix(file, ".hevc") {
				args = append(args, "-framerate", "20")
			}
			args = append(args, "-i", file, "-map", "0:v:0", "-an")
			if codec == "h264" {
				args = append(args, "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-crf", "25", "-vf", "scale='min(1280,iw)':-2", "-pix_fmt", "yuv420p")
			} else {
				args = append(args, "-c:v", "copy")
				if strings.HasSuffix(file, ".hevc") {
					args = append(args, "-tag:v", "hvc1")
				}
			}
			args = append(args, "-fs", "268435456", "-movflags", "+faststart", partial)
			if err := runFFmpeg(ctx, args...); err != nil {
				fail(w, 422, err)
				return
			}
			if info, err := os.Stat(partial); err != nil || info.Size() >= 256<<20 {
				fail(w, 422, errors.New("preview exceeds 256 MiB; download the original file"))
				return
			}
			if err := os.Rename(partial, target); err != nil {
				fail(w, 500, err)
				return
			}
		}
	}
	http.ServeFile(w, r, target)
}
func (s *Server) prunePreviews(folder string, limit int64) {
	entries, _ := os.ReadDir(folder)
	var total int64
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
	}
	for _, entry := range entries {
		if total <= limit {
			return
		}
		if info, err := entry.Info(); err == nil {
			if os.Remove(filepath.Join(folder, entry.Name())) == nil {
				total -= info.Size()
			}
		}
	}
}
func (s *Server) post(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/simulation/state" {
		s.simulationState(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	switch r.URL.Path {
	case "/api/index/rebuild":
		if err := s.index.rebuild(); err != nil {
			fail(w, 409, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"rebuilding": true})
		go func() { _ = s.index.scan(s.ctx) }()
	case "/api/exports":
		var request ExportRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			fail(w, 400, err)
			return
		}
		job, err := s.exports.submit(request)
		if err != nil {
			fail(w, 409, err)
			return
		}
		writeJSON(w, 202, job)
	case "/api/stream":
		s.startLive(w, r)
	case "/api/stream/heartbeat":
		s.liveMu.Lock()
		s.liveTouched = time.Now()
		s.liveMu.Unlock()
		writeJSON(w, 200, map[string]string{"driving": s.index.state()})
	case "/api/stream/stop":
		s.stopLive()
		writeJSON(w, 200, map[string]bool{"stopped": true})
	default:
		if strings.HasPrefix(r.URL.Path, "/api/exports/") && strings.HasSuffix(r.URL.Path, "/cancel") {
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) != 5 {
				http.NotFound(w, r)
				return
			}
			if err := s.exports.cancelJob(parts[3]); err != nil {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, 200, map[string]bool{"canceling": true})
			return
		}
		http.NotFound(w, r)
	}
}
func (s *Server) managedLive() bool {
	for _, prefix := range []string{"openpilot/", ""} {
		helper, err := os.ReadFile(filepath.Join(s.firmware, prefix+"system/webrtc/helpers.py"))
		if err != nil {
			continue
		}
		manager, err := os.ReadFile(filepath.Join(s.firmware, prefix+"system/manager/process_config.py"))
		if err == nil && bytes.Contains(helper, []byte("enabled: bool")) && bytes.Contains(manager, []byte("IsLiveStreaming")) {
			return true
		}
	}
	return false
}
func (s *Server) setLive(enabled bool) error {
	marker := filepath.Join(s.params, ".dash-live-owned")
	if enabled {
		if err := os.WriteFile(marker, []byte("1"), 0600); err != nil {
			return err
		}
	}
	value := []byte("0")
	if enabled {
		value = []byte("1")
	}
	file, err := os.CreateTemp(s.params, ".dash-live-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(value); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(s.params, "IsLiveStreaming")); err != nil {
		return err
	}
	if !enabled {
		_ = os.Remove(marker)
	}
	return nil
}
func (s *Server) stopLive() {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	if s.liveOwned {
		_ = s.setLive(false)
		s.liveOwned = false
		if s.liveRelease != nil {
			s.liveRelease()
			s.liveRelease = nil
		}
	}
}
func (s *Server) startLive(w http.ResponseWriter, r *http.Request) {
	if s.index.state() != "parked" || !memoryAvailable() {
		fail(w, 409, errors.New("live view requires confirmed parked state and available memory"))
		return
	}
	if !s.managedLive() {
		fail(w, 409, errors.New("this firmware lacks managed local streaming; Dash will not start an unmanaged encoder that it cannot stop on ignition"))
		return
	}
	var request struct {
		SDP     string   `json:"sdp"`
		Cameras []string `json:"cameras"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		fail(w, 400, err)
		return
	}
	if len(request.SDP) > 100000 || len(request.Cameras) != 1 || (request.Cameras[0] != "road" && request.Cameras[0] != "wideRoad" && request.Cameras[0] != "driver") {
		fail(w, 400, errors.New("invalid camera request"))
		return
	}
	s.liveMu.Lock()
	owned := s.liveOwned
	s.liveMu.Unlock()
	if owned {
		fail(w, 409, errors.New("a live session is already active"))
		return
	}
	waitCtx, waitCancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer waitCancel()
	release, err := acquireCompute(waitCtx)
	if err != nil {
		fail(w, 409, err)
		return
	}
	s.liveMu.Lock()
	if s.index.state() != "parked" {
		s.liveMu.Unlock()
		release()
		fail(w, 409, errors.New("vehicle is not parked"))
		return
	}
	if value, err := os.ReadFile(filepath.Join(s.params, "IsLiveStreaming")); err == nil && strings.TrimSpace(string(value)) == "1" {
		s.liveMu.Unlock()
		release()
		fail(w, 409, errors.New("firmware already has a live session"))
		return
	}
	s.liveRelease = release
	if err := s.setLive(true); err != nil {
		s.liveRelease = nil
		release()
		s.liveMu.Unlock()
		fail(w, 500, err)
		return
	}
	s.liveOwned = true
	s.liveTouched = time.Now()
	s.liveMu.Unlock()
	ctx, cancel := parkedContext(r.Context(), s.index.state)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 45*time.Second)
	defer timeout()
	// Manager owns startup and teardown of the encoder and camera processes.
	ready := false
	for n := 0; n < 20; n++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", s.upstream+"/schema?services=", nil)
		response, err := s.client.Do(req)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		select {
		case <-ctx.Done():
			s.stopLive()
			fail(w, 409, ctx.Err())
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	if !ready {
		s.stopLive()
		fail(w, 502, errors.New("firmware streaming service did not start"))
		return
	}
	payload, _ := json.Marshal(map[string]any{"sdp": request.SDP, "cameras": request.Cameras, "enabled": true, "bridge_services_in": []string{}, "bridge_services_out": []string{}})
	req, _ := http.NewRequestWithContext(ctx, "POST", s.upstream+"/stream", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		s.stopLive()
		fail(w, 502, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		s.stopLive()
		fail(w, 502, errors.New("firmware rejected the stream request"))
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		s.stopLive()
		fail(w, 502, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
