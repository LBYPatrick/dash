package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHEVCInventoryMatchesEncodedFrameCount(t *testing.T) {
	for _, bframes := range []bool{false, true} {
		data := cameraFixture(t, "hevc", bframes)
		duration, err := countHEVCPictures(context.Background(), bytes.NewReader(data))
		if err != nil || duration != .5 {
			t.Fatal("encoded frame inventory differs", duration, err)
		}
		path := filepath.Join(t.TempDir(), "camera.hevc")
		os.WriteFile(path, data, 0600)
		probe, err := probeMCAPVideo(context.Background(), path)
		if err != nil || len(probe.Offsets) != int(duration*20) {
			t.Fatal("inventory disagrees with FFprobe", duration, len(probe.Offsets), err)
		}
	}
}
func TestHEVCInventoryCountsPicturesNotSlices(t *testing.T) {
	nal := func(kind, first byte) []byte { return []byte{0, 0, 0, 1, kind << 1, 1, first} }
	data := append(append(nal(32, 0), nal(33, 0)...), nal(34, 0)...)
	data = append(data, nal(19, 128)...)
	data = append(data, nal(19, 0)...)
	data = append(data, nal(1, 128)...)
	data = append(data, nal(1, 0)...)
	duration, err := countHEVCPictures(context.Background(), bytes.NewReader(data))
	if err != nil || duration != .1 {
		t.Fatal("multiple slices counted as multiple frames", duration, err)
	}
	for _, bad := range [][]byte{nil, []byte("garbage"), nal(19, 128), {0, 0, 1, 0}, append(append([]byte{}, data...), nal(63, 128)...)} {
		if _, err := countHEVCPictures(context.Background(), bytes.NewReader(bad)); err == nil {
			t.Fatal("unfamiliar video accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := countHEVCPictures(ctx, bytes.NewReader(data)); err == nil {
		t.Fatal("inventory ignored cancellation")
	}
}
