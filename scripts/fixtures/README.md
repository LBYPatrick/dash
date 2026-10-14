# Native recording simulator

These are synthetic recordings, never logs captured from a car. They exercise Dash's native decoder and export path using the actual upstream Cap'n Proto definitions. The older, small `dash-backend/internal/telemetry/testdata/qlog.bz2` remains a legacy-format regression fixture.

## Source audit

Pinned on 2026-10-14:

- [sunnypilot `9bebe42f`](https://github.com/sunnypilot/sunnypilot/tree/9bebe42f735e8859421ca3cfcb2f57377bb1f954): `openpilot/cereal/{log,custom,deprecated}.capnp`, `include/c++.capnp`.
- Its exact opendbc gitlink, [`b2acec1d`](https://github.com/sunnypilot/opendbc/blob/b2acec1dc7f15cfb258aa48415168485e29cc4ff/opendbc/car/car.capnp): `car.capnp`.
- [logger.cc](https://github.com/sunnypilot/sunnypilot/blob/9bebe42f735e8859421ca3cfcb2f57377bb1f954/openpilot/system/loggerd/logger.cc): counter/random-ID route directories, zstd qlog/rlog, repeated route initData and segment sentinels.
- [loggerd.h](https://github.com/sunnypilot/sunnypilot/blob/9bebe42f735e8859421ca3cfcb2f57377bb1f954/openpilot/system/loggerd/loggerd.h): 60-second rotation, 20 fps, filenames and optional cabin recording.
- [loggerd.cc](https://github.com/sunnypilot/sunnypilot/blob/9bebe42f735e8859421ca3cfcb2f57377bb1f954/openpilot/system/loggerd/loggerd.cc): EncodeData becomes EncodeIdx in both logs; logger/encoder segment numbers can have an offset; video uses timestampEof.
- [services.py](https://github.com/sunnypilot/sunnypilot/blob/9bebe42f735e8859421ca3cfcb2f57377bb1f954/openpilot/cereal/services.py): service rates and qlog decimation.

Upstream schemas and their MIT notices live in `schemas/`. `manifest.json` records their SHA-256 digests and every generated log's digest and per-event counts.

## What it models

Each session has a full 60-second part and an 8-second final part, named `<8-hex-counter>--<10-hex-id>--<part>`. Route initData has identical original timestamps in both parts. Sentinels delimit each part exactly. UTC, monotonic clocks, GPS movement and vehicle speed remain continuous across rotation.

| Event                           | rlog             | qlog  |
| ------------------------------- | ---------------- | ----- |
| carState                        | 100 Hz           | 10 Hz |
| selfdriveState                  | 100 Hz           | 10 Hz |
| gpsLocationExternal             | 10 Hz            | 1 Hz  |
| Each stored camera EncodeIdx    | 20 Hz            | 20 Hz |
| carParams                       | every 50 seconds | same  |
| initData and start/end sentinel | per segment      | same  |

Vehicle fields include raw/filtered speed, acceleration, steering angle/torque, override and pedal flags. GPS includes altitude, accuracy estimates, velocity, source and fix validity. Engagement changes include pre-enabled, enabled, overriding and disabled states. Enabled/active flags match [selfdrived/state.py](https://github.com/sunnypilot/sunnypilot/blob/9bebe42f735e8859421ca3cfcb2f57377bb1f954/openpilot/selfdrive/selfdrived/state.py): overriding remains active. Camera indexes include presentation/encode ordering, frame IDs and SOF/EOF clocks; encoder segment numbers deliberately differ from directory numbers. Cabin recording is enabled in this scenario; real devices need not record it.

The media is intentionally small (480 pixels wide), loops the existing synthetic road illustration, disables B-frames and uses identical images for all camera views. It is a format/timing simulator, not a bandwidth, image-quality or device-performance benchmark. EncodeIdx `len` is left unspecified (zero), so it must not be used to validate bitrate. This fixture models the events Dash supports, not every firmware service, CAN traffic or MADS-specific extension.

## Reproduce

`make fixture` verifies the committed log digests, generates matching 20 fps video with FFmpeg and copies the logs into ignored `.artifacts/device/recordings`. No Python, network access or schema compiler is needed for ordinary fixture preparation or device operation. It retires the six folders from the previous synthetic simulator.

To regenerate the committed binary logs after reviewing an upstream schema change:

```sh
python3 -m venv .artifacts/fixture-venv
.artifacts/fixture-venv/bin/pip install pycapnp==2.2.4 zstandard==0.25.0
make regenerate-telemetry-fixtures PYTHON=.artifacts/fixture-venv/bin/python
make fixture
make check
```

The generator both encodes and independently reads the files through upstream schemas, checking counts and clocks. Go tests then validate those files through Dash's separate wire adapter. Updating the pin requires reviewing logger behavior, schema layouts and service rates, replacing the vendored schemas with their license notices and regenerating the manifest; do not simply change a version string.

After `make smoke` or `make sandbox-test`, inspect the downloaded MCAP with a second implementation:

```sh
.artifacts/fixture-venv/bin/pip install mcap==1.3.0 protobuf==6.33.5
.artifacts/fixture-venv/bin/python scripts/verify-mcap.py .artifacts/smoke.mcap --fixture
.artifacts/fixture-venv/bin/python scripts/verify-mcap.py .artifacts/sandbox-smoke.mcap --fixture
```

This validates CRCs, resolves types exclusively from embedded descriptors, checks the exact 68-second span and recorded event counts, and compares every video timestamp with its camera EOF index. `--extract-video DIR` writes the frame bitstreams for independent FFmpeg decoding. Omit `--fixture` to inspect other Dash v2 exports without asserting synthetic counts.
