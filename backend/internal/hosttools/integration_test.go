package hosttools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func integration(t *testing.T, mode string) *Tools {
	t.Helper()
	if os.Getenv("DASH_INTEGRATION") != mode {
		t.Skip("opt-in integration: make " + mode + "-smoke")
	}
	root, err := ProjectRoot()
	if err != nil {
		t.Fatal(err)
	}
	return New(root)
}
func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func assertThat(t *testing.T, condition bool, message string) {
	t.Helper()
	if !condition {
		t.Fatal(message)
	}
}
func output(t *testing.T, tools *Tools, name string, args ...string) []byte {
	t.Helper()
	data, err := tools.command(tools.Root, nil, name, args...)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return data
}
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("integration check timed out")
}
func writeText(t *testing.T, name, text string) {
	t.Helper()
	require(t, os.WriteFile(name, []byte(text), 0600))
}
func nested(data map[string]any, keys ...string) any {
	var current any = data
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	return current
}

type localAPI struct{ base string }

func (api localAPI) request(endpoint string, body any) (int, []byte, error) {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		input = bytes.NewReader(data)
	}
	method := "GET"
	if body != nil {
		method = "POST"
	}
	request, err := http.NewRequest(method, api.base+endpoint, input)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxArchive+1))
	return response.StatusCode, data, err
}
func (api localAPI) get(endpoint string) map[string]any {
	_, data, err := api.request(endpoint, nil)
	if err != nil {
		return nil
	}
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		return nil
	}
	return result
}
func (api localAPI) post(t *testing.T, endpoint string, body any, status int) []byte {
	t.Helper()
	actual, data, err := api.request(endpoint, body)
	require(t, err)
	if actual != status {
		t.Fatalf("%s: HTTP %d, want %d: %.200s", endpoint, actual, status, data)
	}
	return data
}
func (api localAPI) state(t *testing.T, state string) {
	t.Helper()
	api.post(t, "/api/simulation/state", map[string]string{"state": state}, 200)
}
func TestSandboxIntegration(t *testing.T) {
	tools := integration(t, "sandbox")
	api := localAPI{"http://127.0.0.1:" + envDefault("DASH_SANDBOX_PORT", "8089")}
	assertThat(t, nested(api.get("/api/info"), "simulation") == true, "refusing to mutate a non-synthetic backend")
	defer func() { api.request("/api/simulation/state", map[string]string{"state": "unknown"}) }()
	drive := "000001a3--c20ba54385"
	for _, state := range []string{"unknown", "onroad"} {
		api.state(t, state)
		api.post(t, "/api/exports", map[string]any{"drive": drive, "segments": []int{0}, "format": "zip"}, 409)
		api.post(t, "/api/index/rebuild", map[string]any{}, 409)
		assertThat(t, nested(api.get("/api/index"), "status", "driving") == state, "incorrect driving state")
	}
	api.state(t, "parked")
	api.post(t, "/api/index/rebuild", map[string]any{}, 200)
	waitFor(t, func() bool {
		data := api.get("/api/index")
		return nested(data, "status", "completed") == float64(6) && nested(data, "status", "running") == false && nested(data, "status", "errors") == float64(0)
	})
	index := api.get("/api/index")
	entries, ok := index["segments"].(map[string]any)
	assertThat(t, ok && len(entries) == 6, "missing indexed segments")
	car := false
	for name, raw := range entries {
		entry, ok := raw.(map[string]any)
		assertThat(t, ok, "invalid index entry")
		thumbnail, hasThumbnail := nested(entry, "thumbnail").(string)
		utc, hasUTC := nested(entry, "metadata", "startUTC").(float64)
		files, hasFiles := entry["files"].([]any)
		hasRawLog := false
		for _, raw := range files {
			if file, ok := raw.(map[string]any); ok && file["name"] == "rlog.zst" {
				hasRawLog = true
			}
		}
		assertThat(t, hasThumbnail && thumbnail != "" && hasUTC && utc > 0 && hasFiles && hasRawLog && nested(entry, "metadata", "version") == "dash-synthetic-sunnypilot", "missing native log, thumbnail or timing metadata")
		car = car || nested(entry, "metadata", "car") == "DASH SYNTHETIC VEHICLE"
		duration := float64(8)
		if strings.HasSuffix(name, "--0") {
			duration = 60
		}
		assertThat(t, nested(entry, "metadata", "duration") == duration, "incorrect recording duration")
	}
	assertThat(t, car, "missing synthetic vehicle")
	status, preview, err := api.request("/api/video?file="+drive+"--0/qcamera.ts", nil)
	require(t, err)
	assertThat(t, status == 200 && len(preview) > 1000, "native preview failed")
	for _, format := range []string{"zip", "mp4", "mcap"} {
		raw := api.post(t, "/api/exports", map[string]any{"drive": drive, "segments": []int{0, 1}, "format": format, "camera": "qcamera.ts"}, 202)
		var job struct {
			ID string `json:"id"`
		}
		require(t, json.Unmarshal(raw, &job))
		waitFor(t, func() bool {
			result := api.get("/api/exports/" + job.ID)
			state := nested(result, "status")
			assertThat(t, state != "failed" && state != "canceled", fmt.Sprint(result))
			return state == "complete"
		})
		status, data, err := api.request("/api/exports/"+job.ID+"/download", nil)
		require(t, err)
		assertThat(t, status == 200 && len(data) > 100, "empty export")
		if format == "mcap" {
			require(t, os.WriteFile(tools.path(".artifacts/sandbox-smoke.mcap"), data, 0600))
		}
	}
	status, report, err := api.request("/api/diagnostics/download", nil)
	require(t, err)
	assertThat(t, status == 200 && bytes.Contains(report, []byte("Simulation: true")) && bytes.Contains(report, []byte("verified container limits")) && bytes.Contains(report, []byte("memory.max: 402653184")), "diagnostics missing enforced resource limits")
	id := strings.TrimSpace(string(output(t, tools, "docker", "compose", "ps", "-q", "dash")))
	var info []containerInfo
	require(t, json.Unmarshal(output(t, tools, "docker", "inspect", id), &info))
	assertThat(t, len(info) == 1, "missing sandbox container")
	checkContainer(t, info[0])
	var peak int64
	_, err = fmt.Sscan(string(output(t, tools, "docker", "exec", id, "cat", "/sys/fs/cgroup/memory.peak")), &peak)
	require(t, err)
	assertThat(t, peak <= 384<<20, "sandbox exceeded memory budget")
	data, err := tools.command(tools.Root, nil, "docker", "run", "--rm", "--network", "none", "--read-only", "--tmpfs", "/tmp:size=16m,mode=1777", "--entrypoint", "dash-backend", info[0].Config.Image, "--cache", "/tmp", "--require-container-limits")
	assertThat(t, err != nil && bytes.Contains(data, []byte("container resource verification: CPU quota")), "unbounded sandbox was accepted")
	output(t, tools, "docker", "compose", "restart", "dash")
	waitFor(t, func() bool { return api.get("/api/info") != nil })
	assertThat(t, nested(api.get("/api/info"), "driving") == "unknown", "restart trusted stale parked state")
	_, retained, err := api.request("/api/diagnostics/download", nil)
	require(t, err)
	assertThat(t, bytes.Contains(retained, []byte("format=zip")), "logs did not survive restart")
}

type containerInfo struct {
	ID           string `json:"Id"`
	Architecture string
	State        struct{ OOMKilled bool }
	Config       struct{ Image string }
	HostConfig   struct {
		NanoCpus, Memory, MemorySwap, PidsLimit int64
		Privileged, ReadonlyRootfs              bool
	}
	NetworkSettings struct {
		Ports map[string][]struct{ HostPort string }
	}
}

func checkContainer(t *testing.T, info containerInfo) {
	t.Helper()
	c := info.HostConfig
	assertThat(t, !info.State.OOMKilled && c.Memory == 384<<20 && c.MemorySwap == 384<<20 && c.NanoCpus == 500000000 && c.PidsLimit == 64 && !c.Privileged && c.ReadonlyRootfs, "container limits are incorrect or an OOM occurred")
}
func TestMediaIntegration(t *testing.T) {
	tools := integration(t, "media")
	root, err := os.MkdirTemp(tools.path(".artifacts"), "media-smoke-*")
	require(t, err)
	defer os.RemoveAll(root)
	for _, media := range []struct{ name, codec, format string }{{"preview.ts", "libx264", "mpegts"}, {"road.hevc", "libx265", "hevc"}} {
		args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=s=320x180:r=20", "-t", "0.5", "-an", "-c:v", media.codec, "-threads", "1", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-bf", "0"}
		if media.codec == "libx265" {
			args = append(args, "-x265-params", "pools=none:frame-threads=1:log-level=error")
		}
		args = append(args, "-f", media.format, filepath.Join(root, media.name))
		output(t, tools, "ffmpeg", args...)
	}
	script := `set -eu
/media/ffmpeg -v error -nostdin -y -max_alloc 33554432 -threads 8 -filter_threads 8 -i /test/preview.ts -frames:v 1 -vf scale=160:-2 /test/thumb.jpg
/media/ffmpeg -v error -nostdin -y -max_alloc 33554432 -threads 4 -filter_threads 1 -probesize 1048576 -analyzeduration 1000000 -i /test/preview.ts -map 0:v:0 -an -sn -dn -t 6.5 -frames:v 13 -vf 'fps=2:round=up:eof_action=pass,scale=320:240:force_original_aspect_ratio=decrease:force_divisible_by=2' -c:v mjpeg -q:v 5 -threads 1 -f image2pipe /test/samples.mjpg
/media/ffmpeg -v error -nostdin -y -max_alloc 33554432 -threads 8 -i /test/preview.ts -c:v copy -an /test/preview.mp4
/media/ffmpeg -v error -nostdin -y -max_alloc 33554432 -threads 8 -filter_threads 8 -r 20 -i /test/road.hevc -an -c:v libx264 -threads 8 -preset ultrafast -bf 0 -pix_fmt yuv420p -vf scale=160:-2 -f mpegts /test/converted.ts
/media/ffmpeg -v error -nostdin -y -i /test/preview.ts -an -c:v copy -bsf:v h264_metadata=aud=insert -f h264 /test/video.h264
/media/ffmpeg -v error -nostdin -y -i /test/road.hevc -an -c:v copy -bsf:v hevc_metadata=aud=insert -f hevc /test/video.hevc
for file in preview.mp4 converted.ts video.h264 video.hevc; do
 /media/ffprobe -v error -threads 1 -count_packets -select_streams v:0 -show_entries stream=nb_read_packets -of csv=p=0 "/test/$file" | grep -qx 10
done
test -s /test/thumb.jpg
test -s /test/samples.mjpg
`
	writeText(t, filepath.Join(root, "verify.sh"), script)
	architectures := []string{"arm64", "amd64"}
	if arch := os.Getenv("MEDIA_ARCH"); arch != "" {
		architectures = []string{arch}
	}
	for _, arch := range architectures {
		media, err := tools.BuildMedia(arch)
		require(t, err)
		output(t, tools, "docker", "run", "--rm", "--network", "none", "--platform", "linux/"+arch, "--memory", "384m", "--memory-swap", "384m", "--pids-limit", "128", "-v", media+":/media:ro", "-v", root+":/test", "alpine:3.23", "sh", "/test/verify.sh")
	}
}
func TestSourceInstallIntegration(t *testing.T) {
	tools := integration(t, "source-install")
	root := t.TempDir()
	require(t, os.MkdirAll(filepath.Join(root, "bin"), 0755))
	binary, err := os.Executable()
	require(t, err)
	for _, name := range []string{"ssh", "scp"} {
		require(t, os.Symlink(binary, filepath.Join(root, "bin", name)))
	}
	data, err := tools.command(tools.Root, []string{"PATH=" + filepath.Join(root, "bin") + ":" + os.Getenv("PATH"), "DASH_INSTALL_TEST_ROOT=" + root, "DASH_INTEGRATION=", "GITHUB_ACTIONS="}, "bash", "scripts/install-backend.sh", "--source", "comma@fixture")
	if err != nil {
		t.Fatalf("source installer: %v\n%s", err, data)
	}
	transfer := filepath.Join(root, "transfer")
	var build Identity
	require(t, readJSON(filepath.Join(transfer, "payload/release.json"), &build, 16384))
	assertThat(t, build.Channel == "local" && build.Arch == "arm64" && strings.Contains(build.Version, "-local."), "source build identity is incorrect")
	var config struct{ Automatic bool }
	require(t, readJSON(filepath.Join(transfer, ".update.new"), &config, 16384))
	assertThat(t, !config.Automatic, "source install enabled OTA")
	service, err := os.ReadFile(filepath.Join(transfer, ".service.new"))
	require(t, err)
	assertThat(t, bytes.Contains(service, []byte("dash-bootstrap --supervise")), "incorrect service lifecycle")
	html, err := os.ReadFile(filepath.Join(transfer, "payload/dist/index.html"))
	require(t, err)
	assertThat(t, bytes.Contains(html, []byte("<html")), "missing static app")
	for _, name := range []string{"activate.sh", "preflight.sh"} {
		output(t, tools, "bash", "-n", filepath.Join(root, name))
	}
	calls, err := os.ReadFile(filepath.Join(root, "ssh.jsonl"))
	require(t, err)
	assertThat(t, len(bytes.Split(bytes.TrimSpace(calls), []byte("\n"))) == 3, "unexpected SSH transport calls")
}
