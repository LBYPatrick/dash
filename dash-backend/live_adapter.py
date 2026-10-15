"""Dash-owned parked camera adapter. Uses firmware-installed libraries only.

One receive-only WebRTC viewer, one bounded raw camera track. No firmware file
or parameter writes. The Go parent owns the process group and browser lease;
this worker independently stops on ignition, unknown panda state, or parent death.
"""
import asyncio
import ctypes
import fractions
import json
import os
import signal
import subprocess
import sys
import time


def parent_death():
    # Linux PR_SET_PDEATHSIG, including the race where the parent already died.
    parent = os.getppid()
    if ctypes.CDLL(None).prctl(1, signal.SIGKILL) != 0 or os.getppid() != parent or parent == 1:
        os._exit(1)


async def run(config):
    parent_death()
    sys.path.insert(0, config["firmware"])
    os.environ["PYTHONDONTWRITEBYTECODE"] = "1"
    import av
    import numpy as np
    from aiortc import RTCPeerConnection, RTCSessionDescription, RTCConfiguration, RTCRtpSender, VideoStreamTrack
    from aiortc.codecs.h264 import H264Encoder
    import aiortc.rtcrtpsender as rtp_sender
    import aioice.ice
    # Advertise only the LAN address that accepted this browser. No interface
    # enumeration, netlink socket, STUN server, or external network is needed.
    aioice.ice.get_host_addresses = lambda use_ipv4, use_ipv6: [config["address"]]
    from cereal import messaging
    from msgq.visionipc import VisionIpcClient, VisionStreamType

    done = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, done.set)
    # Closing the parent's pipe is also an immediate stop, independently of HTTP.
    loop.add_reader(sys.stdin.fileno(), done.set)
    sm = messaging.SubMaster(["pandaStates"])
    last_panda = 0.0

    def parked():
        nonlocal last_panda
        try:
            with open(os.path.join(config["params"], "IsOnroad")) as f:
                if f.read().strip() != "0":
                    return False
            with open(os.path.join(config["params"], "IsOffroad")) as f:
                if f.read().strip() != "1":
                    return False
            sm.update(0)
            if sm.updated["pandaStates"]:
                last_panda = time.monotonic()
            pandas = sm["pandaStates"]
            return (sm.valid["pandaStates"] and bool(pandas) and time.monotonic() - last_panda < 2.0
                    and not any(p.ignitionLine or p.ignitionCan for p in pandas))
        except Exception:
            return False

    # Do not touch cameras until a fresh, parked ignition message is available.
    deadline = time.monotonic() + 4
    while not parked():
        if time.monotonic() > deadline:
            raise RuntimeError("Live camera requires fresh parked ignition data")
        await asyncio.sleep(0.1)

    class BoundedH264(H264Encoder):
        def _encode_frame(self, frame, force_keyframe):
            if self.codec is None:
                self.codec = av.CodecContext.create("libx264", "w")
                self.codec.width, self.codec.height = frame.width, frame.height
                self.codec.pix_fmt = "yuv420p"
                self.codec.bit_rate = 800_000
                self.codec.thread_count = config["threads"]
                self.codec.framerate = fractions.Fraction(config["fps"], 1)
                self.codec.time_base = fractions.Fraction(1, 90_000)
                self.codec.options = {"preset": "ultrafast", "tune": "zerolatency", "level": "31"}
                self.codec.profile = "Baseline"
            frame.pict_type = av.video.frame.PictureType.I if force_keyframe else av.video.frame.PictureType.NONE
            data = b"".join(bytes(packet) for packet in self.codec.encode(frame))
            if data:
                yield from self._split_bitstream(data)

    original_encoder = rtp_sender.get_encoder
    rtp_sender.get_encoder = lambda codec: BoundedH264() if codec.mimeType.lower() == "video/h264" else original_encoder(codec)
    camera_type = {"road": VisionStreamType.VISION_STREAM_ROAD,
                   "wideRoad": VisionStreamType.VISION_STREAM_WIDE_ROAD,
                   "driver": VisionStreamType.VISION_STREAM_DRIVER}[config["camera"]]
    client = VisionIpcClient("camerad", camera_type, True)

    # Never attach to or stop a camera owned by the firmware or another viewer.
    existing = subprocess.run(["pgrep", "-x", "camerad"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if existing.returncode == 0:
        raise RuntimeError("Device camera is already in use; close its preview and retry")
    camera = None
    peer = RTCPeerConnection(RTCConfiguration(iceServers=[]))
    try:
        await peer.setRemoteDescription(RTCSessionDescription(sdp=config["sdp"], type="offer"))
        camera = subprocess.Popen([os.path.join(config["firmware"], "system/camerad/camerad")],
                                  cwd=config["firmware"], stdin=subprocess.DEVNULL,
                                  stdout=sys.stderr, stderr=sys.stderr, preexec_fn=parent_death)
        print("Dash camera started pid=%d" % camera.pid, file=sys.stderr, flush=True)

        async def safety():
            while not done.is_set():
                if not parked() or camera.poll() is not None:
                    print("Dash live stopped: ignition, unknown state or camera exit", file=sys.stderr, flush=True)
                    done.set()
                    break
                await asyncio.sleep(0.1)
        safety_task = asyncio.create_task(safety())
        deadline = time.monotonic() + 20
        while not client.connect(False):
            if done.is_set() or time.monotonic() > deadline:
                raise RuntimeError("Camera did not become ready while parked")
            await asyncio.sleep(0.1)

        class CameraTrack(VideoStreamTrack):
            def __init__(self):
                super().__init__()
                self.start_time = time.monotonic()
                self.next_frame = self.start_time

            async def recv(self):
                await asyncio.sleep(max(0, self.next_frame - time.monotonic()))
                self.next_frame = time.monotonic() + 1 / config["fps"]
                while not done.is_set():
                    buf = await asyncio.to_thread(client.recv, 100)
                    if buf is not None:
                        break
                else:
                    raise asyncio.CancelledError()
                # Copy only visible NV12 rows; native DMA buffers contain aligned
                # padding between the planes. Never expose stale padding pixels.
                raw = np.asarray(buf.data, dtype=np.uint8)
                image = np.empty((buf.height * 3 // 2, buf.width), dtype=np.uint8)
                image[:buf.height] = raw[:buf.uv_offset].reshape((-1, buf.stride))[:buf.height, :buf.width]
                uv = raw[buf.uv_offset:buf.uv_offset + buf.stride * (buf.height // 2)]
                image[buf.height:] = uv.reshape((-1, buf.stride))[:, :buf.width]
                frame = av.VideoFrame.from_ndarray(image, format="nv12")
                scale = min(config["width"] / buf.width, config["height"] / buf.height, 1)
                frame = frame.reformat(width=int(buf.width * scale) // 2 * 2,
                                       height=int(buf.height * scale) // 2 * 2, format="yuv420p")
                frame.pts = int((time.monotonic() - self.start_time) * 90_000)
                frame.time_base = fractions.Fraction(1, 90_000)
                return frame

        @peer.on("connectionstatechange")
        async def connection_changed():
            if peer.connectionState in ("failed", "closed", "disconnected"):
                done.set()

        sender = peer.addTrack(CameraTrack())
        transceiver = next(t for t in peer.getTransceivers() if t.sender is sender)
        transceiver.setCodecPreferences([c for c in RTCRtpSender.getCapabilities("video").codecs if c.mimeType.lower() == "video/h264"])
        await peer.setLocalDescription(await peer.createAnswer())
        print(json.dumps({"type": "answer", "sdp": peer.localDescription.sdp}), flush=True)
        await done.wait()
    finally:
        # Stop capture before waiting for WebRTC / executor cleanup.
        if camera is not None and camera.poll() is None:
            camera.terminate()
            try:
                await asyncio.wait_for(asyncio.to_thread(camera.wait), 1)
            except asyncio.TimeoutError:
                camera.kill()
                await asyncio.to_thread(camera.wait)
        done.set()
        if "safety_task" in locals():
            safety_task.cancel()
        await peer.close()
        print("Dash camera and WebRTC stopped", file=sys.stderr, flush=True)


if __name__ == "__main__":
    # Read exactly one line without a buffered reader stealing pipe-EOF events.
    raw = bytearray()
    while len(raw) <= 120_000:
        byte = os.read(0, 1)
        if not byte or byte == b"\n":
            break
        raw.extend(byte)
    try:
        asyncio.run(run(json.loads(raw)))
    except Exception as error:
        print("Dash live adapter: " + str(error), file=sys.stderr, flush=True)
        sys.exit(1)
