package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, audio bool, bframes bool) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	directory := t.TempDir()
	video := filepath.Join(directory, "camera.hevc")
	bf := "0"
	if bframes {
		bf = "2"
	}
	args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=s=160x96:r=20", "-t", "6", "-c:v", "libx265", "-threads", "1", "-preset", "ultrafast", "-bf", bf, "-x265-params", "pools=none:frame-threads=1:log-level=error:keyint=20:min-keyint=20:scenecut=0:aud=0:slices=1", "-f", "hevc", video}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("HEVC fixture: %v %s", err, out)
	}
	companion := ""
	if audio {
		companion = filepath.Join(directory, "qcamera.ts")
		args = []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=s=160x96:r=20", "-itsoffset", "3", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "6", "-c:v", "libx264", "-threads", "1", "-bf", "0", "-c:a", "aac", "-ac", "1", "-f", "mpegts", companion}
		if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
			t.Fatalf("AAC fixture: %v %s", err, out)
		}
	}
	return video, companion
}
func inventory(t *testing.T, video, companion string) *Index {
	t.Helper()
	file, err := os.Open(video)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	value, err := InventoryHEVC(context.Background(), file, nil)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := file.Stat()
	index := &Index{Version: Version, Video: value, VideoSize: info.Size()}
	if companion != "" {
		source, err := os.Open(companion)
		if err != nil {
			t.Fatal(err)
		}
		defer source.Close()
		audio, err := InventoryAudio(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		if len(audio.Samples) == 0 {
			t.Fatal("AAC not found")
		}
		if float64(audio.Samples[0].Time)/float64(audio.Rate) < 2.9 {
			t.Fatal("late audio shifted to start")
		}
		last := value.Fragments[len(value.Fragments)-1]
		index.Audio = CompleteAudio(audio, last.Start+last.Duration, 0)
		info, _ = source.Stat()
		index.AudioSize = info.Size()
	}
	if err := Validate(index); err != nil {
		t.Fatal(err)
	}
	return index
}
func writeFragments(t *testing.T, index *Index, video, companion string, parts []int) []byte {
	t.Helper()
	source, err := os.Open(video)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	var audio *os.File
	if companion != "" {
		audio, err = os.Open(companion)
		if err != nil {
			t.Fatal(err)
		}
		defer audio.Close()
	}
	init, err := Init(index)
	if err != nil {
		t.Fatal(err)
	}
	var result bytes.Buffer
	result.Write(init)
	for _, n := range parts {
		reader, err := NewFragment(context.Background(), index, n, source, audio)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(&result, reader); err != nil {
			t.Fatal(err)
		}
	}
	return result.Bytes()
}
func TestNativeFragmentsPreservePicturesAndLateAudio(t *testing.T) {
	video, companion := fixture(t, true, false)
	if out, err := exec.Command("ffmpeg", "-v", "error", "-framerate", "20", "-i", video, "-f", "null", "-").CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("original decode: %v %s", err, out)
	}
	index := inventory(t, video, companion)
	if len(index.Video.Samples) != 120 || len(index.Video.Fragments) != 3 {
		t.Fatal("wrong native inventory", len(index.Video.Samples), len(index.Video.Fragments))
	}
	for _, part := range index.Video.Fragments {
		if part.Duration != 2*Timescale {
			t.Fatal("wrong fragment duration")
		}
	}
	native := filepath.Join(t.TempDir(), "native.mp4")
	if err := os.WriteFile(native, writeFragments(t, index, video, companion, []int{0, 1, 2}), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("ffmpeg", "-v", "error", "-i", native, "-f", "null", "-").CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("native video/AAC decode: %v %s", err, out)
	}
	hashes := func(file string, raw bool) []string {
		t.Helper()
		args := []string{"-v", "error"}
		if raw {
			args = append(args, "-framerate", "20")
		}
		args = append(args, "-i", file, "-map", "0:v:0", "-f", "framemd5", "-")
		out, err := exec.Command("ffmpeg", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("decode hashes: %v %s", err, out)
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
	original, decoded := hashes(video, true), hashes(native, false)
	if fmt.Sprint(original) != fmt.Sprint(decoded) || len(decoded) != 120 {
		t.Fatal("repackaging changed decoded pictures", len(original), len(decoded))
	}
	// Each fragment begins at an independent source keyframe; seeking never
	// requires fetching/decompressing the raw prefix of a recording.
	for _, number := range []int{2, 0, 1} {
		path := filepath.Join(t.TempDir(), "seek.mp4")
		os.WriteFile(path, writeFragments(t, index, video, companion, []int{number}), 0600)
		if out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-map", "0:v:0", "-f", "null", "-").CombinedOutput(); err != nil || len(out) > 0 {
			t.Fatalf("fragment %d: %v %s", number, err, out)
		}
	}
	encoded, err := EncodeIndex(index)
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := DecodeIndex(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(roundtrip.Video.Samples) != 120 {
		t.Fatal("index lost samples")
	}
	encoded[len(encoded)/2] ^= 0x80
	if _, err := DecodeIndex(encoded); err == nil {
		t.Fatal("corrupt index accepted")
	}
}
func TestNativeRejectsReorderedPicturesAndHonorsCancellation(t *testing.T) {
	video, _ := fixture(t, false, true)
	data, err := os.ReadFile(video)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InventoryHEVC(context.Background(), bytes.NewReader(data), nil); !errors.Is(err, ErrUnsupported) {
		t.Fatal("reordered stream accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InventoryHEVC(ctx, bytes.NewReader(data), nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	for _, bad := range [][]byte{{}, {0, 0, 1, 0x26, 1, 0x80}, {0, 0, 1, 0x40, 0, 0}} {
		if _, err := InventoryHEVC(context.Background(), bytes.NewReader(bad), nil); err == nil {
			t.Fatal("malformed stream accepted")
		}
	}
}
func TestVirtualFragmentRangeAndSourceValidation(t *testing.T) {
	video, _ := fixture(t, false, false)
	index := inventory(t, video, "")
	file, _ := os.Open(video)
	defer file.Close()
	reader, err := NewFragment(context.Background(), index, 0, file, nil)
	if err != nil {
		t.Fatal(err)
	}
	full, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int64{0, 31, 150, int64(len(full) - 11)} {
		if _, err := reader.Seek(offset, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, min(9, int64(len(full))-offset))
		if _, err := io.ReadFull(reader, buffer); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buffer, full[offset:offset+int64(len(buffer))]) {
			t.Fatal("range read changed media")
		}
	}
	index.Video.Samples[0].Spans[0].Offset = index.VideoSize
	if err := Validate(index); err == nil {
		t.Fatal("source range outside recording accepted")
	}
}

func TestSessionSilenceKeepsAudioTrackWithoutReadingCompanion(t *testing.T) {
	video, _ := fixture(t, false, false)
	index := inventory(t, video, "")
	index.Audio = Audio{Silence: true, Object: 2, Channels: 1, Rate: 16000}
	path := filepath.Join(t.TempDir(), "silent.mp4")
	if err := os.WriteFile(path, writeFragments(t, index, video, "", []int{0, 1, 2}), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-map", "0:a:0", "-f", "s16le", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 16000*2*5 {
		t.Fatal("silent session track was truncated")
	}
	for _, value := range out {
		if value != 0 {
			t.Fatal("clock-gap padding is audible")
		}
	}
}

func TestMissingAndOutOfRangeAudioStaysAbsent(t *testing.T) {
	if result := CompleteAudio(Audio{}, Timescale, 0); len(result.Samples) != 0 || result.Rate != 0 {
		t.Fatal("created audio for an empty microphone")
	}
	result := CompleteAudio(Audio{Rate: 48000, Object: 2, Channels: 1, Samples: []Sample{{Time: 480000, Duration: 1024}}}, Timescale, 0)
	if result.Rate != 0 || len(result.Samples) != 0 {
		t.Fatal("sound outside this camera's timeline became recorded audio")
	}
}
