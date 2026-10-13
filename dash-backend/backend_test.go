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

	"github.com/foxglove/mcap/go/mcap"
)

const fixtureDrive = "2026-10-12--17-42-08"

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
			if len(info.Schemas) != 3 || len(info.AttachmentIndexes) != 1 {
				t.Fatal("missing MCAP schemas or attachments")
			}
			for _, schema := range info.Schemas {
				if !strings.HasPrefix(schema.Name, "dash.v1.") {
					t.Fatal("foreign MCAP schema")
				}
			}
			messages, err := reader.Messages()
			if err != nil {
				t.Fatal(err)
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
				if schema.Name == "dash.v1.Position" {
					positions++
					var point map[string]float64
					if json.Unmarshal(message.Data, &point) != nil || point["latitude"] < 37 {
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
	server := &Server{params: directory}
	if err := server.setLive(true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "IsLiveStreaming")); err != nil || string(data) != "1" {
		t.Fatal("live flag not enabled", err)
	}
	if _, err := os.Stat(filepath.Join(directory, ".dash-live-owned")); err != nil {
		t.Fatal(err)
	}
	if err := server.setLive(false); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "IsLiveStreaming")); err != nil || string(data) != "0" {
		t.Fatal("live flag not cleared", err)
	}
	if _, err := os.Stat(filepath.Join(directory, ".dash-live-owned")); !os.IsNotExist(err) {
		t.Fatal("ownership survived cleanup")
	}
}
