package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/foxglove/mcap/go/mcap"
)

func TestStreamExportsCompleteSessionWithoutOutputFiles(t *testing.T) {
	index, _ := fixtureIndex(t)
	_ = cameraFixture(t, "h264", false) // Verify optional media tools are present.
	videoPath := filepath.Join(t.TempDir(), "audio.ts")
	if output, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=green:s=160x96:r=20", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000", "-t", "0.5", "-c:v", "libx264", "-threads", "1", "-c:a", "aac", "-f", "mpegts", videoPath).CombinedOutput(); err != nil {
		t.Fatalf("audio fixture: %v %s", err, output)
	}
	video, err := os.ReadFile(videoPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"0", "2"} {
		folder := filepath.Join(index.root, fixtureDrive+"--"+part)
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "qcamera.ts"), video, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, _ := newExports(ctx, index)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manager.downloadStream(w, r, strings.TrimPrefix(r.URL.Path, "/"))
	}))
	defer server.Close()
	for _, format := range []string{"zip", "raw", "mp4", "mcap"} {
		t.Run(format, func(t *testing.T) {
			job, err := manager.submit(ExportRequest{Drive: fixtureDrive, Segments: []int{2}, Format: format, Camera: "qcamera.ts", Stream: true})
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.Get(server.URL + "/" + job.ID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 || len(data) == 0 {
				t.Fatalf("stream failed: HTTP %d, bytes=%d, %v", response.StatusCode, len(data), err)
			}
			completed := waitExport(t, manager, job.ID)
			if completed.Bytes != int64(len(data)) {
				t.Fatal("download size does not match bytes sent", completed.Bytes, len(data))
			}
			switch format {
			case "zip", "raw":
				archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				if err != nil {
					t.Fatal(err)
				}
				parts := 0
				for _, entry := range archive.File {
					if strings.HasSuffix(entry.Name, "/qcamera.ts") {
						parts++
						file, _ := entry.Open()
						copied, err := io.ReadAll(file)
						file.Close()
						if err != nil || !bytes.Equal(copied, video) {
							t.Fatal("original footage changed in transit", err)
						}
					}
				}
				if parts != 2 {
					t.Fatal("caller range omitted session parts", parts)
				}
			case "mp4":
				path := filepath.Join(t.TempDir(), "download.mp4")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				output, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name:format=duration", "-of", "csv=p=0", path).Output()
				if err != nil || !strings.Contains(string(output), "h264") || !strings.Contains(string(output), "aac") {
					t.Fatalf("invalid streamed video/audio: %s %v", output, err)
				}
			case "mcap":
				reader, err := mcap.NewReader(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				info, err := reader.Info()
				if err != nil || len(info.AttachmentIndexes) != 3 || info.Statistics.MessageCount == 0 {
					t.Fatal("incomplete MCAP footer, attachments, or messages", err)
				}
			}
			entries, _ := os.ReadDir(manager.folder)
			for _, entry := range entries {
				if !strings.HasSuffix(entry.Name(), ".audio.json") {
					t.Fatal("stream left output files on the device", entries)
				}
			}
			repeat, err := http.Get(server.URL + "/" + job.ID)
			if err != nil {
				t.Fatal(err)
			}
			repeat.Body.Close()
			if repeat.StatusCode != http.StatusConflict {
				t.Fatal("download was claimed twice")
			}
		})
	}
}

func TestStreamDisconnectAndIgnitionStopWorker(t *testing.T) {
	for _, disconnect := range []bool{true, false} {
		t.Run(map[bool]string{true: "disconnect", false: "ignition"}[disconnect], func(t *testing.T) {
			index, parked := fixtureIndex(t)
			path := filepath.Join(index.root, fixtureDrive+"--0/large.bin")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(128 << 20); err != nil {
				t.Fatal(err)
			}
			file.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			manager, _ := newExports(ctx, index)
			job, err := manager.submit(ExportRequest{Drive: fixtureDrive, Format: "zip", Stream: true})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { manager.downloadStream(w, r, job.ID) }))
			defer server.Close()
			response, err := http.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if _, err := io.CopyN(io.Discard, response.Body, 64<<10); err != nil {
				t.Fatal(err)
			}
			if disconnect {
				response.Body.Close()
			} else {
				parked.Store(false)
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				state, _ := manager.snapshot(job.ID)
				if state.Status == "canceled" {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("stream retained heavy worker after disconnect/ignition")
		})
	}
}

func TestUnclaimedStreamExpires(t *testing.T) {
	index, _ := fixtureIndex(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, _ := newExports(ctx, index)
	job, err := manager.submit(ExportRequest{Drive: fixtureDrive, Format: "zip", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.jobs[job.ID].expires.Reset(time.Millisecond)
	manager.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for manager.busy() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if manager.busy() {
		t.Fatal("unclaimed download retained queue")
	}
}

func TestStreamFailureAbortsIncompleteDownload(t *testing.T) {
	index, _ := fixtureIndex(t)
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe unavailable")
	}
	if err := os.WriteFile(filepath.Join(index.root, fixtureDrive+"--0/qcamera.ts"), []byte("invalid video"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, _ := newExports(ctx, index)
	job, err := manager.submit(ExportRequest{Drive: fixtureDrive, Format: "mcap", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { manager.downloadStream(w, r, job.ID) }))
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err == nil {
		t.Fatal("incomplete MCAP was delivered as a successful download")
	}
	state, _ := manager.snapshot(job.ID)
	if state.Status != "failed" {
		t.Fatal("corrupt footage failure was not reported", state.Status)
	}
}

func TestRecordingReserveProtectsFirmwareCleanupFloor(t *testing.T) {
	const gib = uint64(1 << 30)
	for _, test := range []struct{ total, available, want uint64 }{
		{100 * gib, 10 * gib, 0},
		{100 * gib, 11 * gib, gib - RecordingReserveMargin},
		{100 * gib, 10*gib + 100<<20, (100 << 20) - RecordingReserveMargin},
		{32 * gib, 5 * gib, 0},
		{32 * gib, 6 * gib, gib - RecordingReserveMargin},
	} {
		if got := recordingSpaceBudget(test.total, test.available); got != test.want {
			t.Fatalf("budget(%d,%d)=%d, want %d", test.total, test.available, got, test.want)
		}
	}
}
