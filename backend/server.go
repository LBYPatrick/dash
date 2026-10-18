package main

import (
	"bytes"
	"context"
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
	"runtime"
	"strings"
	"sync"
	"time"

	"dash.local/backend/internal/archive"
	"dash.local/backend/internal/usb"
)

type Server struct {
	transfers                                      *archive.Manager
	transferAuth                                   *archive.Authorizer
	usbClient                                      *usb.Client
	transferHome, transferLocalRoot, transferEpoch string
	updates                                        *UpdateManager
	simulationMu                                   sync.Mutex
	diagnostics                                    *Diagnostics
	simulation                                     bool
	liveRelease                                    func()
	ctx                                            context.Context
	index                                          *Index
	exports                                        *ExportManager
	app, params, firmware, upstream                string
	client                                         *http.Client
	liveMu                                         sync.Mutex
	liveOwned                                      bool
	liveTouched                                    time.Time
	liveLease                                      string
	liveCancel                                     func()
}

func newServer(ctx context.Context, index *Index, exports *ExportManager, app, params, firmware, upstream string) *Server {
	server := &Server{ctx: ctx, index: index, exports: exports, app: app, params: params, firmware: firmware, upstream: strings.TrimRight(upstream, "/"), client: &http.Client{Timeout: 35 * time.Second}}
	go func() {
		lastState := index.state()
		if deviceMode {
			runtime.GOMAXPROCS(goParallelism(lastState))
		}
		ticker := time.NewTicker(DrivingWatchInterval)
		defer ticker.Stop()
		defer server.stopLive()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if state := index.state(); state != lastState {
					if deviceMode {
						runtime.GOMAXPROCS(goParallelism(state))
					}
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
		if strings.HasPrefix(r.URL.Path, "/api/transfers") || strings.HasPrefix(r.URL.Path, "/api/transfer-destinations") {
			if r.Method != http.MethodGet && r.Method != http.MethodPost {
				fail(w, 405, errors.New("method not allowed"))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 65536)
			s.transferAPI(w, r)
			return
		}
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
		writeJSON(w, 200, map[string]any{"build": currentBuild(), "simulation": s.simulation, "kind": "dash-backend", "version": 1, "metadata": true, "ffmpeg": ffmpeg == nil, "recordings": "/files/", "driving": s.index.state(), "managedLive": s.managedLive(), "resources": map[string]any{"heavyJobs": HeavyJobLimit, "heapMiB": GoHeapTargetMiB, "mediaThreads": MediaThreads, "cpuCores": DeviceCPUCores, "deviceMode": deviceMode, "goProcs": runtime.GOMAXPROCS(0), "idleGoProcs": IdleGoProcs}})
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
	case "/api/media/init", "/api/media/fragment":
		s.nativeMedia(w, r)
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
				if job.Streaming {
					s.exports.downloadStream(w, r, job.ID)
					return
				}
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
	unsupported := false
	storageRequired := false
	sessionAudio := false
	codecs := []string{}
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
			if file.Name != camera || file.Size <= 0 || file.Duration <= 0 {
				continue
			}
			if strings.HasSuffix(camera, ".hevc") {
				if file.Native == nil {
					complete--
					continue
				}
				if file.Native.Status == "waitingForSpace" {
					storageRequired = true
					continue
				}
				if file.Native.Status != "ready" {
					unsupported = true
					continue
				}
				_, repair := s.index.nativeRepair.Load(file.Native.Key)
				stat, err := os.Lstat(s.index.nativePath(file.Native.Key))
				if repair || err != nil || !stat.Mode().IsRegular() {
					complete--
					continue
				}
				codec := file.Native.Codec
				if file.Native.Audio {
					sessionAudio = true
				}
				codecs = append(codecs, codec)
			}
			playable++
		}
	}
	if sessionAudio {
		for n := range codecs {
			codecs[n] += ",mp4a.40.2"
		}
	}
	if total > 0 && complete < total && s.index.state() == "parked" {
		s.index.mu.Lock()
		s.index.priorityDrive = drive
		s.index.mu.Unlock()
		go func() { _ = s.index.scan(s.ctx) }()
	}
	state := "unavailable"
	ready := total > 0 && complete == total && playable > 0 && !unsupported && !storageRequired
	if storageRequired {
		state = "storageRequired"
	} else if unsupported {
		state = "unsupported"
	} else if ready {
		state = "ready"
	} else if total > 0 && complete < total {
		state = "parkedRequired"
		if s.index.state() == "parked" {
			state = "indexing"
		}
	}
	writeJSON(w, 200, map[string]any{"ready": ready, "status": state, "completed": complete, "total": total, "codecs": codecs})
}
