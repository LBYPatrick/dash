package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This opt-in test reuses a previously downloaded session archive on the host.
// It never reaches the device or changes its driving state or original files.
func TestPhysicalRecordingNativePackaging(t *testing.T) {
	archive := os.Getenv("DASH_NATIVE_RECORDING_ZIP")
	if archive == "" {
		t.Skip("set DASH_NATIVE_RECORDING_ZIP to a private local session ZIP")
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	index, parked := fixtureIndex(t)
	folder := filepath.Join(index.root, fixtureDrive+"--0")
	for _, entry := range reader.File {
		if !strings.HasPrefix(entry.Name, "0/") || strings.Count(entry.Name, "/") != 1 {
			continue
		}
		name := filepath.Base(entry.Name)
		if !cameras[name] && !strings.HasPrefix(name, "qlog") && !strings.HasPrefix(name, "rlog") {
			continue
		}
		if entry.UncompressedSize64 > 256<<20 {
			t.Fatal("fixture source exceeds budget")
		}
		source, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		target, err := os.OpenFile(filepath.Join(folder, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			source.Close()
			t.Fatal(err)
		}
		_, copyErr := io.Copy(target, io.LimitReader(source, 256<<20))
		source.Close()
		target.Close()
		if copyErr != nil {
			t.Fatal(copyErr)
		}
	}
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := index.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	parked.Store(false)
	server := &Server{index: index, ctx: context.Background()}
	for _, file := range snapshot.Segments[fixtureDrive+"--0"].Files {
		if !strings.HasSuffix(file.Name, ".hevc") {
			continue
		}
		if file.Native == nil || file.Native.Status != "ready" {
			t.Fatalf("real %s native preparation unavailable: %+v", file.Name, file.Native)
		}
		output := filepath.Join(t.TempDir(), "native.mp4")
		data := nativeRecordingBytes(t, server, fixtureDrive+"--0", file.Name)
		if err := os.WriteFile(output, data, 0600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("ffmpeg", "-v", "error", "-threads", "2", "-i", output, "-f", "null", "-").CombinedOutput(); err != nil || len(out) > 0 {
			t.Fatalf("real %s decode: %v %s", file.Name, err, out)
		}
		hashes := func(path string, raw bool) []string {
			t.Helper()
			args := []string{"-v", "error", "-threads", "2"}
			if raw {
				args = append(args, "-framerate", "20")
			}
			args = append(args, "-i", path, "-map", "0:v:0", "-fps_mode", "passthrough", "-f", "framemd5", "-")
			out, err := exec.Command("ffmpeg", args...).CombinedOutput()
			if err != nil {
				t.Fatalf("real frame hashes: %v %s", err, out)
			}
			var result []string
			for _, line := range strings.Split(string(out), "\n") {
				if !strings.HasPrefix(line, "#") && strings.Contains(line, ",") {
					fields := strings.Split(line, ",")
					result = append(result, strings.TrimSpace(fields[len(fields)-1]))
				}
			}
			return result
		}
		sourceHashes := hashes(filepath.Join(folder, file.Name), true)
		nativeHashes := hashes(output, false)
		if !bytes.Equal([]byte(fmt.Sprint(sourceHashes)), []byte(fmt.Sprint(nativeHashes))) {
			t.Fatalf("real %s pictures changed: %d vs %d", file.Name, len(sourceHashes), len(nativeHashes))
		}
		t.Logf("%s: codec=%s frames=%d fragments=%d originalBytes=%d virtualBytes=%d audio=%t", file.Name, file.Native.Codec, len(sourceHashes), len(file.Native.Fragments), file.Size, len(data), file.Native.Audio)
	}
}
