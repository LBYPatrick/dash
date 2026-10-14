#!/usr/bin/env python3
"""Regenerate synthetic native logger fixtures using pinned upstream schemas.

Dev only: pip install pycapnp==2.2.4 zstandard==0.25.0
Normal fixture preparation and Go builds use the checked-in compressed logs.
"""
import collections
import datetime
import hashlib
import io
import json
import math
import pathlib

import capnp
import zstandard

ROOT = pathlib.Path(__file__).resolve().parent
SCHEMAS = ROOT / "schemas"
LOG = capnp.load(str(SCHEMAS / "log.capnp"), imports=[str(SCHEMAS)])
DURATIONS = [60, 8]
DRIVES = {
    "000001a3--c20ba54385": "2026-10-12--17-42-08",
    "000001a2--70f9e43b12": "2026-10-12--08-16-32",
    "000001a1--36a6f1dc08": "2026-10-11--16-05-11",
}
CAMERAS = {
    "road": "narrowRoadEncodeIdx",
    "wide_road": "wideRoadEncodeIdx",
    "driver": "cabinEncodeIdx",
    "preview": "qNarrowRoadEncodeIdx",
}
manifest = {
    "synthetic": True,
    "segmentDurationsSeconds": DURATIONS,
    "fps": 20,
    "recordFront": True,
    "recordAudio": False,
    "drives": list(DRIVES),
    "files": {},
    "schemaSha256": {},
}
for path in sorted(SCHEMAS.rglob("*.capnp")):
    manifest["schemaSha256"][str(path.relative_to(SCHEMAS))] = hashlib.sha256(
        path.read_bytes()
    ).hexdigest()

for drive in DRIVES:
    utc_ms = (
        int(
            datetime.datetime.strptime(DRIVES[drive], "%Y-%m-%d--%H-%M-%S")
            .replace(tzinfo=datetime.timezone.utc)
            .timestamp()
        )
        * 1000
    )
    boot_ns = 100_000_000_000
    init = LOG.Event.new_message(logMonoTime=boot_ns, valid=True)
    init.initData = {
        "version": "dash-synthetic-sunnypilot",
        "deviceType": "mici",
        "wallTimeNanos": utc_ms * 1_000_000,
        "gitBranch": "synthetic",
        "gitCommit": "9bebe42f735e8859421ca3cfcb2f57377bb1f954",
    }
    offset = 0
    for segment, duration in enumerate(DURATIONS):
        rlog, qlog = io.BytesIO(), io.BytesIO()
        counts = {"rlog": collections.Counter(), "qlog": collections.Counter()}

        def write(event, q=True):
            data = event.to_bytes()
            event.clear_write_flag()
            rlog.write(data)
            counts["rlog"][event.which()] += 1
            if q:
                qlog.write(data)
                counts["qlog"][event.which()] += 1

        def event(kind, time_ns, fields, q=True, valid=True):
            value = LOG.Event.new_message(logMonoTime=time_ns, valid=valid)
            value.init(kind).from_dict(fields)
            write(value, q)

        write(init)  # loggerd deliberately repeats the original route initData.
        start_ns = boot_ns + offset * 1_000_000_000
        event(
            "sentinel",
            start_ns,
            {"type": "startOfRoute" if segment == 0 else "startOfSegment"},
        )
        for tick in range(duration * 100):
            t = offset + tick / 100
            mono = start_ns + tick * 10_000_000
            speed = 8 + 2 * math.sin(t / 8)
            accel = 0.25 * math.cos(t / 8)
            gas_pressed = t % 20 < 2
            steering_pressed = 26 <= t < 29
            state = "disabled"
            if 3 <= t < 5:
                state = "preEnabled"
            elif 5 <= t < 58:
                state = "overriding" if gas_pressed or steering_pressed else "enabled"
            # Match selfdrived/state.py: overriding remains active upstream.
            active = state in ("enabled", "softDisabling", "overriding")
            enabled = state == "preEnabled" or active
            event(
                "carState",
                mono,
                {
                    "vEgo": speed,
                    "vEgoRaw": speed + 0.15,
                    "aEgo": accel,
                    "steeringAngleDeg": 4 * math.sin(t / 3),
                    "steeringTorque": 0.2 * math.sin(t),
                    "steeringPressed": steering_pressed,
                    "standstill": False,
                    "gasPressed": gas_pressed,
                    "brakePressed": t >= 64,
                },
                q=tick % 10 == 0,
            )
            event(
                "selfdriveState",
                mono,
                {
                    "state": state,
                    "enabled": enabled,
                    "active": active,
                    "engageable": True,
                },
                q=tick % 10 == 0,
            )
            if tick % 10 == 0:
                # Integrate the same speed used by carState; continuous at rotation.
                distance = 8 * t + 16 * (1 - math.cos(t / 8))
                event(
                    "gpsLocationExternal",
                    mono,
                    {
                        "flags": 1,
                        "hasFix": True,
                        "latitude": 37.77 + distance / 111_320,
                        "longitude": -122.42,
                        "altitude": 16 + 0.1 * math.sin(t / 5),
                        "speed": speed,
                        "bearingDeg": 0,
                        "unixTimestampMillis": utc_ms + round(t * 1000),
                        "horizontalAccuracy": 0.8,
                        "verticalAccuracy": 1.5,
                        "speedAccuracy": 0.1,
                        "source": "ublox",
                        "vNED": [speed, 0, 0],
                        "satelliteCount": 12,
                    },
                    q=tick % 100 == 0,
                )
            if tick % 5 == 0:
                frame = tick // 5
                for camera, kind in CAMERAS.items():
                    # Encoder EOF is the media PTS upstream; SOF is 5 ms earlier.
                    event(
                        kind,
                        mono + 8_000_000,
                        {
                            "frameId": offset * 20 + frame,
                            "encodeId": offset * 20 + frame,
                            "type": (
                                "qcameraH264" if camera == "preview" else "fullHEVC"
                            ),
                            "segmentNum": segment + 4,
                            "segmentId": frame,
                            "segmentIdEncode": frame,
                            "timestampSof": mono,
                            "timestampEof": mono + 5_000_000,
                            "flags": 8 if frame % 20 == 0 else 0,
                            "len": 0,
                        },
                    )
            # carParams is logged at 0.02 Hz, not once per camera segment.
            if round(t * 100) % 5000 == 0:
                event(
                    "carParams",
                    mono,
                    {"carFingerprint": "DASH SYNTHETIC VEHICLE", "brand": "mock"},
                )
        event(
            "sentinel",
            start_ns + duration * 1_000_000_000,
            {"type": "endOfRoute" if segment == len(DURATIONS) - 1 else "endOfSegment"},
        )
        folder = ROOT / "device" / f"{drive}--{segment}"
        folder.mkdir(parents=True, exist_ok=True)
        for kind, buffer in [("rlog", rlog), ("qlog", qlog)]:
            path = folder / f"{kind}.zst"
            path.write_bytes(
                zstandard.ZstdCompressor(level=3).compress(buffer.getvalue())
            )
            # Verify through the upstream decoder, independently of Dash's parser.
            decoded = list(
                LOG.Event.read_multiple_bytes(
                    zstandard.ZstdDecompressor().decompress(path.read_bytes())
                )
            )
            assert collections.Counter(e.which() for e in decoded) == counts[kind]
            assert decoded[0].initData.wallTimeNanos == utc_ms * 1_000_000
            assert decoded[1].logMonoTime == start_ns
            manifest["files"][str(path.relative_to(ROOT))] = {
                "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                "events": dict(counts[kind]),
            }
        offset += duration
(ROOT / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
print(
    "Generated and independently decoded six native-format segments (60 + 8 seconds per session)."
)
