# Dash telemetry and MCAP v2

Dash streams a supported subset of sunnypilot's unpacked Cap'n Proto recordings into its own structures. No comma schema package or Python is needed on the device. Original files remain untouched and are retained as MCAP attachments. See [the pinned upstream audit and native fixtures](../scripts/fixtures/README.md).

## Recording input and display summaries

Both modern `rlog.zst`/`qlog.zst` and older uncompressed/`.bz2` logs are supported. Indexing prefers the smaller qlog; if absent it uses rlog. The UI summary thins GPS/vehicle samples to roughly one per second and retains engagement transitions. Export reads the original rlog at its recorded rate, falling back to qlog only when rlog is absent. It never merges the two or exports the thinned dashboard arrays.

Supported events: initData identity and wall clock; carParams fingerprint; gpsLocation/gpsLocationExternal; carState; core selfdriveState; narrowRoad/cabin/wideRoad/qNarrowRoad EncodeIdx; sentinel. The camera wire tags also cover older road/driver/qRoad names. Unknown event variants are preserved in the original log attachment. This is not a complete cereal converter; sunnypilot MADS-specific states, CAN, perception and planning messages are not currently decoded.

Invalid events/fixes, nonfinite required measurements and invalid coordinates/UTC are rejected. Optional nonfinite fields are omitted. `startMonotonic` excludes the route initData repeated in later parts; start/end sentinels determine the normal segment span. `startUTC` maps the capture start to measured GPS UTC, or the recorded initData wall clock when GPS is unavailable. The UI uses it to date modern route IDs without relying on copy dates. Without a recorded clock it continues to disclose file-date fallback.

Distance sums accepted consecutive GPS points within each part, ignoring gaps over ten seconds and implied speeds over 90 m/s. `distanceSamples` distinguishes a measured stationary distance from missing measurement. No GPS route is fabricated. Index entries upgrade during a parked scan.

## Binary MCAP contract

Profile **`dash.v2`**, schema encoding **`protobuf`**, message encoding **`protobuf`**. This replaces the JSON v1 contract for newly generated exports; existing downloads remain unchanged. Chunks are indexed with CRCs. Each schema contains a serialized `FileDescriptorSet`, including all imports, following the [MCAP Protobuf registry](https://mcap.dev/spec/registry). Readers need no external schema package.

Dash definitions: [`schemas/dash/v2/telemetry.proto`](schemas/dash/v2/telemetry.proto). Official Foxglove definitions: [`schemas/foxglove`](schemas/foxglove), pinned to [foxglove-sdk `8af1390b`](https://github.com/foxglove/foxglove-sdk/tree/8af1390b71ae4ec3b52243bae0d89859391091d1/schemas/proto/foxglove), with its MIT notice. Checked-in generated Go types are used by normal builds. `make generate-mcap-schemas` regenerates them with development-only protoc and a protoc-gen-go version matched to go.mod.

| Topic                         | Schema                     | Meaning                                                                                                                          |
| ----------------------------- | -------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| `/dash/Asset`                 | `dash.v2.Asset`            | File inventory, exact byte count and original modification ns                                                                    |
| `/dash/Position`              | `foxglove.LocationFix`     | Valid GNSS latitude/longitude (degrees), ellipsoid altitude (m), measured GPS UTC; recognized by Foxglove's map                  |
| `/dash/GpsState`              | `dash.v2.GpsState`         | Source event/sensor, measured UTC milliseconds, source monotonic ns, speed m/s, bearing degrees and available accuracy estimates |
| `/dash/VehicleState`          | `dash.v2.VehicleState`     | Recorded speed m/s, acceleration m/s², steering degrees/torque, raw speed and pedal/override flags                               |
| `/dash/Engagement`            | `dash.v2.Engagement`       | Core recorded enabled/active/state; no inferred MADS engagement                                                                  |
| `/dash/CameraFrameIndex`      | `dash.v2.CameraFrameIndex` | Camera, frame IDs, encoder/directory segment numbers, ordering, SOF/EOF ns, flags and encoded bytes                              |
| `/dash/SessionInfo`           | `dash.v2.SessionInfo`      | Recorded firmware/device/wall clock or car fingerprint, with source identity                                                     |
| `/dash/SegmentBoundary`       | `dash.v2.SegmentBoundary`  | Typed start/end segment/route sentinel                                                                                           |
| `/dash/camera/<camera>/video` | `foxglove.CompressedVideo` | One complete compressed video frame, with native bytes and Protobuf timestamp                                                    |

Optional numeric fields are omitted when absent from the source wire layout or nonfinite. Cap'n Proto scalar defaults cannot distinguish an unpopulated value from an intentional zero. Vehicle steering torque retains source units, which depend on the vehicle. GPS accuracy estimates are not converted into an invented ENU covariance matrix; LocationFix covariance remains UNKNOWN. GPS sources are identified individually, not silently merged. Empty schema channels are permitted when that event was not recorded.

`LogTime`/`PublishTime` are Unix nanoseconds. GPS fixes retain each measured GPS UTC, including real gaps. Other events map their exact monotonic nanoseconds to the first valid GPS UTC anchor in their part, or the session's GPS clock. A clock prepass runs before emitting telemetry, so messages before the first fix align correctly even without an index. Without GPS, recorded initData wallTimeNanos anchors the session; without either recorded clock, a single session-wide file-time estimate and observed durations are used. Unknown/missing parts assume 60 seconds. These estimates are disclosed in `dash.telemetry.<part>` and video metadata; a device wall clock is not guaranteed to be GPS-correct.

Repeated initData identity is emitted at its current part's start and retains its original route monotonic/wall timestamps in typed fields. Asset and attachment LogTime use capture start. File modification times are retained only as attachment CreateTime and Asset.modified_time_nanoseconds, so copying files cannot stretch playback to another day.

## Camera frames

`qcamera.ts` → `preview`; `fcamera.hevc` → `road`; `ecamera.hevc` → `wide_road`; `dcamera.hevc` → `driver`. Missing cameras create no video channel. Each message uses `frame_id: dash_<camera>_optical`, a Protobuf timestamp, binary data and `h264` format. No JSON or base64 video is generated.

When EncodeIdx exists, frames are matched by presentation order and timestamped with recorded `timestampEof`, matching the logger's media PTS. This preserves dropped-frame timing gaps and per-camera offsets. Encoder segment numbers are not assumed to match directory numbers. Duplicate, incomplete, nonmonotonic or overlapping recorded indexes fail the export rather than claiming guessed synchronization. The first recorded frame can have a nonzero presentation index because loggerd waits for a keyframe.

Without a camera index, TS uses normalized presentation timestamps; raw HEVC uses the logger's 20 fps assumption. All cameras share the same part clock. Interrupted logs also trigger a bounded decoder pass. Decoded frame packet positions select the surviving capture timestamps, so missing interior frames leave timing gaps rather than shifting later frames earlier; packets beyond a known truncated tail are excluded. Recovery counts are recorded in video metadata. Estimated overlaps are adjusted to preserve frame order and disclosed. This fallback cannot recover true exposure timing.

Every video message is an independently decodable H.264 IDR with its own SPS/PPS, in Annex B format. MCAP export converts all camera feeds with no B-frames, a closed GOP and `MCAPKeyframeIntervalFrames = 1` (one keyframe per frame; 50 ms at 20 fps). This allows paused Foxglove seeks to decode the selected message without replaying preceding delta frames and avoids platform-dependent HEVC support. Recorded frame timing, resolution and frame count are preserved. Conversion takes longer and produces larger files than copying the original GOPs; unchanged original files are also retained as attachments. Existing downloaded MCAP files must be exported again.

## Resource and failure behavior

Everything runs under the existing single parked-only heavy-work lock, ignition watchdog and process/cgroup limits. Driving or unknown state refuses/cancels work. Log reads are streamed and context-cancellable, limited to 256 MiB decompressed and 16 MiB per message; zstd uses one decoder thread with a 64 MiB window budget. Display summaries allow 7,200 samples per category per part and 50,000 samples per interactive session response. Exports stream raw events without that display thinning or whole-session buffering.

Only one part's camera indexes are retained: at most 12,000 frames per camera. FFprobe output is capped at 4 MiB; a frame at 8 MiB; parameter sets at 64 KiB. One media job runs at a time with the fixed `MediaThreads` budget (8). Probes use one thread. Device mode does not inspect or claim kernel enforcement of memory controllers; the service requests memory controls and the supervisor uses the fixed Go heap target. Packet probe timeout is 30 seconds, interrupted-stream decoder inventory timeout is 5 minutes, and conversion timeout is 30 minutes per part. Output is bounded to 8 GiB. Corruption, missing source files, incompatible media or inconsistent frame counts fail the job and discard the partial export. Every available part is included; unsupported original events remain recoverable from attachments.
