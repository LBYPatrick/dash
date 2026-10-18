package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dash.local/backend/internal/playback"
)

func physicalClient(t *testing.T, timeout time.Duration) *http.Client {
	t.Helper()
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if path := os.Getenv("DASH_DEVICE_CA"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(data) {
			t.Fatal("invalid device CA")
		}
		transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
		t.Cleanup(transport.CloseIdleConnections)
		client.Transport = transport
	}
	return client
}

// Explicit opt-in: read already-indexed footage from one short real session.
// No preparation request, state change, export job or firmware operation occurs.
func TestPhysicalDeviceNativePlayback(t *testing.T) {
	if os.Getenv("DASH_DEVICE_ACCEPTANCE") != "1" {
		t.Skip("opt-in physical device acceptance")
	}
	origin, drive, folder := os.Getenv("DASH_TEST_URL"), os.Getenv("DASH_DEVICE_DRIVE"), os.Getenv("DASH_DEVICE_OUTPUT")
	if !strings.HasPrefix(origin, "https://") || !segmentPattern.MatchString(drive+"--0") || !filepath.IsAbs(folder) {
		t.Fatal("explicit HTTPS URL, drive and private absolute output directory required")
	}
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	client := physicalClient(t, 45*time.Second)
	get := func(path string, limit int64) []byte {
		t.Helper()
		response, err := client.Get(origin + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("native HTTP %d", response.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if err != nil || int64(len(data)) > limit {
			t.Fatal("bounded native response", err)
		}
		return data
	}
	var info struct {
		Driving    string
		Simulation bool
	}
	if err := json.Unmarshal(get("/api/info", 64<<10), &info); err != nil || info.Simulation || info.Driving != "parked" {
		t.Fatal("real device must be parked", err)
	}
	var before IndexSnapshot
	if err := json.Unmarshal(get("/api/index", 32<<20), &before); err != nil {
		t.Fatal(err)
	}
	entry, ok := before.Segments[drive+"--0"]
	if !ok {
		t.Fatal("session not indexed")
	}
	beforeEntry, _ := json.Marshal(entry)
	for _, camera := range []string{"fcamera.hevc", "ecamera.hevc", "dcamera.hevc"} {
		var media *NativeMedia
		for _, file := range entry.Files {
			if file.Name == camera {
				media = file.Native
			}
		}
		if media == nil || media.Status != "ready" {
			t.Fatalf("%s must finish parked indexing before this read-only check", camera)
		}
		playlist := string(get("/api/playlist?"+url.Values{"drive": {drive}, "camera": {camera}}.Encode(), 1<<20))
		if !strings.Contains(playlist, "#EXT-X-VERSION:7") || strings.Contains(playlist, "codec=h264") {
			t.Fatal("native playlist fell back to transcoding")
		}
		filename := filepath.Join(folder, camera+".mp4")
		output, err := os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		total := int64(0)
		parts := 0
		firstFragment := ""
		var firstPrefix []byte
		for _, line := range strings.Split(playlist, "\n") {
			path := ""
			if strings.HasPrefix(line, "#EXT-X-MAP:URI=\"") {
				path = strings.TrimSuffix(strings.TrimPrefix(line, "#EXT-X-MAP:URI=\""), "\"")
			} else if strings.HasPrefix(line, "/api/media/fragment?") {
				path = line
				parts++
			}
			if path == "" {
				continue
			}
			parsed, err := url.Parse(path)
			if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/api/media/") {
				output.Close()
				t.Fatal("foreign media URL")
			}
			data := get(path, playback.MaxFragmentBytes+(64<<10))
			total += int64(len(data))
			if total > 256<<20 {
				output.Close()
				t.Fatal("choose a shorter session for device acceptance")
			}
			if _, err := output.Write(data); err != nil {
				output.Close()
				t.Fatal(err)
			}
			if firstFragment == "" && strings.HasPrefix(path, "/api/media/fragment?") {
				firstFragment = path
				firstPrefix = append([]byte{}, data[:min(64, len(data))]...)
			}
		}
		if err := output.Close(); err != nil {
			t.Fatal(err)
		}
		request, _ := http.NewRequest("GET", origin+firstFragment, nil)
		request.Header.Set("Range", "bytes=0-63")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		prefix, err := io.ReadAll(io.LimitReader(response.Body, 65))
		response.Body.Close()
		if err != nil || response.StatusCode != 206 || !bytes.Equal(prefix, firstPrefix) {
			t.Fatal("device byte-range mismatch", err, response.StatusCode)
		}
		if out, err := exec.Command("ffmpeg", "-v", "error", "-threads", "2", "-i", filename, "-f", "null", "-").CombinedOutput(); err != nil || len(out) > 0 {
			t.Fatalf("native %s decode: %v %s", camera, err, out)
		}
		t.Logf("%s: %s, %d fragments, %d bytes, recordedAudio=%t", camera, media.Codec, parts, total, media.Audio)
	}
	var after IndexSnapshot
	if err := json.Unmarshal(get("/api/index", 32<<20), &after); err != nil {
		t.Fatal(err)
	}
	afterEntry, _ := json.Marshal(after.Segments[drive+"--0"])
	if !bytes.Equal(beforeEntry, afterEntry) || len(before.Segments) != len(after.Segments) {
		t.Fatal("cached native playback changed recording inventory")
	}
}
