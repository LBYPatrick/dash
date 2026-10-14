package telemetry

import (
	"bytes"
	capnp "capnproto.org/go/capnp/v3"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeIndependentCerealFixture(t *testing.T) {
	result, err := Decode(context.Background(), "testdata/qlog.bz2")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ready" || result.Car != "TEST VEHICLE" || result.Version != "fixture-sunnypilot" {
		t.Fatalf("unexpected identity: %+v", result)
	}
	if len(result.Points) != 11 || len(result.Speeds) != 11 {
		t.Fatalf("unexpected sample counts %d/%d", len(result.Points), len(result.Speeds))
	}
	if result.DistanceSamples != 10 || result.Duration != 10 || result.DistanceKM < .06 || result.DistanceKM > .08 {
		t.Fatalf("unexpected trip: duration=%v distance=%v", result.Duration, result.DistanceKM)
	}
	if result.Points[0].Latitude != 37.77 || result.Points[0].Time != 1791840000 || result.Speeds[0].Speed != 7 || !result.Speeds[0].GasPressed {
		t.Fatal("wire schema values differ from fixture")
	}
}
func TestDecodeCorruptAndCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qlog")
	if err := os.WriteFile(path, []byte("not a log"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(context.Background(), path); err == nil {
		t.Fatal("corrupt log accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Decode(ctx, "testdata/qlog.bz2"); err == nil {
		t.Fatal("canceled decode continued")
	}
}

func TestRecordedSelfdriveEngagement(t *testing.T) {
	var data bytes.Buffer
	for _, state := range []struct {
		seconds                  float64
		enabled, active, invalid bool
	}{{1, false, false, false}, {2, true, true, false}, {2.5, true, false, false}, {2.8, false, false, true}, {5, false, false, false}} {
		message, segment, err := capnp.NewMessage(capnp.SingleSegment(nil))
		if err != nil {
			t.Fatal(err)
		}
		event, err := capnp.NewRootStruct(segment, capnp.ObjectSize{DataSize: 16, PointerCount: 1})
		if err != nil {
			t.Fatal(err)
		}
		event.SetUint64(0, uint64(state.seconds*1e9))
		event.SetUint16(8, 128)
		event.SetBit(80, state.invalid)
		value, err := capnp.NewStruct(segment, capnp.ObjectSize{DataSize: 8})
		if err != nil {
			t.Fatal(err)
		}
		value.SetBit(16, state.enabled)
		value.SetBit(17, state.active)
		if err := event.SetPtr(0, value.ToPtr()); err != nil {
			t.Fatal(err)
		}
		if err := capnp.NewEncoder(&data).Encode(message); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "qlog")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	metadata, err := Decode(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.StartMonotonic != 1 || len(metadata.Engagement) != 4 {
		t.Fatal("invalid engagement samples", metadata.Engagement)
	}
	if !metadata.Engagement[1].Enabled || !metadata.Engagement[1].Active || metadata.Engagement[2].Active {
		t.Fatal("engaged and override states differ from wire")
	}
}

// These files were encoded and round-tripped with the pinned upstream schemas,
// independently of the hand-written Go wire adapter under test.
func TestNativeSunnypilotRecordings(t *testing.T) {
	root := "../../../scripts/fixtures/device/000001a3--c20ba54385--"
	for _, part := range []struct {
		number   string
		duration float64
	}{{"0", 60}, {"1", 8}} {
		for _, kind := range []string{"rlog", "qlog"} {
			path := root + part.number + "/" + kind + ".zst"
			summary, err := Decode(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if summary.Duration != part.duration || summary.Version != "dash-synthetic-sunnypilot" {
				t.Fatalf("wrong native segment metadata: %+v", summary)
			}
			if part.number == "1" && summary.StartMonotonic != 160 {
				t.Fatal("route initData expanded segment start", summary.StartMonotonic)
			}
			vehicles, engagement, gps, frames, boundaries := 0, 0, 0, 0, 0
			err = Stream(context.Background(), path, func(record Record) error {
				if record.Vehicle != nil {
					vehicles++
					if record.CarFields.Acceleration == nil || record.CarFields.SteeringTorque == nil || record.CarFields.RawSpeed == nil {
						t.Fatal("lost vehicle fields")
					}
					if vehicles == 1 && part.number == "0" && (record.Vehicle.Speed != 8 || *record.CarFields.Acceleration != .25 || !record.Vehicle.GasPressed) {
						t.Fatal("wire layout differs from upstream", record.Vehicle, record.CarFields)
					}
				}
				if record.Engagement != nil {
					engagement++
					state := record.SelfdriveState
					wantActive := state == 2 || state == 3 || state == 4
					wantEnabled := state == 1 || wantActive
					if record.Engagement.Active != wantActive || record.Engagement.Enabled != wantEnabled {
						t.Fatal("synthetic engagement differs from upstream state machine", record.Engagement, state)
					}
				}
				if record.Position != nil {
					gps++
					if record.GPS.Source != 6 || record.GPS.HorizontalAccuracy == nil || record.Position.Monotonic < 100 {
						t.Fatal("wrong GPS fields")
					}
				}
				if record.Frame != nil {
					frames++
					if record.Frame.EndNS-record.Frame.StartNS != 5000000 || record.Frame.EncoderSegment < 4 {
						t.Fatal("wrong frame index")
					}
				}
				if record.Boundary != nil {
					boundaries++
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			rate, gpsRate := 100, 10
			if kind == "qlog" {
				rate, gpsRate = 10, 1
			}
			if vehicles != int(part.duration)*rate || engagement != vehicles || gps != int(part.duration)*gpsRate || frames != int(part.duration)*20*4 || boundaries != 2 {
				t.Fatalf("wrong native rates %s/%s: vehicle=%d engagement=%d gps=%d frames=%d boundaries=%d", part.number, kind, vehicles, engagement, gps, frames, boundaries)
			}
		}
	}
}

func TestPreferredLog(t *testing.T) {
	paths := []string{"/x/qlog.bz2", "/x/rlog.bz2", "/x/qlog.zst", "/x/rlog.zst"}
	if PreferredLog(paths, true) != "/x/rlog.zst" || PreferredLog(paths, false) != "/x/qlog.zst" {
		t.Fatal("wrong source preference")
	}
	if PreferredLog([]string{"/x/rlog.zst"}, false) != "/x/rlog.zst" || PreferredLog([]string{"/x/qlog.zst"}, true) != "/x/qlog.zst" {
		t.Fatal("missing fallback")
	}
}

func TestRecordedStreamCancelsBetweenEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	err := Stream(ctx, "../../../scripts/fixtures/device/000001a3--c20ba54385--0/rlog.zst", func(Record) error { count++; cancel(); return nil })
	if err != context.Canceled || count != 1 {
		t.Fatal("raw-rate decoding continued after cancellation", count, err)
	}
}

func TestSegmentSentinelsOverrideQueuedReceiveTimes(t *testing.T) {
	var span CaptureSpan
	start, end := uint16(2), uint16(0)
	for _, record := range []Record{{MonotonicNS: 100000000000, Kind: 71, Valid: true, Boundary: &start}, {MonotonicNS: 90000000000, Kind: 21, Valid: true}, {MonotonicNS: 108000000000, Kind: 71, Valid: true, Boundary: &end}, {MonotonicNS: 109000000000, Kind: 14, Valid: true}} {
		span.Observe(record)
	}
	first, last, seen := span.Bounds()
	if !seen || first != 100000000000 || last != 108000000000 {
		t.Fatal("queued messages stretched sentinel boundaries", first, last)
	}
}
