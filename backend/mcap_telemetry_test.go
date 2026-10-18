package main

import (
	"bytes"
	capnp "capnproto.org/go/capnp/v3"
	"context"
	"github.com/klauspost/compress/zstd"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dash.local/backend/internal/telemetry"
	"github.com/foxglove/mcap/go/mcap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Parse only the exported descriptors, like a reader with no Dash Go package.
func dynamicMCAPMessage(t *testing.T, schema *mcap.Schema, channel *mcap.Channel, data []byte) *dynamicpb.Message {
	t.Helper()
	if schema.Encoding != "protobuf" || channel.MessageEncoding != "protobuf" {
		t.Fatal("MCAP must be binary protobuf")
	}
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(schema.Data, set); err != nil {
		t.Fatal(err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		t.Fatal("incomplete embedded descriptor set", err)
	}
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(schema.Name))
	if err != nil {
		t.Fatal(err)
	}
	value := dynamicpb.NewMessage(descriptor.(protoreflect.MessageDescriptor))
	if err := proto.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
	return value
}
func TestNativeMCAPRecordedRateAndBinarySchemas(t *testing.T) {
	for _, kind := range []string{"rlog", "qlog"} {
		t.Run(kind, func(t *testing.T) {
			index, _ := fixtureIndex(t)
			os.Remove(filepath.Join(index.root, fixtureDrive+"--0", "qlog.bz2"))
			for _, part := range []string{"0", "1"} {
				folder := filepath.Join(index.root, fixtureDrive+"--"+part)
				if err := os.MkdirAll(folder, 0700); err != nil {
					t.Fatal(err)
				}
				// Include both logs in the rlog case: export must not duplicate qlog data.
				kinds := []string{kind}
				if kind == "rlog" {
					kinds = append(kinds, "qlog")
				}
				for _, name := range kinds {
					source := filepath.Join("../scripts/fixtures/device", fixtureDrive+"--"+part, name+".zst")
					data, err := os.ReadFile(source)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(folder, name+".zst"), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Deliberately do not index first. Exports must resolve their clocks directly
			// from recordings, including data-only parts and repeated route initData.
			manager, err := newExports(context.Background(), index)
			if err != nil {
				t.Fatal(err)
			}
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
			if span := time.Duration(info.Statistics.MessageEndTime - info.Statistics.MessageStartTime); span != 68*time.Second {
				t.Fatal("wrong session clock span", span)
			}
			messages, err := reader.Messages()
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			last := map[string]uint64{}
			for {
				schema, channel, message, err := messages.Next(nil)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				value := dynamicMCAPMessage(t, schema, channel, message.Data)
				if counts[channel.Topic] > 0 && message.LogTime < last[channel.Topic] {
					t.Fatal("topic went backwards", channel.Topic)
				}
				counts[channel.Topic]++
				last[channel.Topic] = message.LogTime
				if schema.Name == "dash.v2.VehicleState" {
					field := value.Descriptor().Fields().ByName("acceleration_mps2")
					if !value.Has(field) {
						t.Fatal("lost optional field")
					}
					boot := value.Get(value.Descriptor().Fields().ByName("log_monotonic_nanoseconds")).Uint()
					if message.LogTime-boot != 1791826828000000000 {
						t.Fatal("inexact UTC/monotonic mapping", message.LogTime, boot)
					}
				}
			}
			vehicles, gps := 6800, 680
			if kind == "qlog" {
				vehicles, gps = 680, 68
			}
			for topic, want := range map[string]int{"/dash/VehicleState": vehicles, "/dash/Engagement": vehicles, "/dash/Position": gps, "/dash/GpsState": gps, "/dash/CameraFrameIndex": 68 * 20 * 3, "/dash/SegmentBoundary": 4} {
				if counts[topic] != want {
					t.Fatalf("%s got %d want %d (%s)", topic, counts[topic], want, kind)
				}
			}
		})
	}
}
func TestRecordedFrameTimingPreservesGaps(t *testing.T) {
	offset := int64(1_700_000_000_000_000_000)
	frames := []telemetry.FrameIndex{{PresentationIndex: 3, EncoderSegment: 9, StartNS: 250000000, EndNS: 255000000}, {PresentationIndex: 0, EncoderSegment: 9, StartNS: 100000000, EndNS: 105000000}}
	times, err := recordedFrameTimes(frames, 2, mcapClock{OffsetNS: &offset})
	if err != nil {
		t.Fatal(err)
	}
	if times[1]-times[0] != 150000000 {
		t.Fatal("dropped-frame gap was flattened")
	}
	if _, err := recordedFrameTimes(frames, 3, mcapClock{}); err == nil {
		t.Fatal("partial camera index accepted")
	}
	frames[1].PresentationIndex = frames[0].PresentationIndex
	if _, err := recordedFrameTimes(frames, 2, mcapClock{}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatal("duplicate index accepted", err)
	}
}

func TestRecordedRouteWallClockWithoutGPS(t *testing.T) {
	source := filepath.Join("../scripts/fixtures/device", fixtureDrive+"--1", "qlog.zst")
	path := filepath.Join(t.TempDir(), "qlog.zst")
	// Rebuild from upstream-generated events without GPS using the generic Cap'n
	// Proto stream, preserving original sentinel and repeated initData timestamps.
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	raw, err := decoder.DecodeAll(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	reader := capnp.NewDecoder(bytes.NewReader(raw))
	for {
		msg, err := reader.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		root, err := msg.Root()
		if err != nil {
			t.Fatal(err)
		}
		kind := root.Struct().Uint16(8)
		if kind != 20 && kind != 47 {
			if err := capnp.NewEncoder(&output).Encode(msg); err != nil {
				t.Fatal(err)
			}
		}
	}
	compressor, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer compressor.Close()
	if err := os.WriteFile(path, compressor.EncodeAll(output.Bytes(), nil), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	logs, clocks, err := resolveMCAPLogs(context.Background(), ExportRequest{Drive: fixtureDrive, Segments: []int{1}}, []ExportAsset{{Segment: 1, Path: path, Size: info.Size(), Modified: time.Now()}}, map[string]IndexedSegment{})
	if err != nil {
		t.Fatal(err)
	}
	if logs[1] != path || clocks[1].timestampNS(160000000000) != 1791826988000000000 || !strings.Contains(clocks[1].Basis, "wallTimeNanos") {
		t.Fatal("wall clock was replaced with copy time", clocks[1])
	}
	summary, err := telemetry.Decode(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if summary.StartUTC != 1791826988 || summary.Duration != 8 {
		t.Fatal("wrong summary without GPS", summary.StartUTC, summary.Duration)
	}
}

func TestInterruptedCameraIndexesUseVerifiedPrefix(t *testing.T) {
	offset := int64(1700000000000000000)
	clock := mcapClock{OffsetNS: &offset}
	frames := []telemetry.FrameIndex{{PresentationIndex: 20, EndNS: 1000000000, Bytes: 100}, {PresentationIndex: 21, EndNS: 1050000000, Bytes: 40}, {PresentationIndex: 23, EndNS: 1100000000, Bytes: 45}}
	probe := videoProbe{Codec: "hevc", Offsets: []float64{0, .05, .1, .15}, Sizes: []int{188, 40, 45, 50}, Step: .05}
	times, err := recoveredFrameTimes(frames, probe, clock)
	if err != nil || len(times) != 4 || times[2] != uint64(offset)+1100000000 || times[3] != uint64(offset)+1150000000 {
		t.Fatal("prefix exposure times or disclosed estimated tail lost", times, err)
	}
	probe.Sizes[1] = 39
	if _, err := recoveredFrameTimes(frames, probe, clock); err == nil {
		t.Fatal("misaligned interrupted index accepted")
	}
	if _, err := recordedFrameTimes(frames, 4, clock); err == nil {
		t.Fatal("complete log count mismatch accepted")
	}
}
