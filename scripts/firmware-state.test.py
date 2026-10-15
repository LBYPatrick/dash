"""Exercise installer predicates and the embedded adapter without firmware imports."""
import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

PROJECT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('live_adapter', PROJECT / 'backend/live_adapter.py')
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)


class FirmwareStateTests(unittest.TestCase):
    def test_installer_conditions_use_published_offroad_and_optional_legacy_veto(self):
        guards = []
        for file in ('scripts/install-backend.sh', 'backend/install-service.sh', 'backend/uninstall-service.sh'):
            text = (PROJECT / file).read_text()
            cursor = 0
            while True:
                start = text.find('[[ "$(cat /data/params/d/IsOffroad)', cursor)
                if start < 0:
                    break
                end = text.index(' ]]', start) + 3
                guards.append((file, text[start:end]))
                cursor = end
        self.assertEqual(len(guards), 5)
        for on, off, permitted in ((None, '1', True), ('0', '1', True), ('1', '1', False),
                                   (None, '0', False), ('0', None, False), ('bad', '1', False),
                                   (None, 'bad', False), ('0', '0', False)):
            with tempfile.TemporaryDirectory() as folder:
                root = Path(folder)
                for name, value in (('IsOnroad', on), ('IsOffroad', off)):
                    if value is not None:
                        (root / name).write_text(value)
                for file, guard in guards:
                    with self.subTest(file=file, on=on, off=off):
                        result = subprocess.run(['bash', '-c', guard.replace('/data/params/d', folder)],
                                                capture_output=True)
                        self.assertEqual(result.returncode == 0, permitted)
                self.assertEqual(adapter.firmware_offroad(folder), permitted)

    def test_adapter_errors_and_state_change_block_capture(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / 'IsOffroad').write_text('1')
            (root / 'IsOnroad').symlink_to(root / 'missing')
            self.assertFalse(adapter.firmware_offroad(folder))
            (root / 'IsOnroad').unlink()
            (root / 'IsOffroad').write_bytes(b'\xff')
            self.assertFalse(adapter.firmware_offroad(folder))
        for values in (['0', '1', '1'], ['0', '1', '0', '0']):
            with patch('builtins.open') as file:
                file.return_value.__enter__.return_value.read.side_effect = values
                self.assertFalse(adapter.firmware_offroad('/synthetic-params'))


if __name__ == '__main__':
    unittest.main()
