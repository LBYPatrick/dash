package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	if response.StatusCode != 410 {
		t.Fatal("retired preview encoder remains available")
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

func nativeRecordingBytes(t *testing.T, server *Server, name, camera string) []byte {
	t.Helper()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/api/playlist?drive="+strings.TrimSuffix(name, "--0")+"&camera="+camera, nil))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var output bytes.Buffer
	for _, line := range strings.Split(response.Body.String(), "\n") {
		path := ""
		if strings.HasPrefix(line, "#EXT-X-MAP:URI=\"") {
			path = strings.TrimSuffix(strings.TrimPrefix(line, "#EXT-X-MAP:URI=\""), "\"")
		} else if strings.HasPrefix(line, "/api/media/fragment?") {
			path = line
		}
		if path == "" {
			continue
		}
		part := httptest.NewRecorder()
		server.ServeHTTP(part, httptest.NewRequest("GET", path, nil))
		if part.Code != 200 {
			t.Fatal(part.Code, part.Body.String())
		}
		output.Write(part.Body.Bytes())
	}
	return output.Bytes()
}
func TestNativeHEVCServesOriginalsOnroadWithoutWorkers(t *testing.T) {
	index, parked := fixtureIndex(t)
	sample := cameraFixture(t, "hevc", false)
	data := bytes.Repeat(sample, 12)
	name := fixtureDrive + "--0"
	if err := os.WriteFile(filepath.Join(index.root, name, "fcamera.hevc"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, _ := index.snapshot()
	beforeJSON, _ := json.Marshal(before.Segments)
	server := &Server{ctx: context.Background(), index: index}
	parked.Store(false)
	// Every heavy slot is unavailable. Cached native reads must still complete.
	computeSlot <- struct{}{}
	defer func() { <-computeSlot }()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/api/playlist?drive="+fixtureDrive+"&camera=fcamera.hevc", nil))
	text := response.Body.String()
	if response.Code != 200 || !strings.Contains(text, "#EXT-X-VERSION:7") || strings.Contains(text, "codec=h264") || strings.Count(text, "/api/media/fragment?") != 3 {
		t.Fatal(text)
	}
	result := nativeRecordingBytes(t, server, name, "fcamera.hevc")
	filename := filepath.Join(t.TempDir(), "native.mp4")
	os.WriteFile(filename, result, 0600)
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name,width,height:format=duration", "-of", "json", filename).CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"codec_name": "hevc"`) {
		t.Fatal(err, string(out))
	}
	after, _ := index.snapshot()
	afterJSON, _ := json.Marshal(after.Segments)
	if !bytes.Equal(beforeJSON, afterJSON) {
		t.Fatal("native playback modified recording index")
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	if index.priorityDrive != "" || index.scanning || index.status.Running {
		t.Fatal("native playback started indexing")
	}
}

func TestReadyPlaybackDoesNotStartOrPrioritizeIndex(t *testing.T) {
	index, _ := fixtureIndex(t)
	folder := filepath.Join(index.root, fixtureDrive+"--0")
	for camera := range cameras {
		codec := "hevc"
		if camera == "qcamera.ts" {
			codec = "h264"
		}
		if err := os.WriteFile(filepath.Join(folder, camera), cameraFixture(t, codec, false), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := &Server{ctx: ctx, index: index}
	for round := 0; round < 3; round++ {
		for camera := range cameras {
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest("POST", "/api/playback/prepare?drive="+fixtureDrive+"&camera="+camera, nil))
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"ready":true`) {
				t.Fatal(response.Body.String())
			}
			response = httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest("GET", "/api/playlist?drive="+fixtureDrive+"&camera="+camera, nil))
			if response.Code != 200 {
				t.Fatal(response.Body.String())
			}
		}
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
	preview := filepath.Join(t.TempDir(), "native.mp4")
	os.WriteFile(preview, nativeRecordingBytes(t, server, fixtureDrive+"--0", "fcamera.hevc"), 0600)
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
	if len(nativeRecordingBytes(t, server, fixtureDrive+"--0", "fcamera.hevc")) == 0 {
		t.Fatal("empty AAC broke native video")
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

func TestNativeHTTPRangesAndParkedCacheRepair(t *testing.T) {
	index, parked := fixtureIndex(t)
	folder := filepath.Join(index.root, fixtureDrive+"--0")
	if err := os.WriteFile(filepath.Join(folder, "fcamera.hevc"), cameraFixture(t, "hevc", false), 0600); err != nil {
		t.Fatal(err)
	}
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := index.snapshot()
	var media *NativeMedia
	for _, file := range snapshot.Segments[fixtureDrive+"--0"].Files {
		if file.Name == "fcamera.hevc" {
			media = file.Native
		}
	}
	if media == nil || media.Status != "ready" {
		t.Fatal("native fixture not prepared")
	}
	server := &Server{ctx: context.Background(), index: index}
	path := "/api/media/fragment?file=" + fixtureDrive + "--0/fcamera.hevc&key=" + media.Key + "&fragment=0"
	request := func(method, path, rangeValue string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		if rangeValue != "" {
			req.Header.Set("Range", rangeValue)
		}
		server.ServeHTTP(response, req)
		return response
	}
	parked.Store(false)
	full := request("GET", path, "")
	if full.Code != 200 {
		t.Fatal(full.Code, full.Body.String())
	}
	partial := request("GET", path, "bytes=31-83")
	if partial.Code != 206 || !bytes.Equal(partial.Body.Bytes(), full.Body.Bytes()[31:84]) {
		t.Fatal("virtual byte range changed media", partial.Code)
	}
	head := request("HEAD", path, "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(full.Body.Len()) {
		t.Fatal("invalid HEAD", head.Code, head.Header())
	}
	nativeRequests <- struct{}{}
	nativeRequests <- struct{}{}
	busy := request("GET", path, "")
	<-nativeRequests
	<-nativeRequests
	if busy.Code != 503 {
		t.Fatal("native request concurrency is unbounded")
	}
	if response := request("GET", strings.Replace(path, "fragment=0", "fragment=-1", 1), ""); response.Code != 400 {
		t.Fatal("invalid fragment accepted")
	}
	cache := index.nativePath(media.Key)
	data, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0x40
	if err := os.WriteFile(cache, data, 0600); err != nil {
		t.Fatal(err)
	}
	broken := request("GET", path, "")
	if broken.Code != 409 {
		t.Fatal("corrupt packet inventory served")
	}
	prepared := request("POST", "/api/playback/prepare?drive="+fixtureDrive+"&camera=fcamera.hevc", "")
	if !strings.Contains(prepared.Body.String(), `"status":"parkedRequired"`) {
		t.Fatal("corrupt index triggered onroad work", prepared.Body.String())
	}
	index.mu.Lock()
	running := index.scanning
	index.mu.Unlock()
	if running {
		t.Fatal("media request started indexing")
	}
	parked.Store(true)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if response := request("GET", path, ""); response.Code != 200 {
		t.Fatal("parked repair did not recover", response.Code, response.Body.String())
	}
	// Atomicity recovers abandoned cache writes without deleting unknown files.
	orphan := filepath.Join(index.cache, "native/.index-abandoned")
	unknown := filepath.Join(index.cache, "native/keep.txt")
	os.WriteFile(orphan, []byte("partial"), 0600)
	os.WriteFile(unknown, []byte("keep"), 0600)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("abandoned native inventory retained")
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal("unrelated cache removed")
	}
}
