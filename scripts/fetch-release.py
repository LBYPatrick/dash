#!/usr/bin/env python3
"""Verify CI or explicitly local payloads on the SSH client, never the device."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import tarfile
import tempfile
import urllib.parse
import urllib.request

MAX_ARCHIVE = 128 << 20
MAX_EXPANDED = 256 << 20
ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}\Z")
REPOSITORY = re.compile(r"[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_-][A-Za-z0-9_.-]*\Z")


class TrustedRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, target):
        require_url(target)
        return super().redirect_request(request, file, code, message, headers, target)


def require_url(address):
    parsed = urllib.parse.urlparse(address)
    if (parsed.scheme != "https" or parsed.username or parsed.password
            or parsed.port or parsed.hostname not in {
                "github.com", "release-assets.githubusercontent.com",
                "objects.githubusercontent.com"}):
        raise ValueError("Untrusted release URL")


def fetch(address):
    require_url(address)
    request = urllib.request.Request(address, headers={"User-Agent": "dash-installer/1"})
    return urllib.request.build_opener(TrustedRedirect).open(request, timeout=30)


def identity(manifest, channel, arch, tag=None):
    actual_tag = manifest.get("tag", "")
    sequence = manifest.get("sequence", 0)
    version = manifest.get("version", "")
    suffix = "+main." if channel == "main" else f"-{channel}."
    if (manifest.get("protocol") != 1 or manifest.get("channel") != channel
            or not isinstance(sequence, int) or sequence <= 0
            or not ID.fullmatch(actual_tag) or actual_tag != "v" + version
            or suffix + str(sequence) + "." not in version
            or not re.fullmatch(r"[a-f0-9]{40}", manifest.get("revision", ""))
            or (tag and tag != actual_tag)):
        raise ValueError("Invalid or incompatible release identity")
    expected = {key: manifest[key] for key in (
        "protocol", "version", "tag", "channel", "sequence", "revision")}
    expected["arch"] = arch
    return expected


def extract(archive, destination, expected):
    seen = set()
    expanded = 0
    # stream mode avoids loading a complete tar inventory into memory.
    with tarfile.open(archive, "r|gz") as bundle:
        for entry in bundle:
            name = entry.name.removeprefix("./").removesuffix("/")
            if name in {"", "."} and entry.isdir():
                continue
            path = PurePosixPath(name)
            if (name in seen or len(seen) >= 2048 or "\\" in name
                    or path.is_absolute() or ".." in path.parts
                    or str(path) != name):
                raise ValueError("Unsafe or duplicate archive path")
            seen.add(name)
            if not (name in {"dash-backend", "release.json", "dist"}
                    or name.startswith("dist/")):
                raise ValueError("Unexpected archive file")
            target = destination / name
            if entry.isdir():
                if name != "dist" and not name.startswith("dist/"):
                    raise ValueError("Unexpected archive directory")
                target.mkdir(parents=True, exist_ok=True)
            elif entry.isreg() and name != "dist":
                expanded += entry.size
                if entry.size < 0 or expanded > MAX_EXPANDED:
                    raise ValueError("Archive exceeds extraction budget")
                target.parent.mkdir(parents=True, exist_ok=True)
                with bundle.extractfile(entry) as source, target.open("xb") as output:
                    shutil.copyfileobj(source, output, 128 << 10)
                target.chmod(0o700 if name == "dash-backend" else 0o600)
            else:
                raise ValueError("Archive links and special files are forbidden")
    metadata = destination / "release.json"
    if metadata.stat().st_size > 16384 or json.loads(metadata.read_text()) != expected:
        raise ValueError("Archive identity mismatch")
    if not (destination / "dist/index.html").is_file():
        raise ValueError("PWA missing from release")
    with (destination / "dash-backend").open("rb") as binary:
        header = binary.read(20)
    machine = 183 if expected["arch"] == "arm64" else 62
    if (header[:6] != b"\x7fELF\x02\x01"
            or int.from_bytes(header[18:20], "little") != machine):
        raise ValueError("Incorrect Linux binary architecture")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--channel", choices=["main", "nightly", "dev"], default="main")
    parser.add_argument("--release", default="")
    parser.add_argument("--repo", default="LBYPatrick/dash")
    parser.add_argument("--arch", choices=["arm64", "amd64"], required=True)
    parser.add_argument("--destination", type=Path, required=True)
    parser.add_argument("--local-manifest", type=Path, help="Verify an explicit local source build without network")
    args = parser.parse_args()
    if not REPOSITORY.fullmatch(args.repo) or (args.release and not ID.fullmatch(args.release)):
        parser.error("Invalid repository or release tag")
    locator = args.release or f"channel-{args.channel}"
    base = f"https://github.com/{args.repo}/releases/download/"
    if args.local_manifest:
        if args.release:
            parser.error("Local source builds cannot use a pinned release")
        raw = args.local_manifest.read_bytes() if args.local_manifest.stat().st_size <= 65536 else b" " * 65537
    else:
        with fetch(base + urllib.parse.quote(locator, safe="") + "/manifest.json") as response:
            raw = response.read(65537)
    if len(raw) > 65536:
        raise ValueError("Manifest exceeds 64 KiB")
    manifest = json.loads(raw)
    expected = identity(manifest, "local" if args.local_manifest else args.channel, args.arch, args.release)
    asset = manifest["assets"][args.arch]
    if (asset.get("name") != f"dash-{expected['tag'].replace('+', '-')}-linux-{args.arch}.tar.gz"
            or not isinstance(asset.get("bytes"), int)
            or not 0 < asset["bytes"] <= MAX_ARCHIVE
            or not re.fullmatch(r"[a-f0-9]{64}", asset.get("sha256", ""))):
        raise ValueError("Invalid archive metadata")
    args.destination.parent.mkdir(parents=True, exist_ok=True)
    if args.destination.exists():
        raise ValueError("Destination already exists")
    with tempfile.TemporaryDirectory(dir=args.destination.parent) as directory:
        temporary = Path(directory)
        archive = temporary / "download.tar.gz"
        checksum = hashlib.sha256()
        size = 0
        source_archive = (args.local_manifest.parent / asset["name"]).open("rb") if args.local_manifest else fetch(base + urllib.parse.quote(expected["tag"], safe="") + "/" + asset["name"])
        with source_archive as source, archive.open("xb") as output:
            while chunk := source.read(128 << 10):
                size += len(chunk)
                if size > asset["bytes"]:
                    raise ValueError("Archive exceeds expected size")
                checksum.update(chunk)
                output.write(chunk)
        if size != asset["bytes"] or checksum.hexdigest() != asset["sha256"]:
            raise ValueError("Archive checksum mismatch")
        payload = temporary / "payload"
        payload.mkdir()
        extract(archive, payload, expected)
        payload.rename(args.destination)
    print(expected["tag"])


if __name__ == "__main__":
    main()
