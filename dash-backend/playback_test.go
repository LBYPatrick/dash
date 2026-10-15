package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func TestHEVCPreviewChunksAndCachedOnroadPlayback(t *testing.T) {
	index, parked := fixtureIndex(t)
	// Independent half-second GOPs, six seconds total. A non-zero chunk must
	// contain only its requested range, not the decoded prefix.
	sample := cameraFixture(t, "hevc", false)
	var data []byte
	for n := 0; n < 12; n++ {
		data = append(data, sample...)
	}
	name := fixtureDrive + "--0"
	os.WriteFile(filepath.Join(index.root, name, "fcamera.hevc"), data, 0600)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	server := &Server{ctx: context.Background(), index: index}
	request := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		return response
	}
	response := request("/api/playlist?drive=" + fixtureDrive + "&camera=fcamera.hevc")
	text := response.Body.String()
	if response.Code != 200 || !strings.Contains(text, "#EXT-X-TARGETDURATION:4") || !strings.Contains(text, "#EXTINF:4.000000,") || !strings.Contains(text, "#EXTINF:2.000000,") || strings.Count(text, "chunk=") != 2 {
		t.Fatal(text)
	}
	for chunk, seconds := range []float64{4, 2} {
		path := "/api/video?file=" + name + "/fcamera.hevc&container=ts&codec=h264&chunk=" + strconv.Itoa(chunk)
		response := request(path)
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		filename := filepath.Join(t.TempDir(), "preview.ts")
		os.WriteFile(filename, response.Body.Bytes(), 0600)
		duration, err := videoDuration(context.Background(), filename)
		if err != nil || math.Abs(duration-seconds) > 0.11 {
			t.Fatal("wrong chunk duration", duration, err)
		}
		parked.Store(false)
		cached := request(path)
		if cached.Code != 200 || !bytes.Equal(cached.Body.Bytes(), response.Body.Bytes()) {
			t.Fatal("cached chunk cannot play onroad")
		}
		parked.Store(true)
	}
	response = request("/api/video?file=" + name + "/fcamera.hevc&container=ts&codec=h264&chunk=999999999999999999")
	if response.Code != 400 {
		t.Fatal("unbounded chunk accepted")
	}
}

func TestReadyPlaybackDoesNotStartOrPrioritizeIndex(t *testing.T) {
	index, _ := fixtureIndex(t)
	entry := IndexedSegment{MediaVersion: 1, Files: []FileInfo{{Name: "qcamera.ts", Size: 100, Duration: 1}}}
	data, _ := json.Marshal(entry)
	_, err := index.db.Exec("INSERT INTO segments(name,fingerprint,entry,summary) VALUES(?,?,?,?)", fixtureDrive+"--0", "ready", string(data), string(data))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := &Server{ctx: ctx, index: index}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("POST", "/api/playback/prepare?drive="+fixtureDrive+"&camera=qcamera.ts", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"ready":true`) {
		t.Fatal(response.Body.String())
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	if index.priorityDrive != "" || index.scanning || index.status.Running {
		t.Fatal("ready playback triggered index work")
	}
}

func TestRecordedAudioSurvivesPreviewAndMP4(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	index, _ := fixtureIndex(t)
	folder := filepath.Join(index.root, fixtureDrive+"--0")
	video := filepath.Join(folder, "qcamera.ts")
	args := []string{"-v", "error", "-f", "lavfi", "-i", "color=c=green:s=160x96:r=20", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000", "-t", "0.5", "-c:v", "libx264", "-threads", "1", "-c:a", "aac", "-ac", "1", "-f", "mpegts", video}
	if output, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("audio fixture: %v %s", err, output)
	}
	os.WriteFile(filepath.Join(folder, "fcamera.hevc"), cameraFixture(t, "hevc", false), 0600)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertAudio := func(path string) {
		t.Helper()
		output, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_name,sample_rate,channels", "-of", "json", path).CombinedOutput()
		if err != nil || !strings.Contains(string(output), `"codec_name": "aac"`) || !strings.Contains(string(output), `"sample_rate": "16000"`) {
			t.Fatal("audio missing", err, string(output))
		}
	}
	server := &Server{index: index}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/api/video?file="+fixtureDrive+"--0/fcamera.hevc&container=ts&codec=h264&chunk=0", nil))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	preview := filepath.Join(t.TempDir(), "preview.ts")
	os.WriteFile(preview, response.Body.Bytes(), 0600)
	assertAudio(preview)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exports, err := newExports(ctx, index)
	if err != nil {
		t.Fatal(err)
	}
	for _, camera := range []string{"qcamera.ts", "fcamera.hevc"} {
		request := ExportRequest{Drive: fixtureDrive, Camera: camera, Format: "mp4", Segments: []int{0}}
		assets, err := exports.collect(request)
		if err != nil {
			t.Fatal(err)
		}
		job := &ExportJob{ID: "audio-test", output: filepath.Join(t.TempDir(), "export.mp4")}
		exports.jobs[job.ID] = job
		if err := exports.mp4(ctx, job, request, assets); err != nil {
			t.Fatal(err)
		}
		assertAudio(job.output)
	}
}

func TestDeclaredEmptyAACDoesNotBreakVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	index, _ := fixtureIndex(t)
	folder := filepath.Join(index.root, fixtureDrive+"--0")
	filename := filepath.Join(folder, "qcamera.ts")
	args := []string{"-v", "error", "-f", "lavfi", "-i", "color=s=160x96:r=20", "-f", "lavfi", "-i", "sine=sample_rate=16000", "-t", "0.5", "-c:v", "libx264", "-threads", "1", "-c:a", "aac", "-ac", "1", "-f", "mpegts", filename}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	data, _ := os.ReadFile(filename)
	empty := []byte{}
	for n := 0; n+188 <= len(data); n += 188 {
		packet := data[n : n+188]
		pid := (int(packet[1]&31) << 8) | int(packet[2])
		if pid != 257 {
			empty = append(empty, packet...)
		}
	}
	os.WriteFile(filename, empty, 0600)
	present, err := recordedAudio(context.Background(), filename, index.cache)
	if err != nil || present {
		t.Fatal("empty PMT audio declaration treated as recorded sound", present, err)
	}
	os.WriteFile(filepath.Join(folder, "fcamera.hevc"), cameraFixture(t, "hevc", false), 0600)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	server := &Server{index: index}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/api/video?file="+fixtureDrive+"--0/fcamera.hevc&container=ts&codec=h264&chunk=0", nil))
	if response.Code != 200 {
		t.Fatal("declared empty AAC broke preview", response.Code, response.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exports, _ := newExports(ctx, index)
	for _, camera := range []string{"qcamera.ts", "fcamera.hevc"} {
		request := ExportRequest{Drive: fixtureDrive, Camera: camera, Format: "mp4", Segments: []int{0}}
		assets, err := exports.collect(request)
		if err != nil {
			t.Fatal(err)
		}
		job := &ExportJob{ID: "empty-audio", output: filepath.Join(t.TempDir(), "video.mp4")}
		exports.jobs[job.ID] = job
		if err := exports.mp4(ctx, job, request, assets); err != nil {
			t.Fatal("empty AAC broke export", err)
		}
	}
	// A session may begin silent and acquire microphone packets in later parts.
	// Retain both leading and internal gaps instead of shifting later audio early.
	for n := 1; n < 4; n++ {
		part := filepath.Join(index.root, fmt.Sprintf("%s--%d", fixtureDrive, n))
		os.MkdirAll(part, 0700)
		audio := data
		if n == 2 {
			audio = empty
		}
		os.WriteFile(filepath.Join(part, "qcamera.ts"), audio, 0600)
		os.WriteFile(filepath.Join(part, "fcamera.hevc"), cameraFixture(t, "hevc", false), 0600)
	}
	if err := index.scan(ctx); err != nil {
		t.Fatal(err)
	}
	for _, camera := range []string{"qcamera.ts", "fcamera.hevc"} {
		request := ExportRequest{Drive: fixtureDrive, Camera: camera, Format: "mp4", Segments: []int{0, 1, 2, 3}}
		assets, err := exports.collect(request)
		if err != nil {
			t.Fatal(err)
		}
		job := &ExportJob{ID: "sparse-audio", output: filepath.Join(t.TempDir(), "sparse.mp4")}
		exports.jobs[job.ID] = job
		if err := exports.mp4(ctx, job, request, assets); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0", "-show_packets", "-show_entries", "packet=pts_time", "-of", "json", job.output).CombinedOutput()
		if err != nil {
			t.Fatal(err, string(out))
		}
		var packets struct {
			Packets []struct {
				PTS string `json:"pts_time"`
			} `json:"packets"`
		}
		if json.Unmarshal(out, &packets) != nil || len(packets.Packets) < 2 {
			t.Fatal("missing sparse AAC", string(out))
		}
		first, _ := strconv.ParseFloat(packets.Packets[0].PTS, 64)
		last, _ := strconv.ParseFloat(packets.Packets[len(packets.Packets)-1].PTS, 64)
		gap, previous := 0.0, first
		for _, packet := range packets.Packets {
			pts, _ := strconv.ParseFloat(packet.PTS, 64)
			gap = max(gap, pts-previous)
			previous = pts
		}
		if first < 0.35 || last < 1.5 || gap < 0.35 || last > 3 {
			t.Fatal("sparse audio shifted or stretched", camera, first, last, gap)
		}
	}

}

func TestLateStartingAACIsFoundAndExported(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	index, _ := fixtureIndex(t)
	folder := filepath.Join(index.root, fixtureDrive+"--0")
	video := filepath.Join(folder, "qcamera.ts")
	args := []string{"-v", "error", "-f", "lavfi", "-i", "color=s=160x96:r=20", "-itsoffset", "6", "-f", "lavfi", "-i", "sine=sample_rate=16000", "-t", "8", "-c:v", "libx264", "-threads", "1", "-c:a", "aac", "-ac", "1", "-f", "mpegts", video}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if present, err := recordedAudio(context.Background(), video, index.cache); err != nil || !present {
		t.Fatal("late microphone data missed", present, err)
	}
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exports, _ := newExports(ctx, index)
	request := ExportRequest{Drive: fixtureDrive, Camera: "qcamera.ts", Format: "mp4", Segments: []int{0}}
	assets, err := exports.collect(request)
	if err != nil {
		t.Fatal(err)
	}
	job := &ExportJob{ID: "late-audio-test", output: filepath.Join(t.TempDir(), "export.mp4")}
	exports.jobs[job.ID] = job
	if err := exports.mp4(ctx, job, request, assets); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "packet=pts_time", "-of", "csv=p=0", job.output).Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(output))
	if len(lines) == 0 {
		t.Fatal("late audio discarded")
	}
	first, err := strconv.ParseFloat(strings.TrimSuffix(lines[0], ","), 64)
	if err != nil || first < 5.5 || first > 6.2 {
		t.Fatal("late audio shifted earlier", first, err)
	}
}
