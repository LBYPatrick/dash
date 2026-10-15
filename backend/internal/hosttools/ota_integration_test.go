package hosttools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOTAIntegration(t *testing.T) {
	tools := integration(t, "ota")
	var images []containerInfo
	require(t, json.Unmarshal(output(t, tools, "docker", "image", "inspect", "dash-sandbox-dash"), &images))
	assertThat(t, len(images) == 1 && validArch(images[0].Architecture), "unsupported sandbox image")
	image, arch := images[0].ID, images[0].Architecture
	root := tools.path(".artifacts/ota-sandbox")
	require(t, os.RemoveAll(root))
	for _, directory := range []string{"releases", "cache", "params"} {
		require(t, os.MkdirAll(filepath.Join(root, directory), 0755))
	}
	logfile := tools.path(".artifacts/ota-sandbox.log")
	writeText(t, logfile, "")
	writeText(t, filepath.Join(root, "params/IsOnroad"), "0")
	writeText(t, filepath.Join(root, "params/IsOffroad"), "1")
	require(t, writeJSON(filepath.Join(root, "update.json"), map[string]any{"repository": "LBYPatrick/dash", "channel": "dev", "automatic": false}))
	writeText(t, filepath.Join(root, "cache/ota-sentinel"), "keep this cache")
	t.Setenv("GITHUB_REF_NAME", "")
	t.Setenv("GITHUB_RUN_NUMBER", "")
	t.Setenv("DASH_RELEASE_CHANNEL", "dev")
	t.Setenv("GITHUB_ACTIONS", "")
	manifests := []Manifest{}
	for _, sequence := range []string{"1", "2"} {
		t.Setenv("DASH_RELEASE_SEQUENCE", sequence)
		require(t, tools.Release(false))
		var manifest Manifest
		require(t, readJSON(tools.path(".artifacts/release/manifest.json"), &manifest, 65536))
		expected, err := validateIdentity(manifest, "dev", arch, "")
		require(t, err)
		target := filepath.Join(root, "releases", manifest.Tag)
		require(t, os.Mkdir(target, 0755))
		require(t, extractPayload(tools.path(".artifacts/release", manifest.Assets[arch].Name), target, expected))
		manifests = append(manifests, manifest)
	}
	first, second := manifests[0], manifests[1]
	t.Setenv("DASH_RELEASE_ARCH", arch)
	t.Setenv("DASH_RELEASE_SEQUENCE", "123456")
	require(t, tools.Release(true))
	var local Manifest
	require(t, readJSON(tools.path(".artifacts/release/manifest.json"), &local, 65536))
	localTarget := filepath.Join(root, "releases", local.Tag)
	_, err := tools.FetchRelease(FetchOptions{Channel: "main", Repository: "LBYPatrick/dash", Arch: arch, Destination: localTarget, LocalManifest: tools.path(".artifacts/release/manifest.json")})
	require(t, err)
	require(t, copyFile(filepath.Join(root, "releases", first.Tag, "dash-backend"), filepath.Join(root, "dash-bootstrap")))
	require(t, os.Chmod(filepath.Join(root, "dash-bootstrap"), 0700))
	require(t, os.Symlink("releases/"+first.Tag, filepath.Join(root, "current")))
	require(t, writeJSON(filepath.Join(root, "ready.json"), second))
	container := ""
	saveLogs := func() {
		if container == "" {
			return
		}
		data, _ := tools.command(tools.Root, nil, "docker", "logs", container)
		file, err := os.OpenFile(logfile, os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			file.Write(data)
			file.Close()
		}
	}
	defer func() {
		saveLogs()
		if container != "" {
			tools.command(tools.Root, nil, "docker", "stop", "-t", "5", container)
		}
	}()
	launch := func(home string) localAPI {
		t.Helper()
		args := []string{"run", "-d", "--rm", "--name", fmt.Sprintf("dash-ota-test-%d", os.Getpid()), "--init", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges:true", "--cpus=0.5", "--memory=384m", "--memory-swap=384m", "--pids-limit=64", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "-v", home + ":/data/dash", "-p", "127.0.0.1::8088", "--entrypoint", "/data/dash/dash-bootstrap", image, "--supervise", "--update-home", "/data/dash", "--require-container-limits", "--simulation", "--listen", "0.0.0.0:8088", "--root", "/recordings", "--cache", "/data/dash/cache", "--params", "/data/dash/params"}
		container = strings.TrimSpace(string(output(t, tools, "docker", args...)))
		var info []containerInfo
		require(t, json.Unmarshal(output(t, tools, "docker", "inspect", container), &info))
		assertThat(t, len(info) == 1 && len(info[0].NetworkSettings.Ports["8088/tcp"]) == 1, "missing local container port")
		return localAPI{"http://127.0.0.1:" + info[0].NetworkSettings.Ports["8088/tcp"][0].HostPort}
	}
	restart := func() localAPI {
		t.Helper()
		output(t, tools, "docker", "restart", container)
		var info []containerInfo
		require(t, json.Unmarshal(output(t, tools, "docker", "inspect", container), &info))
		return localAPI{"http://127.0.0.1:" + info[0].NetworkSettings.Ports["8088/tcp"][0].HostPort}
	}
	linkIs := func(name, tag string) {
		t.Helper()
		link, err := os.Readlink(filepath.Join(root, name))
		require(t, err)
		assertThat(t, link == "releases/"+tag, "incorrect "+name+" release")
	}
	cacheRetained := func() {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, "cache/ota-sentinel"))
		require(t, err)
		assertThat(t, string(data) == "keep this cache", "cache lost during update")
	}
	ready := func(home, tag string) bool {
		var status struct{ Tag string }
		return readJSON(filepath.Join(home, "cache/supervisor-ready.json"), &status, 16384) == nil && status.Tag == tag
	}
	api := launch(root)
	waitFor(t, func() bool { return nested(api.get("/api/info"), "build", "tag") == first.Tag })
	assertThat(t, nested(api.get("/api/updates"), "phase") == "ready", "candidate not ready")
	api.post(t, "/api/updates", map[string]string{"action": "install"}, 202)
	waitFor(t, func() bool {
		state := api.get("/api/updates")
		return nested(state, "current", "tag") == second.Tag && nested(state, "pendingHealth") == false
	})
	linkIs("current", second.Tag)
	linkIs("previous", first.Tag)
	waitFor(t, func() bool { return ready(root, second.Tag) })
	cacheRetained()
	raw, err := json.Marshal(second)
	require(t, err)
	var bad Manifest
	require(t, json.Unmarshal(raw, &bad))
	bad.Sequence = 3
	bad.Version = strings.Replace(second.Version, "-dev.2.", "-dev.3.", 1)
	bad.Tag = "v" + bad.Version
	broken := filepath.Join(root, "releases", bad.Tag)
	require(t, copyTree(filepath.Join(root, "releases", second.Tag), broken))
	falseBinary := output(t, tools, "docker", "run", "--rm", "--entrypoint", "/bin/cat", image, "/bin/false")
	require(t, os.WriteFile(filepath.Join(broken, "dash-backend"), falseBinary, 0700))
	build := bad.Identity
	build.Arch = arch
	require(t, writeJSON(filepath.Join(broken, "release.json"), build))
	asset := bad.Assets[arch]
	asset.Name = archiveName(bad.Identity, arch)
	bad.Assets[arch] = asset
	require(t, writeJSON(filepath.Join(root, "ready.json"), bad))
	api.state(t, "unknown")
	api = restart()
	waitFor(t, func() bool { return nested(api.get("/api/info"), "build", "tag") == second.Tag })
	api.post(t, "/api/updates", map[string]string{"action": "install"}, 409)
	assertThat(t, nested(api.get("/api/index"), "status", "running") == false, "unknown state started indexing")
	children := string(output(t, tools, "docker", "exec", container, "sh", "-c", `for item in /proc/[0-9]*/comm; do cat "$item"; done`))
	assertThat(t, !strings.Contains("\n"+children, "\nffmpeg\n"), "unknown state left an encoder running")
	api.state(t, "parked")
	api.post(t, "/api/updates", map[string]string{"action": "install"}, 202)
	waitFor(t, func() bool {
		state := api.get("/api/updates")
		return nested(state, "current", "tag") == second.Tag && strings.Contains(fmt.Sprint(nested(state, "message")), "restored")
	})
	linkIs("current", second.Tag)
	cacheRetained()
	api.state(t, "unknown")
	writeText(t, filepath.Join(root, "update.json"), "{broken")
	api = restart()
	waitFor(t, func() bool { return nested(api.get("/api/info"), "build", "tag") == second.Tag })
	assertThat(t, nested(api.get("/api/updates"), "managed") == false, "corrupt config enabled OTA")
	assertThat(t, strings.Contains(fmt.Sprint(nested(api.get("/api/updates"), "message")), "unavailable"), "missing config explanation")
	api.post(t, "/api/updates", map[string]string{"action": "check"}, 409)
	saveLogs()
	output(t, tools, "docker", "stop", "-t", "5", container)
	container = ""
	sourceRoot := tools.path(".artifacts/source-sandbox")
	require(t, os.RemoveAll(sourceRoot))
	require(t, os.MkdirAll(filepath.Join(sourceRoot, "releases"), 0755))
	require(t, copyTree(localTarget, filepath.Join(sourceRoot, "releases", local.Tag)))
	require(t, copyFile(filepath.Join(root, "dash-bootstrap"), filepath.Join(sourceRoot, "dash-bootstrap")))
	require(t, os.Mkdir(filepath.Join(sourceRoot, "cache"), 0755))
	require(t, copyTree(filepath.Join(root, "params"), filepath.Join(sourceRoot, "params")))
	require(t, writeJSON(filepath.Join(sourceRoot, "update.json"), map[string]any{"repository": "LBYPatrick/dash", "channel": "main", "automatic": true}))
	require(t, os.Symlink("releases/"+local.Tag, filepath.Join(sourceRoot, "current")))
	api = launch(sourceRoot)
	waitFor(t, func() bool { return nested(api.get("/api/info"), "build", "tag") == local.Tag })
	waitFor(t, func() bool { return ready(sourceRoot, local.Tag) })
	assertThat(t, nested(api.get("/api/updates"), "managed") == false, "source build inherited OTA")
	api.state(t, "parked")
	api.post(t, "/api/updates", map[string]string{"action": "check"}, 409)
	var info []containerInfo
	require(t, json.Unmarshal(output(t, tools, "docker", "inspect", container), &info))
	checkContainer(t, info[0])
	events := string(output(t, tools, "docker", "exec", container, "cat", "/sys/fs/cgroup/memory.events"))
	assertThat(t, strings.Contains(events, "oom_kill 0"), "OOM event detected")
}
