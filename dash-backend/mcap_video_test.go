package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	foxglove "dash.local/backend/internal/mcaptypes/foxglove"
	"github.com/foxglove/mcap/go/mcap"
	"google.golang.org/protobuf/proto"
)

func cameraFixture(t *testing.T, codec string, bframes bool) []byte {
	t.Helper()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip(name + " unavailable")
		}
	}
	extension, encoder, format := ".ts", "libx264", "mpegts"
	if codec == "hevc" {
		extension, encoder, format = ".hevc", "libx265", "hevc"
	}
	filename := filepath.Join(t.TempDir(), "camera"+extension)
	args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=s=160x96:r=20", "-t", "0.5", "-c:v", encoder, "-threads", "1", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-bf", "0"}
	if bframes {
		args[len(args)-1] = "2"
	}
	if codec == "hevc" {
		args = append(args, "-x265-params", "pools=none:frame-threads=1:log-level=error")
	}
	args = append(args, "-f", format, filename)
	if output, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMCAPVideoAllCamerasAndSegments(t *testing.T) {
	index, _ := fixtureIndex(t)
	// Both inputs include B frames, so the exported messages must be converted
	// to a no-lookahead stream, not merely relabeled as Foxglove messages.
	preview := cameraFixture(t, "h264", true)
	road := cameraFixture(t, "hevc", true)
	for _, segment := range []string{"0", "1"} {
		folder := filepath.Join(index.root, fixtureDrive+"--"+segment)
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{"qcamera.ts": preview, "fcamera.hevc": road, "ecamera.hevc": road, "dcamera.hevc": road} {
			if err := os.WriteFile(filepath.Join(folder, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Copy dates must not introduce days into a two-part capture timeline.
	stamp := time.Now().Add(72 * time.Hour)
	if err := os.Chtimes(filepath.Join(index.root, fixtureDrive+"--1", "dcamera.hevc"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	// Avoid thumbnail work; missing indexed GPS intentionally exercises estimates.
	manager, _ := newExports(context.Background(), index)
	job, err := manager.submit(ExportRequest{Drive: fixtureDrive, Format: "mcap"})
	if err != nil {
		t.Fatal(err)
	}
	job = waitExport(t, manager, job.ID)
	file, err := os.Open(job.output)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := mcap.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	info, err := reader.Info()
	if err != nil {
		t.Fatal(err)
	}
	if span := time.Duration(info.Statistics.MessageEndTime - info.Statistics.MessageStartTime); span > 61*time.Second {
		t.Fatalf("file modification dates stretched unindexed recording: %s", span)
	}
	if len(info.AttachmentIndexes) != 9 {
		t.Fatal("original camera/log attachments missing")
	}
	messages, err := reader.Messages()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	lastTime := map[string]uint64{}
	streams := map[string][]byte{}
	for {
		schema, channel, message, err := messages.Next(nil)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if schema.Name != "foxglove.CompressedVideo" {
			continue
		}
		if schema.Encoding != "protobuf" || channel.MessageEncoding != "protobuf" {
			t.Fatal("wrong encoding")
		}
		var value foxglove.CompressedVideo
		if err := proto.Unmarshal(message.Data, &value); err != nil {
			t.Fatal(err)
		}
		if uint64(value.Timestamp.Seconds)*1e9+uint64(value.Timestamp.Nanos) != message.LogTime || message.PublishTime != message.LogTime || value.Timestamp.Nanos >= 1e9 {
			t.Fatal("invalid timestamp")
		}
		if value.Format != "h264" || !strings.HasPrefix(value.FrameId, "dash_") || len(value.Data) == 0 {
			t.Fatal("invalid video frame")
		}
		if counts[channel.Topic] > 0 && message.LogTime <= lastTime[channel.Topic] {
			t.Fatal("camera parts overlap or reorder frames")
		}
		lastTime[channel.Topic] = message.LogTime
		counts[channel.Topic]++
		streams[channel.Topic] = append(streams[channel.Topic], value.Data...)
	}
	for _, topic := range []string{"/dash/camera/preview/video", "/dash/camera/road/video", "/dash/camera/wide_road/video", "/dash/camera/driver/video"} {
		if counts[topic] != 20 {
			t.Fatalf("%s: got %d frames, expected both complete parts", topic, counts[topic])
		}
		assertDecodableVideo(t, streams[topic], "h264", 20)
	}
}

func assertDecodableVideo(t *testing.T, data []byte, codec string, frames int) {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "frames."+codec)
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("ffmpeg", "-v", "error", "-threads", "1", "-i", filename, "-f", "null", "-").CombinedOutput()
	if err != nil || len(output) > 0 {
		t.Fatalf("frames do not decode: %v %s", err, output)
	}
	output, err = exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-count_packets", "-show_entries", "stream=nb_read_packets,has_b_frames", "-of", "json", filename).Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Streams []struct {
			Packets string `json:"nb_read_packets"`
			BFrames int    `json:"has_b_frames"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Streams) != 1 || result.Streams[0].BFrames != 0 || result.Streams[0].Packets != strconv.Itoa(frames) {
		t.Fatal("wrong decoded frame count or B frames", string(output))
	}
}

func TestMCAPVideoCopiesCompatibleStreams(t *testing.T) {
	for _, codec := range []string{"h264", "hevc"} {
		t.Run(codec, func(t *testing.T) {
			extension := ".ts"
			if codec == "hevc" {
				extension = ".hevc"
			}
			data := cameraFixture(t, codec, false)
			filename := filepath.Join(t.TempDir(), "camera"+extension)
			if err := os.WriteFile(filename, data, 0600); err != nil {
				t.Fatal(err)
			}
			probe, err := probeMCAPVideo(context.Background(), filename)
			if err != nil {
				t.Fatal(err)
			}
			if probe.BFrames {
				t.Fatal("fixture has B frames")
			}
			if len(probe.Offsets) != 10 || probe.Offsets[0] != 0 {
				t.Fatal("bad timestamps", probe.Offsets)
			}
			frames := 0
			var stream []byte
			if err := streamMCAPVideo(context.Background(), filename, probe, func(data []byte) error { frames++; stream = append(stream, data...); return nil }); err != nil {
				t.Fatal(err)
			}
			if frames != 10 {
				t.Fatal("wrong frame count", frames)
			}
			assertDecodableVideo(t, stream, codec, 10)
		})
	}
}

func TestAccessUnitParametersAndLimits(t *testing.T) {
	// Every IDR needs parameters, including IDRs following the initial one.
	nal := func(kind byte) []byte { return []byte{0, 0, 0, 1, kind, 0x80} }
	stream := []byte{}
	for _, kind := range []byte{9, 7, 8, 5, 9, 1, 9, 5} {
		stream = append(stream, nal(kind)...)
	}
	frames := [][]byte{}
	if err := readAccessUnits(bytes.NewReader(stream), "h264", func(data []byte) error { frames = append(frames, bytes.Clone(data)); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 || !bytes.Contains(frames[2], nal(7)) || !bytes.Contains(frames[2], nal(8)) {
		t.Fatal("keyframe missing parameters")
	}
	for _, input := range [][]byte{nil, []byte("bad"), nal(5), append(nal(9), bytes.Repeat([]byte{2}, maxVideoFrame)...)} {
		if err := readAccessUnits(bytes.NewReader(input), "h264", func([]byte) error { return nil }); err == nil {
			t.Fatal("invalid/oversized stream accepted")
		}
	}
	var output videoProbeBuffer
	// exec copies stdout with io.Copy. Embedding bytes.Buffer would expose its
	// ReadFrom method and silently bypass the Write budget on that path.
	if _, err := io.Copy(&output, io.LimitReader(strings.NewReader(strings.Repeat("x", (4<<20)+1)), (4<<20)+1)); err == nil {
		t.Fatal("io.Copy bypassed the probe budget")
	}
	output = videoProbeBuffer{}
	if _, err := output.Write(make([]byte, (4<<20)+1)); err == nil {
		t.Fatal("unbounded probe output")
	}
}

func TestInvalidMCAPVideoRemovesPartialExport(t *testing.T) {
	index, _ := fixtureIndex(t)
	if err := os.WriteFile(filepath.Join(index.root, fixtureDrive+"--0", "qcamera.ts"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, _ := newExports(context.Background(), index)
	job, err := manager.submit(ExportRequest{Drive: fixtureDrive, Format: "mcap"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, _ := manager.snapshot(job.ID)
		if status.Status == "failed" {
			if _, err := os.Stat(status.output); !os.IsNotExist(err) {
				t.Fatal("partial MCAP survived")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("corrupt camera did not fail export")
}

// Exercise the actual streaming child, not only queued-job cancellation. The
// wrapper repeats and paces a tiny synthetic input so the watchdog can interrupt
// an active conversion deterministically without expensive test footage.
func TestMCAPVideoStopsOnIgnitionOrUnknown(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	data := cameraFixture(t, "h264", true)
	filename := filepath.Join(t.TempDir(), "qcamera.ts")
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	probe, err := probeMCAPVideo(context.Background(), filename)
	if err != nil {
		t.Fatal(err)
	}
	wrapper := t.TempDir()
	script := []byte("#!/bin/sh\nexec \"$DASH_TEST_FFMPEG\" -stream_loop 9 -re \"$@\"\n")
	if err := os.WriteFile(filepath.Join(wrapper, "ffmpeg"), script, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DASH_TEST_FFMPEG", ffmpeg)
	t.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, next := range []string{"onroad", "unknown"} {
		t.Run(next, func(t *testing.T) {
			var state atomic.Value
			state.Store("parked")
			parent, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			ctx, cancel := parkedContext(parent, func() string { return state.Load().(string) })
			defer cancel()
			frames := 0
			started := time.Now()
			err := streamMCAPVideo(ctx, filename, probe, func([]byte) error {
				frames++
				state.Store(next)
				return ctx.Err()
			})
			if err == nil || frames == 0 || frames >= 10 || ctx.Err() == nil || time.Since(started) > 2*time.Second {
				t.Fatal("video worker did not stop promptly", err, frames, time.Since(started))
			}
		})
	}
}
