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
	if result.DistanceSamples != 10 || result.Duration != 11 || result.DistanceKM < .06 || result.DistanceKM > .08 {
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
