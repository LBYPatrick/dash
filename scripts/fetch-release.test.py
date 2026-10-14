import hashlib
import subprocess
import sys
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("fetch_release", Path(__file__).with_name("fetch-release.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ReleaseInstallerTests(unittest.TestCase):
    def test_default_identity_and_channel_isolation(self):
        manifest = {"protocol": 1, "version": "0.1.0+main.1.aaaaaaaa", "tag": "v0.1.0+main.1.aaaaaaaa", "channel": "main", "sequence": 1, "revision": "a" * 40}
        self.assertEqual(module.identity(manifest, "main", "arm64")["arch"], "arm64")
        with self.assertRaises(ValueError):
            module.identity(manifest, "dev", "arm64")
        with self.assertRaises(ValueError):
            module.identity(manifest, "main", "arm64", "other-tag")

    def test_release_redirects_cannot_leave_trusted_https_hosts(self):
        for address in ["http://github.com/a", "https://github.com.evil/a", "https://user@github.com/a", "https://github.com:123/a"]:
            with self.assertRaises(ValueError):
                module.require_url(address)
        module.require_url("https://release-assets.githubusercontent.com/a")

    def test_local_bundle_verifies_without_network_and_detects_tampering(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manifest = {"protocol": 1, "version": "0.1.0-local.123.aaaaaaaa.dirty", "tag": "v0.1.0-local.123.aaaaaaaa.dirty", "channel": "local", "sequence": 123, "revision": "a" * 40}
            expected = module.identity(manifest, "local", "arm64")
            name = f"dash-{manifest['tag']}-linux-arm64.tar.gz"
            binary = bytearray(64)
            binary[:6] = b"\x7fELF\x02\x01"
            binary[18:20] = (183).to_bytes(2, 'little')
            archive = root / name
            with tarfile.open(archive, "w:gz") as bundle:
                for filename, data in [("dash-backend", bytes(binary)), ("dist/index.html", b"<html>local</html>"), ("release.json", json.dumps(expected).encode())]:
                    entry = tarfile.TarInfo(filename)
                    entry.size = len(data)
                    bundle.addfile(entry, io.BytesIO(data))
            manifest["assets"] = {"arm64": {"name": name, "bytes": archive.stat().st_size, "sha256": hashlib.sha256(archive.read_bytes()).hexdigest()}}
            metadata = root / "manifest.json"
            metadata.write_text(json.dumps(manifest))
            command = [sys.executable, str(Path(module.__file__)), "--local-manifest", str(metadata), "--arch", "arm64", "--destination", str(root / "payload")]
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root / "payload/dist/index.html").read_bytes(), b"<html>local</html>")
            archive.write_bytes(archive.read_bytes() + b"tampered")
            command[-1] = str(root / "rejected")
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((root / "rejected").exists())
            with self.assertRaises(ValueError):
                module.identity(manifest, "main", "arm64")

    def test_extraction_rejects_traversal_and_links(self):
        for name, kind in [("../outside", tarfile.REGTYPE), ("dist/../../outside", tarfile.REGTYPE), ("dist/link", tarfile.SYMTYPE), ("unexpected", tarfile.REGTYPE)]:
            with tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                archive = root / "test.tar.gz"
                with tarfile.open(archive, "w:gz") as bundle:
                    entry = tarfile.TarInfo(name)
                    entry.type = kind
                    bundle.addfile(entry, io.BytesIO())
                destination = root / "payload"
                destination.mkdir()
                with self.assertRaises(ValueError):
                    module.extract(archive, destination, {})
                self.assertFalse((root / "outside").exists())


if __name__ == "__main__":
    unittest.main()
