"""Offline fixtures for the host-only official SDK extraction path."""
import hashlib
import importlib.util
import io
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('setup_toolchains', Path(__file__).with_name('setup-toolchains.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def archive(link='node/lib/npm.js', filename='node/lib/npm.js'):
    data = io.BytesIO()
    with tarfile.open(fileobj=data, mode='w:gz') as bundle:
        entry = tarfile.TarInfo(filename)
        entry.size = 1
        entry.mode = 0o755
        bundle.addfile(entry, io.BytesIO(b'x'))
        entry = tarfile.TarInfo('node/bin/npm')
        entry.type = tarfile.SYMTYPE
        entry.linkname = link
        bundle.addfile(entry)
    return data.getvalue()


class HostSDKTests(unittest.TestCase):
    def test_verified_sdk_preserves_internal_npm_link(self):
        data = archive('../lib/npm.js')
        with tempfile.TemporaryDirectory() as directory, patch.object(module, 'TOOLS', Path(directory)), patch.object(module, 'fetch', return_value=io.BytesIO(data)):
            binary = module.install_sdk('https://nodejs.org/fixture', hashlib.sha256(data).hexdigest(), 'node', 'node')
            self.assertEqual((binary / 'npm').read_bytes(), b'x')
            self.assertTrue((binary / 'npm').is_symlink())

    def test_bad_digest_traversal_and_external_symlinks_are_rejected(self):
        for data, digest in [(archive('../lib/npm.js'), '0' * 64), (archive('/etc/passwd'), None), (archive('../lib/npm.js', '../outside'), None)]:
            with tempfile.TemporaryDirectory() as directory, patch.object(module, 'TOOLS', Path(directory)), patch.object(module, 'fetch', return_value=io.BytesIO(data)):
                with self.assertRaises(ValueError):
                    module.install_sdk('https://nodejs.org/fixture', digest or hashlib.sha256(data).hexdigest(), 'node', 'node')
                self.assertFalse((Path(directory) / 'node').exists())

    def test_sdk_hosts_are_explicit_https_only(self):
        for url in ['http://nodejs.org/a', 'https://go.dev.evil/a', 'https://user@go.dev/a', 'https://go.dev:123/a']:
            with self.assertRaises(ValueError):
                module.trusted_url(url)
        module.trusted_url('https://dl.google.com/go/sdk.tar.gz')


if __name__ == '__main__':
    unittest.main()
