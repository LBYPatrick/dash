package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func releaseFixture(channel string, sequence uint64) Build {
	suffix := "-" + channel
	if channel == "main" {
		suffix = "+main"
	}
	version := "0.1.0" + suffix + "." + fmt.Sprint(sequence) + ".aaaaaaaa"
	return Build{1, version, channel, strings.Repeat("a", 40), sequence, "v" + version, runtime.GOARCH}
}
func fakeELF(arch string) []byte {
	data := make([]byte, 64)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(data[16:], 2)
	machine := uint16(183)
	if arch == "amd64" {
		machine = 62
	}
	binary.LittleEndian.PutUint16(data[18:], machine)
	binary.LittleEndian.PutUint32(data[20:], 1)
	binary.LittleEndian.PutUint16(data[52:], 64)
	return data
}

type archiveEntry struct {
	name string
	kind byte
	data []byte
}

func archiveFixture(t *testing.T, build Build, extra ...archiveEntry) []byte {
	t.Helper()
	metadata, _ := json.Marshal(build)
	entries := []archiveEntry{{"dash-backend", tar.TypeReg, fakeELF(build.Arch)}, {"release.json", tar.TypeReg, metadata}, {"dist/index.html", tar.TypeReg, []byte("<html>CI frontend</html>")}}
	entries = append(entries, extra...)
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(zipped)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0600, Typeflag: entry.kind, Size: int64(len(entry.data))}
		if entry.kind == tar.TypeSymlink {
			header.Linkname = "/etc/passwd"
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := writer.Write(entry.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func testManifest(build Build, archive []byte) ReleaseManifest {
	digest := sha256.Sum256(archive)
	manifest := ReleaseManifest{Build: build, Assets: map[string]ReleaseAsset{build.Arch: {"dash-" + strings.ReplaceAll(build.Tag, "+", "-") + "-linux-" + build.Arch + ".tar.gz", hex.EncodeToString(digest[:]), int64(len(archive))}}}
	manifest.Arch = ""
	return manifest
}
func writeArchive(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.tar.gz")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestReleaseExtractionAndIdentity(t *testing.T) {
	build := releaseFixture("main", 12)
	if err := unpackRelease(context.Background(), writeArchive(t, archiveFixture(t, build)), t.TempDir(), build); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []archiveEntry{{"../escape", tar.TypeReg, []byte("bad")}, {"/absolute", tar.TypeReg, nil}, {"dist/../escape", tar.TypeReg, nil}, {"dist/link", tar.TypeSymlink, nil}, {"unexpected", tar.TypeReg, nil}, {"release.json", tar.TypeReg, []byte("duplicate")}} {
		t.Run(entry.name, func(t *testing.T) {
			if err := unpackRelease(context.Background(), writeArchive(t, archiveFixture(t, build, entry)), t.TempDir(), build); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	wrong := build
	wrong.Revision = strings.Repeat("b", 40)
	if err := unpackRelease(context.Background(), writeArchive(t, archiveFixture(t, wrong)), t.TempDir(), build); err == nil {
		t.Fatal("wrong build accepted")
	}
	corrupt := archiveFixture(t, build)
	corrupt[len(corrupt)-1] ^= 1
	if err := unpackRelease(context.Background(), writeArchive(t, corrupt), t.TempDir(), build); err == nil {
		t.Fatal("gzip corruption accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := unpackRelease(canceled, writeArchive(t, archiveFixture(t, build)), t.TempDir(), build); err == nil {
		t.Fatal("canceled extraction continued")
	}
}
func TestReleaseTrustAndChannelIsolation(t *testing.T) {
	build := releaseFixture("dev", 12)
	manifest := testManifest(build, []byte("archive"))
	if _, err := manifest.asset("dev", runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.asset("main", runtime.GOARCH); err == nil {
		t.Fatal("cross-channel feed accepted")
	}
	manifest.Assets[runtime.GOARCH] = ReleaseAsset{"../../bad", strings.Repeat("a", 64), 10}
	if _, err := manifest.asset("dev", runtime.GOARCH); err == nil {
		t.Fatal("unsafe asset accepted")
	}
	for _, address := range []string{"http://github.com/a/b", "https://github.com.evil.invalid/", "https://user@github.com/a", "https://github.com:123/a", "file:///tmp/a", "https://127.0.0.1/a"} {
		parsed, _ := url.Parse(address)
		if trustedReleaseURL(parsed) {
			t.Fatal("untrusted release URL accepted", address)
		}
	}
	for _, host := range []string{"github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"} {
		parsed, _ := url.Parse("https://" + host + "/a")
		if !trustedReleaseURL(parsed) {
			t.Fatal(host)
		}
	}
}

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
func response(body []byte) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Header: http.Header{}}
}
func TestReleaseDownloadHashAndBounds(t *testing.T) {
	build := releaseFixture("main", 13)
	archive := archiveFixture(t, build)
	manifest := testManifest(build, archive)
	client := &http.Client{Transport: releaseTransport(func(request *http.Request) (*http.Response, error) { return response(archive), nil })}
	if err := downloadRelease(context.Background(), client, "LBYPatrick/dash", manifest, runtime.GOARCH, filepath.Join(t.TempDir(), "download"), nil); err != nil {
		t.Fatal(err)
	}
	asset := manifest.Assets[runtime.GOARCH]
	asset.SHA256 = strings.Repeat("0", 64)
	manifest.Assets[runtime.GOARCH] = asset
	if err := downloadRelease(context.Background(), client, "LBYPatrick/dash", manifest, runtime.GOARCH, filepath.Join(t.TempDir(), "download"), nil); err == nil {
		t.Fatal("bad digest accepted")
	}
	client.Transport = releaseTransport(func(*http.Request) (*http.Response, error) {
		return response(bytes.Repeat([]byte("x"), (64<<10)+1)), nil
	})
	if _, err := readManifest(context.Background(), client, "LBYPatrick/dash", "main", ""); err == nil {
		t.Fatal("unbounded manifest")
	}
}
func waitUpdate(t *testing.T, manager *UpdateManager, phase string) UpdateState {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		state := manager.snapshot()
		manager.mu.Lock()
		busy := manager.busy
		manager.mu.Unlock()
		if state.Phase == phase && !busy {
			return state
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("update timeout", manager.snapshot())
	return UpdateState{}
}
func TestUpdaterStagesPersistsAndRequestsRestart(t *testing.T) {
	build := releaseFixture("main", 14)
	archive := archiveFixture(t, build)
	manifest := testManifest(build, archive)
	data, _ := json.Marshal(manifest)
	var driving atomic.Value
	driving.Store("unknown")
	var restarted atomic.Bool
	manager, err := newUpdaterForBuild(context.Background(), t.TempDir(), func() string { return driving.Load().(string) }, func() { restarted.Store(true) }, releaseFixture("main", 1))
	if err != nil {
		t.Fatal(err)
	}
	manager.client = &http.Client{Transport: releaseTransport(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "manifest.json") {
			return response(data), nil
		}
		return response(archive), nil
	})}
	for _, state := range []string{"unknown", "onroad"} {
		driving.Store(state)
		if err := manager.begin("check"); err == nil {
			t.Fatal("update check started without parked state")
		}
		if err := manager.configure("dev", true); err == nil {
			t.Fatal("preferences changed onroad")
		}
		if err := manager.install(); err == nil {
			t.Fatal("install accepted onroad")
		}
	}
	// A previous incomplete/corrupt candidate with valid metadata must be
	// replaced by the freshly downloaded, digest-verified payload.
	candidate := filepath.Join(manager.home, "releases", build.Tag)
	if err := os.MkdirAll(filepath.Join(candidate, "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(candidate, "release.json"), build); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate, "dash-backend"), []byte("corrupt old binary"), 0700); err != nil {
		t.Fatal(err)
	}
	driving.Store("parked")
	if err := manager.begin("check"); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, manager, "available")
	if err := manager.begin("download"); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, manager, "ready")
	actual, err := os.ReadFile(filepath.Join(candidate, "dash-backend"))
	if err != nil || !bytes.Equal(actual, fakeELF(build.Arch)) {
		t.Fatal("old unverified bytes reused", err)
	}
	restored, err := newUpdaterForBuild(context.Background(), manager.home, manager.state, nil, releaseFixture("main", 1))
	if err != nil || restored.snapshot().Phase != "ready" {
		t.Fatal("download did not survive restart", err)
	}
	if err := manager.install(); err != nil {
		t.Fatal(err)
	}
	tag, err := pendingRelease(manager.home)
	if err != nil || tag != build.Tag {
		t.Fatal("unverified restart request", tag, err)
	}
	time.Sleep(300 * time.Millisecond)
	if !restarted.Load() {
		t.Fatal("restart not requested")
	}
}

type slowReleaseBody struct {
	ctx  context.Context
	data []byte
}

func (b *slowReleaseBody) Close() error { return nil }
func (b *slowReleaseBody) Read(p []byte) (int, error) {
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n := min(8, len(p), len(b.data))
	copy(p, b.data[:n])
	b.data = b.data[n:]
	return n, nil
}
func TestUpdateTransferStopsOnIgnition(t *testing.T) {
	for _, next := range []string{"onroad", "unknown"} {
		t.Run(next, func(t *testing.T) {
			build := releaseFixture("dev", 15)
			archive := archiveFixture(t, build)
			manifest := testManifest(build, archive)
			var driving atomic.Value
			driving.Store("parked")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			manager, _ := newUpdaterForBuild(ctx, t.TempDir(), func() string { return driving.Load().(string) }, nil, releaseFixture("dev", 1))
			manager.config.Channel = "dev"
			manager.status.Channel = "dev"
			manager.status.Phase = "available"
			manager.status.Release = &manifest
			var started atomic.Bool
			manager.client = &http.Client{Transport: releaseTransport(func(request *http.Request) (*http.Response, error) {
				started.Store(true)
				return &http.Response{StatusCode: 200, Body: &slowReleaseBody{request.Context(), archive}, ContentLength: int64(len(archive))}, nil
			})}
			if err := manager.begin("download"); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for !started.Load() && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !started.Load() {
				t.Fatal("transfer did not start")
			}
			driving.Store(next)
			waitUpdate(t, manager, "error")
			if _, err := os.Stat(filepath.Join(manager.home, "ready.json")); !os.IsNotExist(err) {
				t.Fatal("canceled download became installable")
			}
			// Cancellation never recursively cleans files while onroad/unknown.
			stages, _ := filepath.Glob(filepath.Join(manager.home, "releases", ".staging-*"))
			if len(stages) != 1 {
				t.Fatal("staging cleanup ran after ignition")
			}
		})
	}
}
func TestActivationSurvivesPowerLossAndRollsBack(t *testing.T) {
	home := t.TempDir()
	old := releaseFixture("main", 1)
	candidate := releaseFixture("main", 2)
	for _, build := range []Build{old, candidate} {
		folder := filepath.Join(home, "releases", build.Tag)
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		if err := atomicJSON(filepath.Join(folder, "release.json"), build); err != nil {
			t.Fatal(err)
		}
	}
	if err := switchRelease(home, "current", old.Tag); err != nil {
		t.Fatal(err)
	}
	if err := beginActivation(home, candidate.Tag); err != nil {
		t.Fatal(err)
	}
	if got, _ := releaseTarget(home, "current"); got != candidate.Tag {
		t.Fatal("activation not atomic")
	}
	if err := recoverActivation(home); err != nil {
		t.Fatal(err)
	}
	if got, _ := releaseTarget(home, "current"); got != old.Tag {
		t.Fatal("power-loss rollback failed")
	}
	if err := beginActivation(home, candidate.Tag); err != nil {
		t.Fatal(err)
	}
	if err := commitActivation(home); err != nil {
		t.Fatal(err)
	}
	if got, _ := releaseTarget(home, "previous"); got != old.Tag {
		t.Fatal("previous release not retained")
	}
	if err := recoverActivation(home); err != nil {
		t.Fatal(err)
	}
	if got, _ := releaseTarget(home, "current"); got != candidate.Tag {
		t.Fatal("committed release rolled back")
	}
	if err := switchRelease(home, "current", "../escape"); err == nil {
		t.Fatal("unsafe activation accepted")
	}
}

func TestSourceBuildNeverEnablesOTA(t *testing.T) {
	build := releaseFixture("local", 123)
	if err := build.validate("local", runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	manager, err := newUpdaterForBuild(context.Background(), t.TempDir(), func() string { return "parked" }, nil, build)
	if err != nil || manager.snapshot().Managed {
		t.Fatal("local source build enabled OTA", err)
	}
	manifest := testManifest(build, archiveFixture(t, build))
	if _, err := manifest.asset("local", runtime.GOARCH); err == nil {
		t.Fatal("local build accepted as channel feed")
	}
}
