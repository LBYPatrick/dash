package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func thumbnailTestFrame(base, contrast uint8) *image.Gray {
	frame := image.NewGray(image.Rect(0, 0, 96, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 96; x++ {
			value := base
			if (x/12+y/8)%2 == 0 {
				value += contrast
			}
			frame.SetGray(x, y, color.Gray{Y: value})
		}
	}
	return frame
}

func TestThumbnailExposureSelection(t *testing.T) {
	black := measureThumbnail(thumbnailTestFrame(0, 0))
	settled := measureThumbnail(thumbnailTestFrame(85, 70))
	white := measureThumbnail(thumbnailTestFrame(255, 0))
	night := measureThumbnail(thumbnailTestFrame(5, 12))
	if black.usable() || white.usable() || night.usable() || !settled.usable() {
		t.Fatal("exposure classification lost black, clipped or settled frames")
	}
	for _, test := range []struct {
		name   string
		frames []thumbnailExposure
		want   int
	}{
		{"AE recovery", []thumbnailExposure{black, black, settled, settled}, 2},
		{"bright flash", []thumbnailExposure{black, white, settled, settled}, 2},
		{"night flash", []thumbnailExposure{black, white, night, night}, 2},
		{"single dark frame", []thumbnailExposure{night}, 0},
		{"already exposed", []thumbnailExposure{settled, settled, settled}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if selected := selectThumbnail(test.frames); selected != test.want {
				t.Fatalf("selected frame %d, wanted %d", selected, test.want)
			}
		})
	}
	var bounded thumbnailBuffer
	if _, err := bounded.Write(make([]byte, ThumbnailOutputLimit+1)); err == nil || bounded.Len() != 0 {
		t.Fatal("thumbnail output exceeded its hard byte budget")
	}
	bounded = thumbnailBuffer{}
	if _, err := io.Copy(&bounded, io.LimitReader(bytes.NewReader(make([]byte, ThumbnailOutputLimit+1)), ThumbnailOutputLimit+1)); err == nil || bounded.Len() > ThumbnailOutputLimit {
		t.Fatal("io.Copy bypassed the thumbnail output budget")
	}
}

func TestThumbnailJPEGFraming(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, thumbnailTestFrame(85, 70), nil); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	// Metadata can itself contain EOI bytes, so a naive byte split is unsafe.
	metadata := []byte{0xff, 0xe1, 0, 6, 0xff, 0xd9, 0, 0}
	withMetadata := append(append(append([]byte{}, data[:2]...), metadata...), data[2:]...)
	stream := append(append([]byte{}, withMetadata...), data...)
	if size, err := thumbnailJPEGSize(stream); err != nil || size != len(withMetadata) {
		t.Fatal("JPEG metadata or the next frame changed framing", size, err)
	}
	for _, broken := range [][]byte{nil, {0xff, 0xd8}, data[:len(data)-2], {0xff, 0xd8, 0xff, 0xe1, 0xff, 0xff}} {
		if _, err := thumbnailJPEGSize(broken); err == nil {
			t.Fatal("accepted an incomplete thumbnail frame")
		}
	}
}

// Encode a tiny synthetic exposure ramp; no captured footage or timing changes.
func thumbnailTestVideo(t *testing.T, folder string, count int, frameAt func(int) *image.Gray) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	var raw bytes.Buffer
	for n := 0; n < count; n++ {
		frame := frameAt(n)
		for _, value := range frame.Pix {
			raw.Write([]byte{value, value, value})
		}
	}
	filename := filepath.Join(folder, "qcamera.ts")
	command := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "rawvideo", "-pix_fmt", "rgb24", "-s", "96x64", "-r", "20", "-i", "pipe:0", "-an", "-c:v", "libx264", "-threads", "1", "-filter_threads", "1", "-preset", "ultrafast", "-g", "20", "-bf", "0", "-pix_fmt", "yuv420p", "-f", "mpegts", filename)
	command.Stdin = &raw
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("exposure fixture: %v %s", err, output)
	}
	return filename
}

func TestThumbnailSamplingAfterAEAndShortClips(t *testing.T) {
	black, settled, night := thumbnailTestFrame(0, 0), thumbnailTestFrame(85, 70), thumbnailTestFrame(5, 12)
	for _, test := range []struct {
		name  string
		count int
		frame func(int) *image.Gray
		dark  bool
	}{
		{"AE settles after two seconds", 140, func(n int) *image.Gray {
			if n < 40 {
				return black
			}
			return settled
		}, false},
		{"single recorded frame", 1, func(int) *image.Gray { return settled }, false},
		{"night remains naturally dark", 20, func(int) *image.Gray { return night }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			camera := thumbnailTestVideo(t, t.TempDir(), test.count, test.frame)
			target := filepath.Join(t.TempDir(), "thumbnail.jpg")
			if err := writeIndexThumbnail(context.Background(), camera, target); err != nil {
				t.Fatal(err)
			}
			if usable := existingThumbnailUsable(target); usable == test.dark {
				t.Fatalf("wrong exposure after sampling: usable=%t", usable)
			}
			if !test.dark {
				return
			}
			file, _ := os.Open(target)
			defer file.Close()
			frame, err := jpeg.Decode(file)
			if err != nil || measureThumbnail(frame).upper > 35 {
				t.Fatal("night frame was artificially brightened", err)
			}
		})
	}
}

func TestThumbnailMigrationPreservesNativeIndexesAndTelemetry(t *testing.T) {
	index, parked := fixtureIndex(t)
	folder := filepath.Join(index.root, fixtureDrive+"--0")
	thumbnailTestVideo(t, folder, 60, func(n int) *image.Gray {
		if n < 20 {
			return thumbnailTestFrame(0, 0)
		}
		return thumbnailTestFrame(85, 70)
	})
	if err := os.WriteFile(filepath.Join(folder, "fcamera.hevc"), cameraFixture(t, "hevc", false), 0600); err != nil {
		t.Fatal(err)
	}
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	details, _ := index.driveMetadata(fixtureDrive)
	entry := details[fixtureDrive+"--0"]
	metadata, _ := json.Marshal(entry.Metadata)
	var nativePath string
	for _, file := range entry.Files {
		if file.Native != nil && file.Native.Status == "ready" {
			nativePath = index.nativePath(file.Native.Key)
		}
	}
	if nativePath == "" {
		t.Fatal("native fixture unavailable")
	}
	originalTime := time.Unix(1700000000, 0)
	if err := os.Chtimes(nativePath, originalTime, originalTime); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(index.cache, "legacy.jpg")
	file, err := os.Create(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(file, thumbnailTestFrame(0, 0), nil); err != nil {
		t.Fatal(err)
	}
	file.Close()
	entry.ThumbnailVersion, entry.Thumbnail = 0, "/api/thumbnail/legacy.jpg"
	raw, _ := json.Marshal(entry)
	if _, err := index.db.Exec("UPDATE segments SET entry=?,summary=?", string(raw), string(raw)); err != nil {
		t.Fatal(err)
	}
	parked.Store(false)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	details, _ = index.driveMetadata(fixtureDrive)
	if details[fixtureDrive+"--0"].ThumbnailVersion != 0 {
		t.Fatal("thumbnail migration ran while onroad")
	}
	parked.Store(true)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	details, _ = index.driveMetadata(fixtureDrive)
	updated := details[fixtureDrive+"--0"]
	actualMetadata, _ := json.Marshal(updated.Metadata)
	nativeInfo, err := os.Stat(nativePath)
	if err != nil || !nativeInfo.ModTime().Equal(originalTime) || !bytes.Equal(metadata, actualMetadata) {
		t.Fatal("thumbnail migration rebuilt native video or telemetry", err)
	}
	if updated.ThumbnailVersion != ThumbnailVersion || updated.Thumbnail == entry.Thumbnail || !existingThumbnailUsable(filepath.Join(index.cache, filepath.Base(updated.Thumbnail))) {
		t.Fatal("dark legacy thumbnail was not replaced")
	}
	// A second scan must retain a good thumbnail and do no media work.
	current := filepath.Join(index.cache, filepath.Base(updated.Thumbnail))
	info, _ := os.Stat(current)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	again, _ := os.Stat(current)
	if !again.ModTime().Equal(info.ModTime()) {
		t.Fatal("incremental scan generated another thumbnail")
	}
	// A good legacy image gets its version updated without launching media tools.
	updated.ThumbnailVersion = 0
	raw, _ = json.Marshal(updated)
	if _, err := index.db.Exec("UPDATE segments SET entry=?,summary=?", string(raw), string(raw)); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	marker := filepath.Join(bin, "called")
	t.Setenv("DASH_THUMBNAIL_TEST_MARKER", marker)
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\nprintf called > \"$DASH_THUMBNAIL_TEST_MARKER\"\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("good cached thumbnail launched a media process")
	}
}
