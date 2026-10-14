#!/usr/bin/env python3
"""Inspect MCAP with the independent Python reader and embedded Protobuf types.

Dev only: pip install mcap==1.3.0 protobuf==6.33.5
Use --fixture to validate the native 68-second simulator session's exact rates.
"""
import argparse
import collections
import pathlib

from google.protobuf import descriptor_pb2, descriptor_pool, message_factory
from mcap.reader import make_reader

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("file", type=pathlib.Path)
parser.add_argument("--fixture", action="store_true")
parser.add_argument("--extract-video", type=pathlib.Path)
args = parser.parse_args()
counts, types, classes = collections.Counter(), {}, {}
video_times, index_times = collections.defaultdict(list), collections.defaultdict(list)
gps_offset = None
outputs = {}
with args.file.open("rb") as source:
    reader = make_reader(source, validate_crcs=True)
    assert reader.get_header().profile == "dash.v2"
    summary = reader.get_summary()
    for schema, channel, message in reader.iter_messages():
        assert schema.encoding == channel.message_encoding == "protobuf"
        if schema.id not in classes:
            descriptors = descriptor_pb2.FileDescriptorSet.FromString(schema.data)
            pool = descriptor_pool.DescriptorPool()
            for descriptor in descriptors.file:
                pool.Add(descriptor)
            classes[schema.id] = message_factory.GetMessageClass(
                pool.FindMessageTypeByName(schema.name)
            )
        value = classes[schema.id].FromString(message.data)
        counts[channel.topic] += 1
        types[channel.topic] = schema.name
        assert message.log_time == message.publish_time
        if "timestamp" in value.DESCRIPTOR.fields_by_name:
            assert value.HasField("timestamp")
            assert (
                value.timestamp.seconds * 1_000_000_000 + value.timestamp.nanos
                == message.log_time
            )
        if schema.name == "dash.v2.GpsState" and gps_offset is None:
            gps_offset = (
                value.unix_timestamp_millis * 1_000_000
                - value.log_monotonic_nanoseconds
            )
        if args.fixture and schema.name == "dash.v2.Engagement":
            assert value.active == (value.state in (2, 3, 4))
            assert value.enabled == (value.state in (1, 2, 3, 4))
        if schema.name == "dash.v2.CameraFrameIndex":
            index_times[value.camera].append(value.end_of_frame_nanoseconds)
        if schema.name == "foxglove.CompressedVideo":
            assert value.format in ("h264", "h265") and value.data
            camera = channel.topic.split("/")[-2]
            video_times[camera].append(message.log_time)
            if args.extract_video:
                args.extract_video.mkdir(parents=True, exist_ok=True)
                if camera not in outputs:
                    outputs[camera] = (
                        args.extract_video / f"{camera}.{value.format}"
                    ).open("wb")
                outputs[camera].write(value.data)
    span = (
        summary.statistics.message_end_time - summary.statistics.message_start_time
    ) / 1e9
for output in outputs.values():
    output.close()
if args.fixture:
    assert span == 68, span
    expected = {
        "VehicleState": 6800,
        "Engagement": 6800,
        "Position": 680,
        "GpsState": 680,
        "CameraFrameIndex": 5440,
        "SegmentBoundary": 4,
        "SessionInfo": 4,
        "Asset": 12,
    }
    for topic, count in expected.items():
        assert counts[f"/dash/{topic}"] == count, (
            topic,
            counts[f"/dash/{topic}"],
            count,
        )
    assert len(summary.attachment_indexes) == 12
    assert gps_offset is not None
    for camera in ("preview", "road", "wide_road", "driver"):
        assert counts[f"/dash/camera/{camera}/video"] == 1360
        assert video_times[camera] == [
            time + gps_offset for time in index_times[camera]
        ]
        assert all(
            b - a == 50_000_000
            for a, b in zip(video_times[camera], video_times[camera][1:])
        )
print(
    f"{args.file}: {span:g} seconds, {sum(counts.values())} binary messages, CRCs and embedded descriptors verified"
)
for topic in sorted(counts):
    print(f"  {topic}: {counts[topic]} ({types[topic]})")
