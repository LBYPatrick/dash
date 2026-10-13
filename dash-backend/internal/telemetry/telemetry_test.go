package telemetry

import (
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
	if result.Duration != 11 || result.DistanceKM < .06 || result.DistanceKM > .08 {
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
