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


def archive(link='sdk/lib/tool.js', filename='sdk/lib/tool.js'):
    data = io.BytesIO()
    with tarfile.open(fileobj=data, mode='w:gz') as bundle:
        entry = tarfile.TarInfo(filename)
        entry.size = 1
        entry.mode = 0o755
        bundle.addfile(entry, io.BytesIO(b'x'))
        entry = tarfile.TarInfo('sdk/bin/tool')
        entry.type = tarfile.SYMTYPE
        entry.linkname = link
        bundle.addfile(entry)
    return data.getvalue()


class HostSDKTests(unittest.TestCase):
    def test_verified_sdk_preserves_internal_tool_link(self):
        data = archive('../lib/tool.js')
        with tempfile.TemporaryDirectory() as directory, patch.object(module, 'TOOLS', Path(directory)), patch.object(module, 'fetch', return_value=io.BytesIO(data)):
            binary = module.install_sdk('https://dl.google.com/go/fixture', hashlib.sha256(data).hexdigest(), 'sdk', 'sdk')
            self.assertEqual((binary / 'tool').read_bytes(), b'x')
            self.assertTrue((binary / 'tool').is_symlink())

    def test_bad_digest_traversal_and_external_symlinks_are_rejected(self):
        for data, digest in [(archive('../lib/tool.js'), '0' * 64), (archive('/etc/passwd'), None), (archive('../lib/tool.js', '../outside'), None)]:
            with tempfile.TemporaryDirectory() as directory, patch.object(module, 'TOOLS', Path(directory)), patch.object(module, 'fetch', return_value=io.BytesIO(data)):
                with self.assertRaises(ValueError):
                    module.install_sdk('https://dl.google.com/go/fixture', digest or hashlib.sha256(data).hexdigest(), 'sdk', 'sdk')
                self.assertFalse((Path(directory) / 'sdk').exists())

    def test_sdk_hosts_are_explicit_https_only(self):
        for url in ['http://go.dev/a', 'https://go.dev.evil/a', 'https://user@go.dev/a', 'https://go.dev:123/a']:
            with self.assertRaises(ValueError):
                module.trusted_url(url)
        module.trusted_url('https://dl.google.com/go/sdk.tar.gz')

    def test_verified_native_viteplus_cli_is_extracted_without_other_paths(self):
        data = io.BytesIO()
        with tarfile.open(fileobj=data, mode='w:gz') as bundle:
            entry = tarfile.TarInfo('vp')
            entry.size = 3
            bundle.addfile(entry, io.BytesIO(b'cli'))
            # Only the named CLI is extracted, even from a hostile archive.
            entry = tarfile.TarInfo('../outside')
            entry.size = 1
            bundle.addfile(entry, io.BytesIO(b'x'))
        content = data.getvalue()
        with tempfile.TemporaryDirectory() as directory, patch.object(module, 'TOOLS', Path(directory)), patch.object(module, 'fetch', return_value=io.BytesIO(content)):
            binary = module.install_viteplus_cli('https://github.com/voidzero-dev/vite-plus/fixture', hashlib.sha256(content).hexdigest())
            self.assertEqual(binary.read_bytes(), b'cli')
            self.assertEqual(binary.stat().st_mode & 0o777, 0o755)
            self.assertFalse((Path(directory) / 'outside').exists())

    def test_native_cli_symlink_and_invalid_checksum_are_rejected(self):
        data = io.BytesIO()
        with tarfile.open(fileobj=data, mode='w:gz') as bundle:
            entry = tarfile.TarInfo('vp')
            entry.type = tarfile.SYMTYPE
            entry.linkname = '/etc/passwd'
            bundle.addfile(entry)
        content = data.getvalue()
        for digest in ('0' * 64, hashlib.sha256(content).hexdigest()):
            with tempfile.TemporaryDirectory() as directory, patch.object(module, 'TOOLS', Path(directory)), patch.object(module, 'fetch', return_value=io.BytesIO(content)):
                with self.assertRaises(ValueError):
                    module.install_viteplus_cli('https://github.com/voidzero-dev/vite-plus/fixture', digest)
                self.assertFalse((Path(directory) / 'vite-plus/bin/vp').exists())


if __name__ == '__main__':
    unittest.main()
