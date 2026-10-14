# Dash telemetry and MCAP v1

Dash decodes a small subset of the cereal wire format into independent Go structures. No comma schema package is required at runtime and the telemetry schema names are `dash.v1.*` (camera video uses `foxglove.CompressedVideo`). Source recordings remain untouched.

## Supported input

The decoder streams ordinary unpacked Cap'n Proto messages from `qlog`, `.bz2`, or `.zst` logs. It recognizes Event variants `initData`, `gpsLocation`, `gpsLocationExternal`, `carState`, `carParams`, and core `selfdriveState`. The stable wire offsets were checked against sunnypilot's [log.capnp](https://github.com/sunnypilot/sunnypilot/blob/master/openpilot/cereal/log.capnp) and [opendbc car.capnp](https://github.com/commaai/opendbc/blob/master/opendbc/car/car.capnp). Unknown variants are ignored. Future incompatible layouts require an adapter change; this is not a complete cereal implementation.

Invalid events, invalid GPS fixes, nonfinite values, and out-of-range coordinates/timestamps are rejected. Samples are thinned to approximately one per second, retaining engagement transitions. `startMonotonic` records the first log event so the browser can align samples to each camera part. `distanceSamples` counts accepted consecutive GPS pairs, including stationary pairs. Zero counts are omitted. A missing count with zero distance means no usable distance measurement; it is not a measured zero. Positive legacy totals remain usable until the parked cache upgrade. This small field stays in the index summary so dashboard statistics need no sample-array fetches. Older index entries upgrade during the usual parked scan. Core `selfdriveState` enabled/active flags are display-only recorded engagement; sunnypilot MADS-specific states are not decoded. Missing or stale states stay unknown. Each decoded file is limited to 256 MiB decompressed, 16 MiB per message, and 7,200 GPS/vehicle samples; zstd has a 64 MiB decoder window budget. Oversized or corrupt input reports a metadata error rather than inventing a route. A drive metadata response is capped at 50,000 samples.

`internal/telemetry/testdata/qlog.bz2` is an independently encoded synthetic fixture generated using pycapnp and the upstream schemas. It contains a test vehicle/version, eleven valid GPS and vehicle-state samples, and an invalid GPS event. It is not captured user data. Production and tests decode it using Go; Python is not a runtime dependency.

Distance sums valid successive GPS positions within each segment, rejecting gaps over 10 seconds and implied speeds over 90 m/s. Segment boundaries are not bridged, so distance is conservative. Duration is the observed message time span, not assumed segment count times one minute. This does not identify destinations or count disengagements. Missing GPS or logs yield missing metrics, not inferred trips.

## MCAP contract

Header profile: `dash.v1`. Schema encoding: `jsonschema` (Draft 7). Message encoding: `json`. Chunks are indexed with CRCs. Original videos/logs stream as attachments named `<segment>/<filename>`; camera recordings also appear as playable frame messages using the exact `foxglove.CompressedVideo` JSON schema name recognized by Foxglove. No comma schema package is used.

| Topic / schema                                | Fields                                                | Units                                                              |
| --------------------------------------------- | ----------------------------------------------------- | ------------------------------------------------------------------ |
| `/dash/Asset` / `dash.v1.Asset`               | path, segment, bytes, attachment                      | segment integer, bytes integer; strings identify attachment        |
| `/dash/Position` / `dash.v1.Position`         | latitude, longitude, altitude, speed, time, monotonic | degrees, degrees, meters, m/s, Unix seconds, boot-relative seconds |
| `/dash/VehicleState` / `dash.v1.VehicleState` | time, speed, steeringAngle, brakePressed, gasPressed  | boot-relative seconds, m/s, degrees, booleans                      |

MCAP LogTime/PublishTime are nanoseconds. Position messages use measured GPS UTC. Vehicle-state messages map monotonic time to the first valid GPS UTC anchor in their segment; without GPS they use an estimate derived from an original file's modification time minus the observed duration. The JSON VehicleState `time` remains monotonic regardless of the MCAP timestamp. Asset message and attachment times are source file modification times, not exposure timestamps. `dash.drive` metadata states these conventions.

### Camera video

Each available camera has a separate channel using `foxglove.CompressedVideo`:

| Source         | Topic                          |
| -------------- | ------------------------------ |
| `qcamera.ts`   | `/dash/camera/preview/video`   |
| `fcamera.hevc` | `/dash/camera/road/video`      |
| `ecamera.hevc` | `/dash/camera/wide_road/video` |
| `dcamera.hevc` | `/dash/camera/driver/video`    |

Messages contain `timestamp: {sec, nsec}`, `frame_id: dash_<camera>_optical`, base64 `data`, and `format: h264` or `h265`. Each message holds one complete Annex B access unit. H.264 IDRs include SPS/PPS; HEVC IRAP frames include VPS/SPS/PPS. Compatible streams are copied; streams advertising B-frames are converted to H.264 with B-frames disabled and presentation timing preserved (`-fps_mode passthrough`, requiring FFmpeg 5 or later), because [Foxglove does not support lookahead](https://docs.foxglove.dev/docs/sdk/schemas/compressed-video). HEVC playback depends on the viewing platform's decoder. Missing cameras do not create empty channels.

TS frame offsets use normalized presentation timestamps. Raw HEVC has no container timestamps and assumes the logger's 20 fps, matching Dash playback. The first log event is mapped to GPS UTC for a segment start estimate; without GPS, the estimate uses file modification time minus video duration. These are **not decoded camera exposure timestamps**, so precise camera/telemetry synchronization is not guaranteed. If estimates overlap across parts, the next part begins at the previous part's end to preserve decodable frame order within its channel. `dash.video.<segment>.<camera>` metadata records the original estimate, adjusted start, time basis, frame count, 20 fps assumption, and B-frame conversion flag. Missing parts retain gaps when their UTC anchors provide them.

Processing streams one camera part at a time, under the same parked-only compute lock, ignition watchdog and resource controls as MP4. FFprobe inventories are capped at 4 MiB and 12,000 packets per part; individual frames at 8 MiB; decoder parameter sets at 64 KiB each. Both FFprobe and FFmpeg use one thread and the existing 192 MiB worker RSS ceiling, 32 MiB per-allocation ceiling, and service/container resource budget. Probe timeout is 30 seconds; conversion timeout is 30 minutes per part. Video is never buffered as an entire recording. Corrupt/unsupported cameras or inconsistent frame counts fail the export and discard partial output rather than silently produce attachment-only success.

Every available session part contributes attachments and messages. Source files can disappear due to firmware retention; an export that cannot read a required file fails and discards its partial output. A successful file can contain attachments without telemetry when logs were unsupported or had no usable samples.
