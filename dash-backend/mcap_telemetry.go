package main

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"

	dashv2 "dash.local/backend/internal/mcaptypes/dashv2"
	foxglove "dash.local/backend/internal/mcaptypes/foxglove"
	"dash.local/backend/internal/telemetry"
	"github.com/foxglove/mcap/go/mcap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// protobufSchema embeds a complete descriptor set so readers need no Dash code,
// generated types, schema server, JSON decoder, or comma schema package.
func protobufSchema(id uint16, value proto.Message) (*mcap.Schema, error) {
	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	var add func(protoreflect.FileDescriptor)
	add = func(file protoreflect.FileDescriptor) {
		if seen[file.Path()] {
			return
		}
		seen[file.Path()] = true
		for n := 0; n < file.Imports().Len(); n++ {
			add(file.Imports().Get(n).FileDescriptor)
		}
		set.File = append(set.File, protodesc.ToFileDescriptorProto(file))
	}
	descriptor := value.ProtoReflect().Descriptor()
	add(descriptor.ParentFile())
	data, err := proto.Marshal(set)
	return &mcap.Schema{ID: id, Name: string(descriptor.FullName()), Encoding: "protobuf", Data: data}, err
}
func writeProtobufSchema(writer *mcap.Writer, id uint16, value proto.Message) error {
	schema, err := protobufSchema(id, value)
	if err != nil {
		return err
	}
	return writer.WriteSchema(schema)
}
func protobufTime(ns uint64) *timestamppb.Timestamp {
	return &timestamppb.Timestamp{Seconds: int64(ns / 1e9), Nanos: int32(ns % 1e9)}
}

const videoSchemaID uint16 = 9

func mcapTelemetrySchemas(writer *mcap.Writer) error {
	values := []proto.Message{&dashv2.Asset{}, &foxglove.LocationFix{}, &dashv2.VehicleState{}, &dashv2.Engagement{}, &dashv2.GpsState{}, &dashv2.CameraFrameIndex{}, &dashv2.SessionInfo{}, &dashv2.SegmentBoundary{}}
	topics := []string{"Asset", "Position", "VehicleState", "Engagement", "GpsState", "CameraFrameIndex", "SessionInfo", "SegmentBoundary"}
	for n, value := range values {
		id := uint16(n + 1)
		if err := writeProtobufSchema(writer, id, value); err != nil {
			return err
		}
		if err := writer.WriteChannel(&mcap.Channel{ID: id, SchemaID: id, Topic: "/dash/" + topics[n], MessageEncoding: "protobuf"}); err != nil {
			return err
		}
	}
	return nil
}

// Resolve recording clocks before emitting any event; vehicle samples can occur
// before the first GPS message. Keep only one int64 offset per segment in memory.
func resolveMCAPLogs(ctx context.Context, request ExportRequest, assets []ExportAsset, snapshot map[string]IndexedSegment) (map[int]string, map[int]mcapClock, error) {
	paths := map[int][]string{}
	for _, asset := range assets {
		paths[asset.Segment] = append(paths[asset.Segment], asset.Path)
	}
	logs := map[int]string{}
	offsets := map[int]*int64{}
	wallOffsets := map[int]*int64{}
	observed := map[int]bool{}
	resolved := map[string]IndexedSegment{}
	for name, entry := range snapshot {
		resolved[name] = entry
	}
	var sessionOffset *int64
	segments := []int{}
	for segment := range paths {
		segments = append(segments, segment)
	}
	sort.Ints(segments)
	for _, segment := range segments {
		path := telemetry.PreferredLog(paths[segment], true)
		if path == "" {
			continue
		}
		logs[segment] = path
		var offset, wallOffset *int64
		var span telemetry.CaptureSpan
		metadata := telemetry.Metadata{}
		err := telemetry.Stream(ctx, path, func(record telemetry.Record) error {
			if record.MonotonicNS > math.MaxInt64 {
				return fmt.Errorf("log monotonic timestamp exceeds supported range")
			}
			if record.Kind == 0 && record.WallTimeNS > 946684800000000000 && record.WallTimeNS < math.MaxInt64 {
				value := int64(record.WallTimeNS) - int64(record.MonotonicNS)
				wallOffset = &value
			}
			if record.GPS != nil && offset == nil {
				value := record.GPS.UTCMillis*1000000 - int64(record.MonotonicNS)
				offset = &value
				metadata.Points = []telemetry.Position{*record.Position}
			}
			span.Observe(record)
			return nil
		})
		if err != nil {
			return nil, nil, fmt.Errorf("%d/%s: %w", segment, filepath.Base(path), err)
		}
		start, end, seen := span.Bounds()
		if seen {
			observed[segment] = true
			metadata.StartMonotonic = float64(start) / 1e9
			metadata.Duration = float64(end-start) / 1e9
			if wallOffset != nil {
				metadata.StartUTC = metadata.StartMonotonic + float64(*wallOffset)/1e9
			}
		}
		name := fmt.Sprintf("%s--%d", request.Drive, segment)
		entry := resolved[name]
		entry.Metadata = metadata
		resolved[name] = entry
		offsets[segment] = offset
		if observed[segment] {
			wallOffsets[segment] = wallOffset
		}
		if sessionOffset == nil {
			sessionOffset = offset
		}
	}
	clocks := mcapClocks(request, assets, resolved)
	for segment, offset := range offsets {
		if offset != nil {
			clock := clocks[segment]
			clock.OffsetNS = offset
			clock.Basis = "recorded GPS UTC mapped to nanosecond log monotonic clock"
			clocks[segment] = clock
		}
	}
	if sessionOffset != nil {
		for segment := range logs {
			clock := clocks[segment]
			if clock.OffsetNS == nil && observed[segment] {
				clock.OffsetNS = sessionOffset
				clock.Basis = "session GPS UTC mapped to log monotonic clock"
				clocks[segment] = clock
			}
		}
	}
	if sessionOffset == nil {
		for _, segment := range segments {
			if wallOffsets[segment] != nil {
				sessionOffset = wallOffsets[segment]
				break
			}
		}
		if sessionOffset != nil {
			for segment := range logs {
				if !observed[segment] {
					continue
				}
				offset := wallOffsets[segment]
				if offset == nil {
					offset = sessionOffset
				}
				clock := clocks[segment]
				clock.OffsetNS = offset
				clock.Basis = "recorded initData wallTimeNanos mapped to log monotonic clock (device wall clock, no GPS UTC)"
				clocks[segment] = clock
			}
		}
	}
	return logs, clocks, nil
}

func mcapLogTelemetry(ctx context.Context, writer *mcap.Writer, segment int, path string, clock mcapClock, message videoMessageWriter) error {
	if err := writer.WriteMetadata(&mcap.Metadata{Name: fmt.Sprintf("dash.telemetry.%d", segment), Metadata: map[string]string{"source": filepath.Base(path), "sampling": "recorded rate; no dashboard thinning; one log source per segment", "timeBasis": clock.Basis, "coverage": "GPS fixes, carState, core selfdriveState, stored-camera EncodeIdx, initData identity, carParams identity, sentinel; all other original events remain in log attachments"}}); err != nil {
		return err
	}
	return telemetry.Stream(ctx, path, func(record telemetry.Record) error {
		timestamp := clock.timestampNS(record.MonotonicNS)
		stamp := protobufTime(timestamp)
		part := uint32(segment)
		if record.Kind == 0 {
			timestamp = clock.timestamp(clock.Monotonic)
			stamp = protobufTime(timestamp)
		} // Repeated route init belongs at this part's start.
		if sample := record.Position; sample != nil {
			// LocationFix carries measured GPS UTC, not receive time. Covariance remains
			// UNKNOWN: source accuracy estimates are not a measured ENU covariance matrix.
			fixTime := uint64(record.GPS.UTCMillis) * 1000000
			fix := &foxglove.LocationFix{Timestamp: protobufTime(fixTime), FrameId: "dash_gnss", Latitude: sample.Latitude, Longitude: sample.Longitude, Altitude: sample.Altitude}
			if record.GPS.Bearing != nil {
				fix.Heading = proto.Float64(*record.GPS.Bearing * math.Pi / 180)
			}
			if err := message(2, fixTime, fix); err != nil {
				return err
			}
			source := "gpsLocation"
			if record.Kind == 47 {
				source = "gpsLocationExternal"
			}
			gps := &dashv2.GpsState{Timestamp: stamp, LogMonotonicNanoseconds: record.MonotonicNS, Segment: part, SourceEvent: source, UnixTimestampMillis: record.GPS.UTCMillis, SpeedMps: sample.Speed, BearingDegrees: record.GPS.Bearing, HorizontalAccuracyMeters: record.GPS.HorizontalAccuracy, VerticalAccuracyMeters: record.GPS.VerticalAccuracy, SensorSource: uint32(record.GPS.Source)}
			if err := message(5, timestamp, gps); err != nil {
				return err
			}
		}
		if sample := record.Vehicle; sample != nil {
			fields := record.CarFields
			value := &dashv2.VehicleState{Timestamp: stamp, LogMonotonicNanoseconds: record.MonotonicNS, Segment: part, SpeedMps: sample.Speed, SteeringAngleDegrees: sample.SteeringAngle, BrakePressed: sample.BrakePressed, GasPressed: sample.GasPressed, AccelerationMps2: fields.Acceleration, SteeringTorque: fields.SteeringTorque, RawSpeedMps: fields.RawSpeed, SteeringPressed: fields.SteeringPressed, Standstill: fields.Standstill}
			if err := message(3, timestamp, value); err != nil {
				return err
			}
		}
		if sample := record.Engagement; sample != nil {
			if err := message(4, timestamp, &dashv2.Engagement{Timestamp: stamp, LogMonotonicNanoseconds: record.MonotonicNS, Segment: part, Enabled: sample.Enabled, Active: sample.Active, State: uint32(record.SelfdriveState)}); err != nil {
				return err
			}
		}
		if frame := record.Frame; frame != nil {
			if err := message(6, timestamp, &dashv2.CameraFrameIndex{Timestamp: stamp, LogMonotonicNanoseconds: record.MonotonicNS, Segment: part, Camera: frame.Camera, FrameId: frame.FrameID, EncodeId: frame.EncodeID, EncoderSegment: frame.EncoderSegment, PresentationIndex: frame.PresentationIndex, EncodeIndex: frame.EncodeIndex, StartOfFrameNanoseconds: frame.StartNS, EndOfFrameNanoseconds: frame.EndNS, Flags: frame.Flags, EncodedBytes: frame.Bytes}); err != nil {
				return err
			}
		}
		if record.Version != "" || record.Car != "" {
			value := &dashv2.SessionInfo{Timestamp: stamp, LogMonotonicNanoseconds: record.MonotonicNS, Segment: part}
			if record.Kind == 0 {
				value.SourceEvent = "initData"
				value.FirmwareVersion = proto.String(record.Version)
				value.RouteWallTimeNanoseconds = proto.Uint64(record.WallTimeNS)
				value.DeviceType = proto.Uint32(uint32(record.DeviceType))
			} else {
				value.SourceEvent = "carParams"
				value.CarFingerprint = proto.String(record.Car)
			}
			if err := message(7, timestamp, value); err != nil {
				return err
			}
		}
		if record.Boundary != nil {
			return message(8, timestamp, &dashv2.SegmentBoundary{Timestamp: stamp, LogMonotonicNanoseconds: record.MonotonicNS, Segment: part, Kind: dashv2.SegmentBoundary_Kind(*record.Boundary)})
		}
		return nil
	})
}
