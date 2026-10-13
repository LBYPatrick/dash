// Package telemetry decodes a deliberately small, wire-compatible subset of
// cereal logs into Dash's own model. It does not require Python on the device.
package telemetry

import (
	"compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	capnp "capnproto.org/go/capnp/v3"
	"github.com/klauspost/compress/zstd"
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
type Metadata struct {
	DistanceSamples int            `json:"distanceSamples,omitempty"`
	StartMonotonic  float64        `json:"startMonotonic"`
	Engagement      []Engagement   `json:"engagement,omitempty"`
	Status          string         `json:"status"`
	Reason          string         `json:"reason,omitempty"`
	Duration        float64        `json:"duration"`
	DistanceKM      float64        `json:"distanceKm"`
	Points          []Position     `json:"points,omitempty"`
	Speeds          []VehicleState `json:"speeds,omitempty"`
	Car             string         `json:"car,omitempty"`
	Version         string         `json:"version,omitempty"`
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

// Offsets and union tags are the stable cereal wire layout, verified against
// sunnypilot master log.capnp and opendbc car.capnp. See SCHEMA.md and fixtures.
func Decode(ctx context.Context, filename string) (Metadata, error) {
	result := Metadata{Status: "ready"}
	file, err := os.Open(filename)
	if err != nil {
		return result, err
	}
	defer file.Close()
	var source io.Reader = file
	if strings.HasSuffix(filename, ".bz2") {
		source = bzip2.NewReader(file)
	}
	if strings.HasSuffix(filename, ".zst") {
		decoder, err := zstd.NewReader(file, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20))
		if err != nil {
			return result, err
		}
		defer decoder.Close()
		source = decoder
	}
	limited := &io.LimitedReader{R: contextReader{ctx, source}, N: 256 << 20}
	decoder := capnp.NewDecoder(limited)
	decoder.MaxMessageSize = 16 << 20
	first, last := math.Inf(1), float64(0)
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		message, err := decoder.Decode()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return result, fmt.Errorf("decode cereal: %w", err)
		}
		root, err := message.Root()
		if err != nil {
			return result, err
		}
		event := root.Struct()
		if !event.IsValid() || event.Size().DataSize < 16 {
			return result, errors.New("invalid cereal event")
		}
		mono := float64(event.Uint64(0)) / 1e9
		if mono < first {
			first = mono
		}
		if mono > last {
			last = mono
		}
		count++
		kind := event.Uint16(8)
		payload, err := event.Ptr(0)
		if err != nil {
			return result, err
		}
		value := payload.Struct()
		if !value.IsValid() {
			continue
		}
		if len(result.Points) > 7200 || len(result.Speeds) > 7200 || len(result.Engagement) > 7200 {
			return result, errors.New("segment exceeds telemetry sample budget")
		}
		switch kind {
		case 128: // Event.selfdriveState @130 (valid is the second non-union field).
			if event.Bit(80) || value.Size().DataSize < 3 {
				continue
			}
			sample := Engagement{mono, value.Bit(16), value.Bit(17)}
			if len(result.Engagement) == 0 || mono-result.Engagement[len(result.Engagement)-1].Time >= 1 || sample.Enabled != result.Engagement[len(result.Engagement)-1].Enabled || sample.Active != result.Engagement[len(result.Engagement)-1].Active {
				result.Engagement = append(result.Engagement, sample)
			}

		case 20, 47: // gpsLocation, gpsLocationExternal
			if event.Bit(80) {
				continue
			} // Event.valid defaults to true (wire XOR).
			lat, lon := math.Float64frombits(value.Uint64(8)), math.Float64frombits(value.Uint64(16))
			altitude := math.Float64frombits(value.Uint64(24))
			speed := float64(math.Float32frombits(value.Uint32(4)))
			utc := float64(int64(value.Uint64(40))) / 1000
			valid := value.Uint16(0)&1 != 0 || value.Bit(480)
			if valid && finite(lat, lon, altitude, speed, utc) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 && utc > 946684800 {
				if len(result.Points) == 0 || mono-result.Points[len(result.Points)-1].Monotonic >= 1 {
					result.Points = append(result.Points, Position{lat, lon, altitude, math.Max(0, speed), utc, mono})
				}
			}
		case 21: // carState
			if event.Bit(80) {
				continue
			}
			speed := float64(math.Float32frombits(value.Uint32(0)))
			angle := float64(math.Float32frombits(value.Uint32(16)))
			if finite(speed, angle) && (len(result.Speeds) == 0 || mono-result.Speeds[len(result.Speeds)-1].Time >= 1) {
				result.Speeds = append(result.Speeds, VehicleState{mono, math.Max(0, speed), angle, value.Bit(65), value.Bit(64)})
			}
		case 0: // initData
			p, err := value.Ptr(3)
			if err == nil {
				result.Version = p.Text()
			}
		case 67: // carParams
			p, err := value.Ptr(1)
			if err == nil {
				result.Car = p.Text()
			}
		}
	}
	if limited.N == 0 {
		return result, errors.New("qlog exceeds 256 MiB decoded limit")
	}
	if count == 0 {
		return result, errors.New("qlog contains no cereal events")
	}
	result.StartMonotonic = first
	result.Duration = math.Max(0, last-first)
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
