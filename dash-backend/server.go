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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type Server struct {
	updates                         *UpdateManager
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
	liveLease                       string
	liveCancel                      func()
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
				stop := server.liveOwned && (index.state() != "parked" || time.Since(server.liveTouched) > LiveLeaseTimeout || !memoryAvailable())
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
		// An open tab may still request immutable assets from the last build.
		// Resolve only hash-named public assets from retained Dash releases.
		file, err = s.retainedAsset(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(file, "index.html") || strings.HasSuffix(file, "sw.js") {
		w.Header().Set("Cache-Control", "no-store")
	}
	if immutableAssetPattern.MatchString(path) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFile(w, r, file)
}
func (s *Server) getAPI(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/diagnostics/download":
		s.downloadDiagnostics(w, r)
	case "/api/updates":
		s.updateAPI(w, r)
	case "/api/info":
		_, ffmpeg := mediaTool("ffmpeg")
		writeJSON(w, 200, map[string]any{"build": currentBuild(), "simulation": s.simulation, "kind": "dash-backend", "version": 1, "metadata": true, "ffmpeg": ffmpeg == nil, "recordings": "/files/", "driving": s.index.state(), "managedLive": s.managedLive(), "resources": map[string]any{"heavyJobs": HeavyJobLimit, "heapMiB": GoHeapTargetMiB, "mediaThreads": MediaThreads, "cpuCores": DeviceCPUCores, "deviceMode": deviceMode}})
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
	case "/api/playlist":
		s.playlist(w, r)
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
	transport := r.URL.Query().Get("container") == "ts"
	if transport {
		w.Header().Set("Content-Type", "video/mp2t")
	}
	if transport && strings.HasSuffix(file, ".ts") {
		http.ServeFile(w, r, file)
		return
	}
	codec := "copy"
	if r.URL.Query().Get("codec") == "h264" {
		codec = "h264"
	}
	chunk := -1
	if value := r.URL.Query().Get("chunk"); value != "" {
		chunk, err = strconv.Atoi(value)
		if err != nil || chunk < 0 || chunk >= (600+PreviewChunkSeconds-1)/PreviewChunkSeconds || !transport || codec != "h264" || !strings.HasSuffix(file, ".hevc") {
			fail(w, 400, errors.New("invalid preview chunk"))
			return
		}
	}
	companion := ""
	companionStamp := ""
	if strings.HasSuffix(file, ".hevc") {
		candidate, err := safePath(s.index.root, filepath.Join(filepath.Dir(r.URL.Query().Get("file")), "qcamera.ts"))
		if err == nil {
			if info, err := os.Stat(candidate); err == nil && info.Size() > 0 {
				companion = candidate
				companionStamp = fmt.Sprintf(":%d:%d", info.Size(), info.ModTime().UnixNano())
			}
		}
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%s:%t:chunk-v4-late-audio:%d:%d%s", file, info.Size(), info.ModTime().UnixNano(), codec, transport, chunk, PreviewChunkSeconds, companionStamp)))
	folder := filepath.Join(s.index.cache, "previews")
	_ = os.MkdirAll(folder, 0700)
	extension := ".mp4"
	if transport {
		extension = ".ts"
	}
	target := filepath.Join(folder, fmt.Sprintf("%x%s", hash, extension))
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
			partial := target + ".partial" + extension
			defer os.Remove(partial)
			if companion != "" {
				present, err := recordedAudio(ctx, companion, folder)
				if ctx.Err() != nil {
					fail(w, 409, ctx.Err())
					return
				}
				if err != nil || !present {
					companion = ""
				}
			}
			log.Printf("preview preparing file=%s chunk=%d", r.URL.Query().Get("file"), chunk)
			args := []string{}
			if strings.HasSuffix(file, ".hevc") {
				args = append(args, "-framerate", "20")
			}
			input, relative, audioStart := file, float64(max(0, chunk)*PreviewChunkSeconds), 0.0
			if chunk > 0 {
				var cleanup func()
				var err error
				input, relative, audioStart, cleanup, err = hevcPreviewWindow(ctx, file, folder, float64(chunk*PreviewChunkSeconds))
				if err != nil {
					fail(w, 422, err)
					return
				}
				defer cleanup()
			}
			args = append(args, audioProbeArgs()...)
			args = append(args, "-i", input)
			audioInput := 0
			if strings.HasSuffix(file, ".hevc") {
				// AAC is stored with the road preview, independently of the
				// selected raw camera. Both inputs use their native start time.
				audioInput = -1
				if companion != "" {
					if audioStart > 0 {
						args = append(args, "-ss", strconv.FormatFloat(audioStart, 'f', 6, 64))
					}
					args = append(args, audioProbeArgs()...)
					args = append(args, "-i", companion)
					audioInput = 1
				}
			}
			if chunk >= 0 {
				// Encode only this short range from the nearest independent GOP.
				args = append(args, "-ss", strconv.FormatFloat(relative, 'f', 6, 64), "-t", strconv.Itoa(PreviewChunkSeconds))
			}
			args = append(args, "-map", "0:v:0")
			if audioInput >= 0 {
				args = append(args, "-map", fmt.Sprintf("%d:a:0?", audioInput), "-c:a", "copy")
			} else {
				args = append(args, "-an")
			}
			if codec == "h264" {
				args = append(args, "-c:v", "libx264", "-threads", strconv.Itoa(MediaThreads), "-preset", "ultrafast", "-crf", "25", "-vf", fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2", PreviewMaxWidth, PreviewMaxHeight), "-pix_fmt", "yuv420p")
			} else {
				args = append(args, "-c:v", "copy")
				if strings.HasSuffix(file, ".hevc") && !transport {
					args = append(args, "-tag:v", "hvc1")
				}
			}
			args = append(args, "-fs", "268435456")
			if transport {
				args = append(args, "-f", "mpegts")
			} else {
				args = append(args, "-movflags", "+faststart")
			}
			args = append(args, partial)
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
	case "/api/updates":
		s.updateAPI(w, r)
	case "/api/playback/prepare":
		s.preparePlayback(w, r)
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
		lease := liveLease(r)
		s.liveMu.Lock()
		active := lease != "" && s.liveOwned && lease == s.liveLease
		if active {
			s.liveTouched = time.Now()
		}
		s.liveMu.Unlock()
		writeJSON(w, 200, map[string]any{"driving": s.index.state(), "active": active})
	case "/api/stream/stop":
		if lease := liveLease(r); lease != "" {
			s.stopLiveLease(lease)
		}
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
func (s *Server) managedLive() bool { return s.firmwareManagedLive() || s.localLiveAvailable() }
func (s *Server) firmwareManagedLive() bool {
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
	marker := filepath.Join(s.index.cache, ".dash-live-owned")
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
func (s *Server) stopLive() { s.stopLiveLease("") }
func (s *Server) stopLiveLease(lease string) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	if lease != "" && lease != s.liveLease {
		return
	}
	if s.liveOwned {
		if s.liveCancel != nil {
			s.liveCancel()
			s.liveCancel = nil
		} else {
			_ = s.setLive(false)
		}
		s.liveOwned = false
		s.liveLease = ""
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
		fail(w, 409, errors.New("local streaming requires installed camera and WebRTC support"))
		return
	}
	var request struct {
		Lease   string   `json:"lease"`
		SDP     string   `json:"sdp"`
		Cameras []string `json:"cameras"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		fail(w, 400, err)
		return
	}
	if !liveLeasePattern.MatchString(request.Lease) || len(request.SDP) > 100000 || len(request.Cameras) != 1 || (request.Cameras[0] != "road" && request.Cameras[0] != "wideRoad" && request.Cameras[0] != "driver") {
		fail(w, 400, errors.New("invalid camera request"))
		return
	}
	if !s.firmwareManagedLive() {
		s.startLocalLive(w, r, request.SDP, request.Cameras[0], request.Lease)
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
	s.liveLease = request.Lease
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

// Readiness is an ordinary 200 response, not an HLS error/retry loop. Opening
// a session promotes its parts in the existing single parked index worker.
func (s *Server) preparePlayback(w http.ResponseWriter, r *http.Request) {
	drive, camera := r.URL.Query().Get("drive"), r.URL.Query().Get("camera")
	if !segmentPattern.MatchString(drive+"--0") || !cameras[camera] {
		fail(w, 400, errors.New("invalid recording or camera"))
		return
	}
	folders, err := os.ReadDir(s.index.root)
	if err != nil {
		fail(w, 500, err)
		return
	}
	snapshot, err := s.index.snapshot()
	if err != nil {
		fail(w, 500, err)
		return
	}
	total, complete, playable := 0, 0, 0
	for _, folder := range folders {
		if !folder.IsDir() || !strings.HasPrefix(folder.Name(), drive+"--") || !segmentPattern.MatchString(folder.Name()) {
			continue
		}
		total++
		entry, ok := snapshot.Segments[folder.Name()]
		if !ok || entry.MediaVersion != 1 {
			continue
		}
		complete++
		for _, file := range entry.Files {
			if file.Name == camera && file.Size > 0 && file.Duration > 0 {
				playable++
			}
		}
	}
	if total > 0 && complete < total && s.index.state() == "parked" {
		s.index.mu.Lock()
		s.index.priorityDrive = drive
		s.index.mu.Unlock()
		go func() { _ = s.index.scan(s.ctx) }()
	}
	state := "unavailable"
	ready := total > 0 && complete == total && playable > 0
	if ready {
		state = "ready"
	} else if total > 0 && complete < total {
		state = "parkedRequired"
		if s.index.state() == "parked" {
			state = "indexing"
		}
	}
	writeJSON(w, 200, map[string]any{"ready": ready, "status": state, "completed": complete, "total": total})
}
