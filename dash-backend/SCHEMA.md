# Dash telemetry and MCAP v1

Dash decodes a small subset of the cereal wire format into independent Go structures. No comma schema package is required at runtime and the MCAP schema names are `dash.v1.*`. Source recordings remain untouched.

## Supported input

The decoder streams ordinary unpacked Cap'n Proto messages from `qlog`, `.bz2`, or `.zst` logs. It recognizes Event variants `initData`, `gpsLocation`, `gpsLocationExternal`, `carState`, `carParams`, and core `selfdriveState`. The stable wire offsets were checked against sunnypilot's [log.capnp](https://github.com/sunnypilot/sunnypilot/blob/master/openpilot/cereal/log.capnp) and [opendbc car.capnp](https://github.com/commaai/opendbc/blob/master/opendbc/car/car.capnp). Unknown variants are ignored. Future incompatible layouts require an adapter change; this is not a complete cereal implementation.

Invalid events, invalid GPS fixes, nonfinite values, and out-of-range coordinates/timestamps are rejected. Samples are thinned to approximately one per second, retaining engagement transitions. `startMonotonic` records the first log event so the browser can align samples to each camera part. `distanceSamples` counts accepted consecutive GPS pairs, including stationary pairs. Zero counts are omitted. A missing count with zero distance means no usable distance measurement; it is not a measured zero. Positive legacy totals remain usable until the parked cache upgrade. This small field stays in the index summary so dashboard statistics need no sample-array fetches. Older index entries upgrade during the usual parked scan. Core `selfdriveState` enabled/active flags are display-only recorded engagement; sunnypilot MADS-specific states are not decoded. Missing or stale states stay unknown. Each decoded file is limited to 256 MiB decompressed, 16 MiB per message, and 7,200 GPS/vehicle samples; zstd has a 64 MiB decoder window budget. Oversized or corrupt input reports a metadata error rather than inventing a route. A drive metadata response is capped at 50,000 samples.

`internal/telemetry/testdata/qlog.bz2` is an independently encoded synthetic fixture generated using pycapnp and the upstream schemas. It contains a test vehicle/version, eleven valid GPS and vehicle-state samples, and an invalid GPS event. It is not captured user data. Production and tests decode it using Go; Python is not a runtime dependency.

Distance sums valid successive GPS positions within each segment, rejecting gaps over 10 seconds and implied speeds over 90 m/s. Segment boundaries are not bridged, so distance is conservative. Duration is the observed message time span, not assumed segment count times one minute. This does not identify destinations or count disengagements. Missing GPS or logs yield missing metrics, not inferred trips.

## MCAP contract

Header profile: `dash.v1`. Schema encoding: `jsonschema` (Draft 7). Message encoding: `json`. Chunks are indexed with CRCs. Original videos/logs stream as attachments named `<segment>/<filename>`; the export is not a frame-by-frame playable video MCAP. Consumers can extract attachments or visualize the telemetry channels.

| Topic / schema                                | Fields                                                | Units                                                              |
| --------------------------------------------- | ----------------------------------------------------- | ------------------------------------------------------------------ |
| `/dash/Asset` / `dash.v1.Asset`               | path, segment, bytes, attachment                      | segment integer, bytes integer; strings identify attachment        |
| `/dash/Position` / `dash.v1.Position`         | latitude, longitude, altitude, speed, time, monotonic | degrees, degrees, meters, m/s, Unix seconds, boot-relative seconds |
| `/dash/VehicleState` / `dash.v1.VehicleState` | time, speed, steeringAngle, brakePressed, gasPressed  | boot-relative seconds, m/s, degrees, booleans                      |

MCAP LogTime/PublishTime are nanoseconds. Position messages use measured GPS UTC. Vehicle-state messages map monotonic time to the first valid GPS UTC anchor in their segment; without GPS they use an estimate derived from an original file's modification time minus the observed duration. The JSON VehicleState `time` remains monotonic regardless of the MCAP timestamp. Asset message and attachment times are source file modification times, not exposure timestamps. `dash.drive` metadata states these conventions.

Every available session part contributes attachments and messages. Source files can disappear due to firmware retention; an export that cannot read a required file fails and discards its partial output. A successful file can contain attachments without telemetry when logs were unsupported or had no usable samples.
