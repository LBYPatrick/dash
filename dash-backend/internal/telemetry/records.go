package telemetry

import (
	"compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	capnp "capnproto.org/go/capnp/v3"
	"github.com/klauspost/compress/zstd"
)

// Record is a short-lived decoded event. Stream calls emit at the recorded rate;
// callers must not use the dashboard's one-second summaries for data export.
type Record struct {
	SelfdriveState uint16
	MonotonicNS    uint64
	Kind           uint16
	Valid          bool
	Position       *Position
	Vehicle        *VehicleState
	Engagement     *Engagement
	GPS            *GPSFields
	CarFields      *CarFields
	Frame          *FrameIndex
	Version, Car   string
	WallTimeNS     uint64
	DeviceType     uint16
	Boundary       *uint16
}
type GPSFields struct {
	UTCMillis                            int64
	Source                               uint16
	Bearing                              *float64
	HorizontalAccuracy, VerticalAccuracy *float64
}
type CarFields struct {
	Acceleration, SteeringTorque, RawSpeed *float64
	SteeringPressed, Standstill            bool
}
type FrameIndex struct {
	Camera                         string
	FrameID, EncodeID              uint32
	EncoderSegment                 int32
	PresentationIndex, EncodeIndex uint32
	StartNS, EndNS                 uint64
	Flags, Bytes                   uint32
}

// PreferredLog chooses one recording, never merging rlog and its qlog subset.
// Indexing prefers qlog; exports prefer the full-rate rlog. Modern zstd wins.
func PreferredLog(paths []string, fullRate bool) string {
	kinds := []string{"qlog", "rlog"}
	if fullRate {
		kinds = []string{"rlog", "qlog"}
	}
	for _, kind := range kinds {
		for _, extension := range []string{".zst", ".bz2", ""} {
			for _, path := range paths {
				if filepath.Base(path) == kind+extension {
					return path
				}
			}
		}
	}
	return ""
}

// IncompleteLogError reports an interrupted file after complete messages were
// recovered. Malformed messages, budget errors and cancellation remain fatal.
type IncompleteLogError struct {
	Records int
	Cause   error
}

func (e *IncompleteLogError) Error() string {
	return fmt.Sprintf("incomplete log tail; recovered %d complete events: %v", e.Records, e.Cause)
}
func (e *IncompleteLogError) Unwrap() error { return e.Cause }
func IsIncomplete(err error) bool           { var tail *IncompleteLogError; return errors.As(err, &tail) }

// Stream reads unpacked cereal without generated comma types or Python. Layouts
// are checked against the pinned upstream schemas by independently built fixtures.
// Unknown events still reach emit for clock observation, with no invented fields.
func Stream(ctx context.Context, filename string, emit func(Record) error) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	var source io.Reader = file
	if strings.HasSuffix(filename, ".bz2") {
		source = bzip2.NewReader(file)
	}
	if strings.HasSuffix(filename, ".zst") {
		decoder, err := zstd.NewReader(file, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20))
		if err != nil {
			return err
		}
		defer decoder.Close()
		source = decoder
	}
	limited := &io.LimitedReader{R: contextReader{ctx, source}, N: 256 << 20}
	decoder := capnp.NewDecoder(limited)
	decoder.MaxMessageSize = 16 << 20
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		message, err := decoder.Decode()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if count > 0 && limited.N > 0 && errors.Is(err, io.ErrUnexpectedEOF) {
				return &IncompleteLogError{Records: count, Cause: err}
			}
			return fmt.Errorf("decode cereal: %w", err)
		}
		root, err := message.Root()
		if err != nil {
			return err
		}
		event := root.Struct()
		if !event.IsValid() || event.Size().DataSize < 16 {
			return errors.New("invalid cereal event")
		}
		record := Record{MonotonicNS: event.Uint64(0), Kind: event.Uint16(8), Valid: !event.Bit(80)}
		count++
		payload, err := event.Ptr(0)
		if err != nil {
			return err
		}
		value := payload.Struct()
		if value.IsValid() && record.Valid {
			mono := float64(record.MonotonicNS) / 1e9
			switch record.Kind {
			case 128:
				if value.Size().DataSize >= 3 {
					record.SelfdriveState = value.Uint16(0)
					record.Engagement = &Engagement{mono, value.Bit(16), value.Bit(17)}
				}
			case 20, 47:
				if value.Size().DataSize < 48 {
					break
				}
				lat, lon, alt := math.Float64frombits(value.Uint64(8)), math.Float64frombits(value.Uint64(16)), math.Float64frombits(value.Uint64(24))
				speed := float64(math.Float32frombits(value.Uint32(4)))
				utc := int64(value.Uint64(40))
				fix := value.Uint16(0)&1 != 0 || value.Bit(480)
				if fix && finite(lat, lon, alt, speed) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 && utc > 946684800000 && utc < math.MaxInt64/1000000 {
					record.Position = &Position{lat, lon, alt, speed, float64(utc) / 1000, mono}
					record.GPS = &GPSFields{UTCMillis: utc, Source: value.Uint16(2), Bearing: optionalFloat(value, 32), HorizontalAccuracy: optionalFloat(value, 36), VerticalAccuracy: optionalFloat(value, 48)}
				}
			case 21:
				if value.Size().DataSize < 20 {
					break
				}
				speed, angle := float64(math.Float32frombits(value.Uint32(0))), float64(math.Float32frombits(value.Uint32(16)))
				if finite(speed, angle) {
					record.Vehicle = &VehicleState{mono, speed, angle, value.Bit(65), value.Bit(64)}
					record.CarFields = &CarFields{Acceleration: optionalFloat(value, 28), SteeringTorque: optionalFloat(value, 20), RawSpeed: optionalFloat(value, 32), SteeringPressed: value.Bit(66), Standstill: value.Bit(67)}
				}
			case 14, 74, 75, 88:
				if value.Size().DataSize < 48 {
					break
				}
				camera := map[uint16]string{14: "road", 74: "driver", 75: "wide_road", 88: "preview"}[record.Kind]
				record.Frame = &FrameIndex{Camera: camera, FrameID: value.Uint32(0), EncodeID: value.Uint32(8), EncoderSegment: int32(value.Uint32(12)), PresentationIndex: value.Uint32(16), EncodeIndex: value.Uint32(20), StartNS: value.Uint64(24), EndNS: value.Uint64(32), Flags: value.Uint32(40), Bytes: value.Uint32(44)}
			case 71:
				if value.Size().DataSize >= 2 {
					boundary := value.Uint16(0)
					record.Boundary = &boundary
				}
			case 0:
				p, err := value.Ptr(3)
				if err == nil {
					record.Version = p.Text()
				}
				record.WallTimeNS = value.Uint64(8)
				record.DeviceType = value.Uint16(0)
			case 67:
				p, err := value.Ptr(1)
				if err == nil {
					record.Car = p.Text()
				}
			}
		}
		err = emit(record)
		message.Release()
		if err != nil {
			return err
		}
	}
	if limited.N == 0 {
		return errors.New("log exceeds 256 MiB decoded limit")
	}
	if count == 0 {
		return errors.New("log contains no cereal events")
	}
	return nil
}
func optionalFloat(value capnp.Struct, offset capnp.DataOffset) *float64 {
	if uint32(value.Size().DataSize) < uint32(offset)+4 {
		return nil
	}
	result := float64(math.Float32frombits(value.Uint32(offset)))
	if !finite(result) {
		return nil
	}
	return &result
}
