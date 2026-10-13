package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordingPlaylistAndOnroadOriginalStreaming(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe unavailable")
	}
	index, parked := fixtureIndex(t)
	video := filepath.Join(index.root, fixtureDrive+"--0", "qcamera.ts")
	command := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=green:s=160x90:r=20", "-t", "1", "-c:v", "libx264", "-threads", "1", "-f", "mpegts", video)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	data, _ := os.ReadFile(video)
	second := filepath.Join(index.root, fixtureDrive+"--1")
	os.Mkdir(second, 0700)
	os.WriteFile(filepath.Join(second, "qcamera.ts"), data, 0600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exports, _ := newExports(ctx, index)
	app := newServer(ctx, index, exports, t.TempDir(), t.TempDir(), t.TempDir(), "")
	server := httptest.NewServer(app)
	defer server.Close()
	address := server.URL + "/api/playlist?drive=" + fixtureDrive + "&camera=qcamera.ts"
	response, _ := http.Get(address)
	response.Body.Close()
	if response.StatusCode != 409 {
		t.Fatal("published partial unindexed timeline")
	}
	if err := index.scan(ctx); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	playlist, _ := io.ReadAll(response.Body)
	response.Body.Close()
	text := string(playlist)
	if response.StatusCode != 200 || strings.Count(text, "#EXTINF:1.000000,") != 2 || strings.Count(text, "#EXT-X-DISCONTINUITY") != 1 || !strings.Contains(text, "#EXT-X-ENDLIST") {
		t.Fatal("invalid full recording playlist", text)
	}
	if strings.Index(text, fixtureDrive+"--0") > strings.Index(text, fixtureDrive+"--1") {
		t.Fatal("wrong playback order")
	}
	os.WriteFile(filepath.Join(index.root, fixtureDrive+"--0", "fcamera.hevc"), []byte("not prepared"), 0600)
	parked.Store(false)
	response, _ = http.Get(server.URL + "/api/video?file=" + fixtureDrive + "--1/qcamera.ts&container=ts")
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || len(body) != len(data) {
		t.Fatal("original TS streaming should need no onroad compute")
	}
	response, _ = http.Get(server.URL + "/api/video?file=" + fixtureDrive + "--0/fcamera.hevc&container=ts&codec=h264")
	response.Body.Close()
	if response.StatusCode != 409 {
		t.Fatal("uncached camera preparation allowed onroad")
	}
	if _, err := videoDuration(context.Background(), video); err != nil {
		t.Fatal(err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := videoDuration(canceled, video); err == nil {
		t.Fatal("probe ignored cancellation")
	}
}
