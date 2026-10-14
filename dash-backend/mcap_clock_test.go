package main

import (
	"testing"
	"time"

	"dash.local/backend/internal/telemetry"
)

func TestMCAPClockPreservesCaptureGapsAndMissingGPS(t *testing.T) {
	request := ExportRequest{Drive: fixtureDrive, Segments: []int{0, 1, 2, 3}}
	assets := []ExportAsset{{Segment: 0, Modified: time.Unix(900000, 0)}}
	snapshot := map[string]IndexedSegment{
		fixtureDrive + "--0": {Metadata: telemetry.Metadata{StartMonotonic: 50, Duration: 10}},
		fixtureDrive + "--1": {Metadata: telemetry.Metadata{StartMonotonic: 60, Duration: 10, Points: []telemetry.Position{{Time: 1002, Monotonic: 62}}}},
		fixtureDrive + "--2": {Metadata: telemetry.Metadata{StartMonotonic: 360, Duration: 10, Points: []telemetry.Position{{Time: 1300, Monotonic: 360}}}},
	}
	clocks := mcapClocks(request, assets, snapshot)
	for segment, want := range map[int]uint64{0: 990e9, 1: 1000e9, 2: 1300e9, 3: 1310e9} {
		clock := clocks[segment]
		if got := clock.timestamp(clock.Monotonic); got != want {
			t.Fatalf("part %d: got %d want %d", segment, got, want)
		}
	}
	if clocks[0].timestamp(55) != 995e9 {
		t.Fatal("vehicle samples must retain monotonic offset")
	}
}

func TestMCAPClockFallbackUsesOneFilesystemAnchor(t *testing.T) {
	request := ExportRequest{Drive: fixtureDrive, Segments: []int{0, 1, 3}}
	assets := []ExportAsset{{Segment: 0, Modified: time.Unix(1000, 0)}, {Segment: 1, Modified: time.Unix(900000, 0)}}
	snapshot := map[string]IndexedSegment{fixtureDrive + "--0": {Files: []FileInfo{{Name: "qcamera.ts", Duration: 8}}}, fixtureDrive + "--1": {Metadata: telemetry.Metadata{Duration: 12}}}
	clocks := mcapClocks(request, assets, snapshot)
	for segment, want := range map[int]uint64{0: 992e9, 1: 1000e9, 3: 1072e9} {
		if got := clocks[segment].timestamp(0); got != want {
			t.Fatalf("part %d: got %d want %d", segment, got, want)
		}
	}
}
