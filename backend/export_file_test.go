package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/foxglove/mcap/go/mcap"
)

func TestMCAPPreparesCompleteFileBeforeDownload(t *testing.T) {
	index, _ := fixtureIndex(t)
	video := cameraFixture(t, "hevc", false)
	for _, part := range []string{"0", "2"} {
		folder := filepath.Join(index.root, fixtureDrive+"--"+part)
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "fcamera.hevc"), video, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, err := newExports(ctx, index)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newServer(ctx, index, manager, t.TempDir(), t.TempDir(), t.TempDir(), ""))
	defer server.Close()
	release, err := acquireCompute(ctx)
	if err != nil {
		t.Fatal(err)
	}
	release = sync.OnceFunc(release)
	defer release()
	// Even a stale PWA asking for streaming must receive a file-based job.
	job, err := manager.submit(ExportRequest{Drive: fixtureDrive, Segments: []int{2}, Format: "mcap", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if job.Streaming || job.request.Stream || job.expires != nil {
		t.Fatal("MCAP still depends on claiming a streaming connection")
	}
	url := server.URL + "/api/exports/" + job.ID + "/download"
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatal("unfinished MCAP was downloadable", response.Status)
	}
	release()
	job = waitExport(t, manager, job.ID)
	data, err := os.ReadFile(job.output)
	if err != nil || int64(len(data)) != job.Bytes {
		t.Fatal("complete output file missing", err)
	}
	reader, err := mcap.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	info, err := reader.Info()
	if err != nil || len(info.AttachmentIndexes) != 3 || info.Statistics.MessageCount == 0 {
		t.Fatal("MCAP footer, complete-session attachments or messages missing", err)
	}
	for range 2 {
		response, err = http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		download, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || response.ContentLength != job.Bytes || !bytes.Equal(download, data) {
			t.Fatal("completed download must be retryable and have a known size", err)
		}
	}
	request, _ := http.NewRequest(http.MethodGet, url, nil)
	request.Header.Set("Range", "bytes=0-7")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	partial, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusPartialContent || response.Header.Get("Content-Range") != "bytes 0-7/"+strconv.FormatInt(job.Bytes, 10) || !bytes.Equal(partial, data[:8]) {
		t.Fatal("completed download must support byte-range resume", err, response.Status)
	}
}

func TestMCAPFailureLeavesNoDownload(t *testing.T) {
	index, _ := fixtureIndex(t)
	_ = cameraFixture(t, "hevc", false) // Confirm media tooling is available.
	if err := os.WriteFile(filepath.Join(index.root, fixtureDrive+"--0/fcamera.hevc"), []byte("corrupt video"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, _ := newExports(ctx, index)
	job, err := manager.submit(ExportRequest{Drive: fixtureDrive, Format: "mcap", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, _ = manager.snapshot(job.ID)
		if job.Status == "failed" {
			if _, err := os.Stat(job.output); !os.IsNotExist(err) {
				t.Fatal("incomplete MCAP retained", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("corrupt MCAP did not fail before download", job.Status)
}
