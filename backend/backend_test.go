package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dashv2 "dash.local/backend/internal/mcaptypes/dashv2"
	foxglove "dash.local/backend/internal/mcaptypes/foxglove"
	"github.com/foxglove/mcap/go/mcap"
	"google.golang.org/protobuf/proto"
)

const fixtureDrive = "000001a3--c20ba54385"

func fixtureIndex(t *testing.T) (*Index, *atomic.Bool) {
	t.Helper()
	root := t.TempDir()
	folder := filepath.Join(root, fixtureDrive+"--0")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("internal/telemetry/testdata/qlog.bz2")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(folder, "qlog.bz2"), data, 0600); err != nil {
		t.Fatal(err)
	}
	parked := &atomic.Bool{}
	parked.Store(true)
	index, err := newIndex(root, t.TempDir(), func() string {
		if parked.Load() {
			return "parked"
		}
		return "onroad"
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { index.db.Close() })
	return index, parked
}
func TestIndexIncrementalCorruptAndDeleted(t *testing.T) {
	index, parked := fixtureIndex(t)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := index.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	entry := snapshot.Segments[fixtureDrive+"--0"]
	if entry.Metadata.Car != "TEST VEHICLE" || len(entry.Metadata.Points) != 0 {
		t.Fatal("summary must have identity but no full samples")
	}
	details, err := index.driveMetadata(fixtureDrive)
	if err != nil || len(details[fixtureDrive+"--0"].Metadata.Points) != 11 {
		t.Fatal("full metadata missing", err)
	}
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	parked.Store(false)
	if err := index.rebuild(); err == nil {
		t.Fatal("rebuild allowed onroad")
	}
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	parked.Store(true)
	if err := os.RemoveAll(filepath.Join(index.root, fixtureDrive+"--0")); err != nil {
		t.Fatal(err)
	}
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = index.snapshot()
	if len(snapshot.Segments) != 0 {
		t.Fatal("deleted segment retained")
	}
	cache := t.TempDir()
	if err := os.WriteFile(filepath.Join(cache, "index.sqlite3"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	repaired, err := newIndex(t.TempDir(), cache, func() string { return "parked" })
	if err != nil {
		t.Fatal(err)
	}
	repaired.db.Close()
}
func TestParkedGateAndCancellation(t *testing.T) {
	root := t.TempDir()
	if paramsState(root)() != "unknown" {
		t.Fatal("absent params treated as parked")
	}
	os.WriteFile(filepath.Join(root, "IsOnroad"), []byte("0"), 0600)
	os.WriteFile(filepath.Join(root, "IsOffroad"), []byte("1"), 0600)
	state := paramsState(root)
	ctx, cancel := parkedContext(context.Background(), state)
	defer cancel()
	release, err := acquireCompute(ctx)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "IsOnroad"), []byte("1"), 0600)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("onroad transition did not cancel work")
	}
	release()
	if _, err := acquireCompute(ctx); err == nil {
		t.Fatal("canceled work acquired compute slot")
	}
	index, parked := fixtureIndex(t)
	parked.Store(false)
	manager, _ := newExports(context.Background(), index)
	if _, err := manager.submit(ExportRequest{Drive: fixtureDrive, Segments: []int{0}, Format: "zip"}); err == nil {
		t.Fatal("export started onroad")
	}
}
func waitExport(t *testing.T, manager *ExportManager, id string) ExportJob {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		job, err := manager.snapshot(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "complete" {
			return job
		}
		if job.Status == "failed" || job.Status == "canceled" {
			t.Fatal(job.Message)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("export timeout")
	return ExportJob{}
}
func TestZIPAndMCAPExports(t *testing.T) {
	index, _ := fixtureIndex(t)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager, err := newExports(context.Background(), index)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"zip", "mcap"} {
		job, err := manager.submit(ExportRequest{Drive: fixtureDrive, Segments: []int{0}, Format: format})
		if err != nil {
			t.Fatal(err)
		}
		job = waitExport(t, manager, job.ID)
		if format == "zip" {
			archive, err := zip.OpenReader(job.output)
			if err != nil {
				t.Fatal(err)
			}
			if len(archive.File) != 2 {
				t.Fatal("missing zip files")
			}
			archive.Close()
		} else {
			file, err := os.Open(job.output)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := mcap.NewReader(file)
			if err != nil {
				t.Fatal(err)
			}
			info, err := reader.Info()
			if err != nil {
				t.Fatal(err)
			}
			if len(info.Schemas) != 8 || len(info.AttachmentIndexes) != 1 {
				t.Fatal("missing MCAP schemas or attachments")
			}
			for _, schema := range info.Schemas {
				if !strings.HasPrefix(schema.Name, "dash.v2.") && schema.Name != "foxglove.LocationFix" {
					t.Fatal("foreign MCAP schema")
				}
			}
			messages, err := reader.Messages()
			if err != nil {
				t.Fatal(err)
			}
			if span := time.Duration(info.Statistics.MessageEndTime - info.Statistics.MessageStartTime); span != 10*time.Second {
				t.Fatalf("file mtime stretched GPS capture: %s", span)
			}
			attachment := info.AttachmentIndexes[0]
			source, err := os.Stat(filepath.Join(index.root, fixtureDrive+"--0", "qlog.bz2"))
			if err != nil || attachment.LogTime != info.Statistics.MessageStartTime || attachment.CreateTime != uint64(source.ModTime().UnixNano()) {
				t.Fatal("attachment must use capture LogTime and preserve filesystem CreateTime", err)
			}
			positions := 0
			for {
				schema, _, message, err := messages.Next(nil)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if schema.Name == "dash.v2.Asset" {
					var asset dashv2.Asset
					if proto.Unmarshal(message.Data, &asset) != nil || asset.ModifiedTimeNanoseconds != uint64(source.ModTime().UnixNano()) {
						t.Fatal("asset must preserve exact original file time")
					}
				}
				if schema.Name == "foxglove.LocationFix" {
					positions++
					var point foxglove.LocationFix
					if proto.Unmarshal(message.Data, &point) != nil || point.Latitude < 37 {
						t.Fatal("wrong position")
					}
				}
			}
			if positions != 11 {
				t.Fatalf("got %d GPS messages", positions)
			}
			reader.Close()
			file.Close()
		}
	}
}
func TestStaticRangeAndPathBoundary(t *testing.T) {
	index, _ := fixtureIndex(t)
	app := t.TempDir()
	os.WriteFile(filepath.Join(app, "index.html"), []byte("dash app"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, _ := newExports(ctx, index)
	server := httptest.NewServer(newServer(ctx, index, manager, app, t.TempDir(), t.TempDir(), "http://127.0.0.1:1"))
	defer server.Close()
	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "dash app" {
		t.Fatal("PWA not served")
	}
	request, _ := http.NewRequest("GET", server.URL+"/files/"+fixtureDrive+"--0/qlog.bz2", nil)
	request.Header.Set("Range", "bytes=0-9")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 206 || response.ContentLength != 10 {
		t.Fatal("range request failed")
	}
	outside := filepath.Join(t.TempDir(), "private")
	os.WriteFile(outside, []byte("private"), 0600)
	os.Symlink(outside, filepath.Join(index.root, "escape"))
	if _, err := safePath(index.root, "escape"); err == nil {
		t.Fatal("symlink traversal allowed")
	}
	request, _ = http.NewRequest("POST", server.URL+"/api/index/rebuild", nil)
	request.Header.Set("Origin", "https://unrelated.example")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
}
func TestNativeVideoExport(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	index, _ := fixtureIndex(t)
	camera := filepath.Join(index.root, fixtureDrive+"--0/qcamera.ts")
	command := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=green:s=160x90:r=20", "-t", "1", "-c:v", "libx264", "-threads", "1", "-f", "mpegts", camera)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture video: %v %s", err, output)
	}
	manager, _ := newExports(context.Background(), index)
	job, err := manager.submit(ExportRequest{Drive: fixtureDrive, Segments: []int{0}, Format: "mp4", Camera: "qcamera.ts"})
	if err != nil {
		t.Fatal(err)
	}
	job = waitExport(t, manager, job.ID)
	output, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", job.output).Output()
	if err != nil || !strings.HasPrefix(string(output), "1.") {
		t.Fatalf("invalid MP4: %s %v", output, err)
	}
}

func TestActiveFFmpegStopsOnIgnition(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	parked := &atomic.Bool{}
	parked.Store(true)
	ctx, cancel := parkedContext(context.Background(), func() string {
		if parked.Load() {
			return "parked"
		}
		return "onroad"
	})
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- runFFmpeg(ctx, "-re", "-f", "lavfi", "-i", "color=c=green:s=160x90:r=20", "-t", "30", "-f", "null", "-")
	}()
	time.Sleep(150 * time.Millisecond)
	select {
	case err := <-result:
		t.Fatalf("worker exited before ignition: %v", err)
	default:
	}
	parked.Store(false)
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("active conversion completed after ignition")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("active conversion was not killed")
	}
}
func TestExportQueuedAtIgnitionDiscardsOutput(t *testing.T) {
	index, parked := fixtureIndex(t)
	manager, _ := newExports(context.Background(), index)
	release, err := acquireCompute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	job, err := manager.submit(ExportRequest{Drive: fixtureDrive, Segments: []int{0}, Format: "zip"})
	if err != nil {
		release()
		t.Fatal(err)
	}
	parked.Store(false)
	time.Sleep(150 * time.Millisecond)
	release()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, _ := manager.snapshot(job.ID)
		if status.Status == "failed" || status.Status == "canceled" {
			if _, err := os.Stat(status.output); !os.IsNotExist(err) {
				t.Fatal("partial output was retained")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("queued export did not cancel")
}

func TestKernelResourceLimitsFailClosed(t *testing.T) {
	directory := t.TempDir()
	values := map[string]string{"cpu.max": "50000 100000", "memory.max": "402653184", "memory.high": "268435456", "memory.swap.max": "0", "pids.max": "64"}
	for name, value := range values {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyLimits(directory); err != nil {
		t.Fatal(err)
	}
	for name := range values {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("max"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := verifyLimits(directory); err == nil {
			t.Fatalf("unlimited %s accepted", name)
		}
		os.WriteFile(filepath.Join(directory, name), []byte(values[name]), 0600)
	}
	os.WriteFile(filepath.Join(directory, "memory.high"), []byte("max"), 0600)
	if verifyKernelLimits(directory, false) != nil || verifyLimits(directory) == nil {
		t.Fatal("Docker hard limits must not weaken the device MemoryHigh requirement")
	}
	os.WriteFile(filepath.Join(directory, "memory.high"), []byte(values["memory.high"]), 0600)
	os.WriteFile(filepath.Join(directory, "cpu.max"), []byte("100000 100000"), 0600)
	if verifyLimits(directory) == nil {
		t.Fatal("excessive CPU quota accepted")
	}
}

func TestLiveOwnershipCleanup(t *testing.T) {
	directory := t.TempDir()
	cache := t.TempDir()
	server := &Server{params: directory, index: &Index{cache: cache}}
	if err := server.setLive(true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "IsLiveStreaming")); err != nil || string(data) != "1" {
		t.Fatal("live flag not enabled", err)
	}
	if _, err := os.Stat(filepath.Join(cache, ".dash-live-owned")); err != nil {
		t.Fatal(err)
	}
	if err := server.setLive(false); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "IsLiveStreaming")); err != nil || string(data) != "0" {
		t.Fatal("live flag not cleared", err)
	}
	if _, err := os.Stat(filepath.Join(cache, ".dash-live-owned")); !os.IsNotExist(err) {
		t.Fatal("ownership survived cleanup")
	}
}

func TestExportsAlwaysResolveCompleteSession(t *testing.T) {
	index, _ := fixtureIndex(t)
	video := cameraFixture(t, "h264", false)
	// Lexical order differs from chronological order; include a gap to verify
	// all available parts are included without asking the user for ranges.
	for _, part := range []string{"0", "2", "10"} {
		folder := filepath.Join(index.root, fixtureDrive+"--"+part)
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "qcamera.ts"), video, 0600); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := newExports(context.Background(), index)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"zip", "raw", "mcap"} {
		job, err := manager.submit(ExportRequest{Drive: fixtureDrive, Segments: []int{2}, Format: format, Camera: "qcamera.ts"})
		if err != nil {
			t.Fatal(err)
		}
		job = waitExport(t, manager, job.ID)
		if format == "mcap" {
			file, err := os.Open(job.output)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := mcap.NewReader(file)
			if err != nil {
				t.Fatal(err)
			}
			info, err := reader.Info()
			if err != nil {
				t.Fatal(err)
			}
			if len(info.AttachmentIndexes) != 4 {
				t.Fatal("MCAP omitted session assets")
			}
			file.Close()
			continue
		}
		archive, err := zip.OpenReader(job.output)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := archive.File[0].Open()
		if err != nil {
			t.Fatal(err)
		}
		var request ExportRequest
		if err := json.NewDecoder(manifest).Decode(&request); err != nil {
			t.Fatal(err)
		}
		manifest.Close()
		if len(request.Segments) != 3 || request.Segments[0] != 0 || request.Segments[1] != 2 || request.Segments[2] != 10 {
			t.Fatal("incomplete or unordered manifest", request.Segments)
		}
		originals := 0
		for _, entry := range archive.File {
			if !strings.HasSuffix(entry.Name, "/qcamera.ts") {
				continue
			}
			original, err := entry.Open()
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(original)
			original.Close()
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != string(video) {
				t.Fatal("raw video modified")
			}
			originals++
		}
		if originals != 3 {
			t.Fatal("missing camera parts")
		}
		if format == "raw" && (len(archive.File) != 4 || !strings.HasSuffix(job.Name, "-raw-video.zip")) {
			t.Fatal("raw archive includes non-camera data or wrong extension")
		}
		archive.Close()
	}
}

func TestPlaybackReadinessPrioritizesWithoutConflictErrors(t *testing.T) {
	index, parked := fixtureIndex(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := &Server{ctx: ctx, index: index}
	parked.Store(false)
	request := httptest.NewRequest("POST", "/api/playback/prepare?drive="+fixtureDrive+"&camera=qcamera.ts", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	var result struct {
		Ready  bool   `json:"ready"`
		Status string `json:"status"`
		Total  int    `json:"total"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Status != "parkedRequired" || result.Total != 1 {
		t.Fatal("preparation should wait without an HLS conflict", response.Code, response.Body.String())
	}
	parked.Store(true)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Status != "indexing" {
		t.Fatal("parked preparation did not queue index", response.Body.String())
	}
	index.mu.Lock()
	priority := index.priorityDrive
	index.mu.Unlock()
	if priority != fixtureDrive {
		t.Fatal("opened drive not prioritized")
	}
	// Wait for the canceled scheduling goroutine before fixture DB cleanup.
	time.Sleep(20 * time.Millisecond)
}

func TestLiveViewerLeaseOwnership(t *testing.T) {
	index, _ := fixtureIndex(t)
	const owner = "11111111111111111111111111111111"
	const stranger = "22222222222222222222222222222222"
	stopped, released := 0, 0
	started := time.Now().Add(-time.Second)
	server := &Server{index: index, liveOwned: true, liveLease: owner, liveTouched: started,
		liveCancel: func() { stopped++ }, liveRelease: func() { released++ }}
	call := func(path, lease string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest("POST", path, strings.NewReader(`{"lease":"`+lease+`"}`)))
		return response
	}
	call("/api/stream/heartbeat", stranger)
	if !server.liveTouched.Equal(started) {
		t.Fatal("another viewer renewed the lease")
	}
	call("/api/stream/stop", stranger)
	call("/api/stream/stop", "")
	if stopped != 0 {
		t.Fatal("unrelated browser stopped owner")
	}
	response := call("/api/stream/heartbeat", owner)
	if !strings.Contains(response.Body.String(), `"active":true`) || !server.liveTouched.After(started) {
		t.Fatal("owner cannot renew")
	}
	call("/api/stream/stop", owner)
	if stopped != 1 || released != 1 || server.liveOwned || server.liveLease != "" {
		t.Fatal("lease did not release worker and compute slot")
	}
	call("/api/stream/stop", owner)
	if stopped != 1 || released != 1 {
		t.Fatal("worker released twice")
	}
}

func TestUnchangedCorruptLogKeepsIdleIndexStable(t *testing.T) {
	index, _ := fixtureIndex(t)
	path := filepath.Join(index.root, fixtureDrive+"--0", "qlog.bz2")
	if err := os.WriteFile(path, []byte("broken log"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, _ := index.snapshot()
	if before.Segments[fixtureDrive+"--0"].Metadata.Status != "error" {
		t.Fatal("fixture should record a parse failure")
	}
	var writesBefore int
	if err := index.db.QueryRow("SELECT total_changes()").Scan(&writesBefore); err != nil {
		t.Fatal(err)
	}
	// Block the cached lookup so the public state can be observed during an
	// ordinary inventory refresh, rather than only after it has completed.
	conn, err := index.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- index.scan(ctx) }()
	for {
		index.mu.Lock()
		scanning, status := index.scanning, index.status
		index.mu.Unlock()
		if scanning {
			if status.Running || status.Completed != before.Status.Completed || status.Total != before.Status.Total || status.Current != "" {
				t.Errorf("cached refresh changed idle UI status: %+v", status)
			}
			break
		}
		select {
		case err := <-done:
			t.Fatal("refresh finished before blocked lookup", err)
		case <-ctx.Done():
			t.Fatal("refresh did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	conn.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var writesAfter int
	if err := index.db.QueryRow("SELECT total_changes()").Scan(&writesAfter); err != nil {
		t.Fatal(err)
	}
	if writesAfter != writesBefore {
		t.Fatal("unchanged corrupt log was processed again", writesBefore, writesAfter)
	}
	if err := os.WriteFile(path, []byte("changed broken log"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	index.db.QueryRow("SELECT total_changes()").Scan(&writesAfter)
	if writesAfter <= writesBefore {
		t.Fatal("changed source was not reindexed")
	}
}
