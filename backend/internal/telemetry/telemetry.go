// Package telemetry decodes a deliberately small, wire-compatible subset of
// cereal logs into Dash's own model. It does not require Python on the device.
package telemetry

import (
	"context"
	"errors"
	"io"
	"math"
)

type Position struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Altitude  float64 `json:"altitude"`
	Speed     float64 `json:"speed"`
	Time      float64 `json:"time"`
	Monotonic float64 `json:"monotonic"`
}
type VehicleState struct {
	Time          float64 `json:"time"`
	Speed         float64 `json:"speed"`
	SteeringAngle float64 `json:"steeringAngle"`
	BrakePressed  bool    `json:"brakePressed"`
	GasPressed    bool    `json:"gasPressed"`
}
type Engagement struct {
	Time    float64 `json:"time"`
	Enabled bool    `json:"enabled"`
	Active  bool    `json:"active"`
}
type CameraSummary struct {
	Frames int    `json:"frames"`
	Bytes  uint64 `json:"bytes"`
}
type Metadata struct {
	TimeSource      string                   `json:"timeSource,omitempty"`
	Cameras         map[string]CameraSummary `json:"cameras,omitempty"`
	Partial         bool                     `json:"partial,omitempty"`
	StartUTC        float64                  `json:"startUTC,omitempty"`
	DistanceSamples int                      `json:"distanceSamples,omitempty"`
	StartMonotonic  float64                  `json:"startMonotonic"`
	Engagement      []Engagement             `json:"engagement,omitempty"`
	Status          string                   `json:"status"`
	Reason          string                   `json:"reason,omitempty"`
	Duration        float64                  `json:"duration"`
	DistanceKM      float64                  `json:"distanceKm"`
	Points          []Position               `json:"points,omitempty"`
	Speeds          []VehicleState           `json:"speeds,omitempty"`
	Car             string                   `json:"car,omitempty"`
	Version         string                   `json:"version,omitempty"`
}
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func finite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

// Decode builds a bounded display summary. initData is reused by loggerd across
// parts; its route-start timestamp must never extend later segment durations.
func Decode(ctx context.Context, filename string) (Metadata, error) {
	result := Metadata{Status: "ready", Cameras: map[string]CameraSummary{}}
	var span CaptureSpan
	var wallOffset float64
	err := Stream(ctx, filename, func(record Record) error {
		mono := float64(record.MonotonicNS) / 1e9
		span.Observe(record)
		if frame := record.Frame; frame != nil {
			camera := result.Cameras[frame.Camera]
			camera.Frames++
			camera.Bytes += uint64(frame.Bytes)
			result.Cameras[frame.Camera] = camera
		}
		if record.Kind == 0 && record.WallTimeNS > 946684800000000000 && record.WallTimeNS < math.MaxInt64 {
			wallOffset = float64(record.WallTimeNS)/1e9 - mono
		}
		if record.Version != "" {
			result.Version = record.Version
		}
		if record.Car != "" {
			result.Car = record.Car
		}
		if sample := record.Position; sample != nil && (len(result.Points) == 0 || mono-result.Points[len(result.Points)-1].Monotonic >= 1) {
			result.Points = append(result.Points, *sample)
		}
		if sample := record.Vehicle; sample != nil && (len(result.Speeds) == 0 || mono-result.Speeds[len(result.Speeds)-1].Time >= 1) {
			result.Speeds = append(result.Speeds, *sample)
		}
		if sample := record.Engagement; sample != nil {
			n := len(result.Engagement)
			if n == 0 || mono-result.Engagement[n-1].Time >= 1 || sample.Enabled != result.Engagement[n-1].Enabled || sample.Active != result.Engagement[n-1].Active {
				result.Engagement = append(result.Engagement, *sample)
			}
		}
		if len(result.Points) > 7200 || len(result.Speeds) > 7200 || len(result.Engagement) > 7200 {
			return errors.New("segment exceeds telemetry sample budget")
		}
		return nil
	})
	if err != nil {
		if !IsIncomplete(err) {
			return result, err
		}
		result.Partial = true
		result.Reason = err.Error()
	}
	start, end, observed := span.Bounds()
	if observed {
		result.StartMonotonic = float64(start) / 1e9
		result.Duration = float64(end-start) / 1e9
	}
	if len(result.Points) > 0 {
		point := result.Points[0]
		result.StartUTC = point.Time + result.StartMonotonic - point.Monotonic
		result.TimeSource = "gps"
	} else if wallOffset != 0 && observed {
		result.StartUTC = result.StartMonotonic + wallOffset
		result.TimeSource = "device"
	}
	for i := 1; i < len(result.Points); i++ {
		a, b := result.Points[i-1], result.Points[i]
		dt := b.Time - a.Time
		if dt <= 0 || dt > 10 {
			continue
		}
		rad := math.Pi / 180
		h := math.Pow(math.Sin((b.Latitude-a.Latitude)*rad/2), 2) + math.Cos(a.Latitude*rad)*math.Cos(b.Latitude*rad)*math.Pow(math.Sin((b.Longitude-a.Longitude)*rad/2), 2)
		km := 6371 * 2 * math.Atan2(math.Sqrt(h), math.Sqrt(math.Max(0, 1-h)))
		if km/dt <= 0.09 {
			result.DistanceKM += km
			result.DistanceSamples++
		}
	}

	return result, nil
}
