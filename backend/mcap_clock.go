package main

import (
	"fmt"
	"math"
)

type mcapClock struct {
	UTC, Monotonic float64
	Basis          string
	OffsetNS       *int64
}

func (c mcapClock) timestamp(monotonic float64) uint64 {
	return c.timestampNS(uint64(max(0, math.Round(monotonic*1e9))))
}
func (c mcapClock) timestampNS(monotonic uint64) uint64 {
	if c.OffsetNS != nil {
		offset := *c.OffsetNS
		if offset < 0 {
			difference := uint64(-offset)
			if monotonic < difference {
				return 0
			}
			return monotonic - difference
		}
		if monotonic > math.MaxUint64-uint64(offset) {
			return 0
		}
		return monotonic + uint64(offset)
	}
	return uint64(max(0, math.Round(c.UTC*1e9)+float64(monotonic)-math.Round(c.Monotonic*1e9)))
}

// Resolve once for the entire export. Filesystem timestamps describe copying or
// finalization, not capture: never let individual files introduce another clock.
// This uses cached metadata only and starts no additional media workers.
func mcapClocks(request ExportRequest, assets []ExportAsset, snapshot map[string]IndexedSegment) map[int]mcapClock {
	result := map[int]mcapClock{}
	if len(request.Segments) == 0 || len(assets) == 0 {
		return result
	}
	var gps *mcapClock
	durations := map[int]float64{}
	offsets := map[int]float64{}
	elapsed := 0.0
	previous := request.Segments[0]
	for _, segment := range request.Segments {
		elapsed += float64(max(0, segment-previous-1)) * 60
		offsets[segment] = elapsed
		indexed := snapshot[fmt.Sprintf("%s--%d", request.Drive, segment)]
		entry := indexed.Metadata
		duration := entry.Duration
		for _, file := range indexed.Files {
			duration = max(duration, file.Duration)
		}
		durations[segment] = duration
		if gps == nil && len(entry.Points) > 0 {
			point := entry.Points[0]
			anchor := mcapClock{UTC: point.Time, Monotonic: point.Monotonic, Basis: "session GPS UTC mapped to log monotonic time"}
			gps = &anchor
		}
		if duration <= 0 {
			duration = 60
		}
		elapsed += duration
		previous = segment
	}
	first := request.Segments[0]
	// A single session-wide filesystem estimate is used only without GPS.
	fallback := float64(assets[0].Modified.UnixNano())/1e9 - durations[first]
	wallClock := false
	for _, segment := range request.Segments {
		entry := snapshot[fmt.Sprintf("%s--%d", request.Drive, segment)].Metadata
		if entry.StartUTC > 0 {
			fallback = entry.StartUTC - offsets[segment]
			wallClock = true
			break
		}
	}
	if gps != nil {
		for _, segment := range request.Segments {
			entry := snapshot[fmt.Sprintf("%s--%d", request.Drive, segment)].Metadata
			if len(entry.Points) > 0 {
				fallback = gps.UTC + entry.StartMonotonic - gps.Monotonic - offsets[segment]
				break
			}
		}
	}
	correction := 0.0
	for _, segment := range request.Segments {
		entry := snapshot[fmt.Sprintf("%s--%d", request.Drive, segment)].Metadata
		clock := mcapClock{UTC: fallback + offsets[segment] + correction, Monotonic: entry.StartMonotonic, Basis: "session file-time estimate + indexed durations; unknown/missing parts assume 60 seconds"}
		if wallClock {
			clock.Basis = "recorded route wall clock + indexed durations; unknown/missing parts assume 60 seconds"
		}
		if gps != nil {
			clock.Basis = "session GPS clock + indexed durations; unknown/missing parts assume 60 seconds"
		}
		if len(entry.Points) > 0 {
			point := entry.Points[0]
			clock.UTC = point.Time + entry.StartMonotonic - point.Monotonic
			clock.Basis = "segment GPS UTC mapped to first log event; exposure timestamps unavailable"
		} else if gps == nil && entry.StartUTC > 0 {
			clock.UTC = entry.StartUTC
		} else if gps != nil && (entry.Duration > 0 || len(entry.Speeds) > 0) {
			clock.UTC = gps.UTC + entry.StartMonotonic - gps.Monotonic
			clock.Basis = gps.Basis
		}
		result[segment] = clock
		correction = clock.UTC - fallback - offsets[segment]
	}
	return result
}
