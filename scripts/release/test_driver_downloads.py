import importlib.util
import json
import pathlib
import struct
import tempfile
import unittest
import zipfile
import hashlib

spec = importlib.util.spec_from_file_location('catalog', pathlib.Path(__file__).with_name('driver-downloads.py'))
catalog = importlib.util.module_from_spec(spec)
spec.loader.exec_module(catalog)


class CatalogTests(unittest.TestCase):
    def package(self, root, wrong_hash=False, wrong_manifest=False):
        name = 'dbx-driver-postgres_v0.2.0-driver-process.3_windows_arm64.zip'
        binary = bytearray(80)
        binary[:2] = b'MZ'
        struct.pack_into('<I', binary, 60, 64)
        binary[64:68] = b'PE\0\0'
        struct.pack_into('<H', binary, 68, 0xAA64)
        exe = 'dbx-driver-postgres-0.2.0-driver-process.3.exe'
        manifest = dict(engine='postgres', version='v0.2.0-driver-process.3', os='windows', arch='arm64',
                        protocol_version=1, executable=exe, sha256='0'*64 if wrong_hash else hashlib.sha256(binary).hexdigest())
        with zipfile.ZipFile(root / name, 'w') as archive:
            archive.writestr(exe, binary)
            archive.writestr('wrong.json' if wrong_manifest else exe + '.json', json.dumps(manifest))
        return root / name

    def test_verified_fields_cannot_be_overridden(self):
        for field, value in [('kind', 'vendor-installer'), ('protocol_version', 100),
                             ('executable', 'wrong.exe'), ('layout', 'legacy-engine-directory'),
                             ('dbx_supported', False)]:
            with self.subTest(field=field), tempfile.TemporaryDirectory() as temp:
                root = pathlib.Path(temp)
                package = self.package(root)
                records = root / 'records.json'
                records.write_text(json.dumps([dict(filename=package.name, **{field: value})]))
                with self.assertRaisesRegex(ValueError, 'metadata disagrees'):
                    catalog.generate(root, records)

    def test_index_must_match_archives_and_list_each_once(self):
        for change in ('duplicate', 'missing', 'arch', 'protocol_version', 'kind'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temp:
                root = pathlib.Path(temp)
                self.package(root)
                records = root / 'records.json'
                records.write_text('[]')
                index = catalog.generate(root, records)
                if change == 'duplicate':
                    index['artifacts'] *= 2
                elif change == 'missing':
                    index['artifacts'] = []
                else:
                    index['artifacts'][0][change] = 'incorrect'
                (root / 'index.json').write_text(json.dumps(index))
                sums = [f'{catalog.digest(path)}  {path.name}' for path in sorted(root.iterdir()) if path.name != 'SHA256SUMS']
                (root / 'SHA256SUMS').write_text('\n'.join(sums) + '\n')
                with self.assertRaises(ValueError):
                    catalog.verify(root)

    def test_flat_manifest_name_must_match_loader(self):
        with tempfile.TemporaryDirectory() as temp:
            with self.assertRaisesRegex(ValueError, 'manifest filename'):
                catalog.dbx_record(self.package(pathlib.Path(temp), wrong_manifest=True))

    def test_catalog_detects_tampering_and_complete_inventory(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            self.package(root)
            records = root / 'records.json'
            records.write_text('[]')
            catalog.generate(root, records)
            self.assertEqual(catalog.verify(root), 1)
            (root / 'README.md').write_text('changed')
            with self.assertRaisesRegex(ValueError, 'digest mismatch'):
                catalog.verify(root)

    def test_manifest_hash_and_target_must_agree(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            with self.assertRaisesRegex(ValueError, 'digest mismatch'):
                catalog.dbx_record(self.package(root, wrong_hash=True))
        with self.assertRaisesRegex(ValueError, 'header'):
            catalog.check_binary(b'\x7fELF' + b'\0'*60, 'linux', 'amd64')

    def test_unavailable_is_not_downloadable(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            records = root / 'records.json'
            records.write_text(json.dumps([dict(vendor='dm', os='darwin', arch='arm64', availability='not-obtained', reason='no verified vendor package')]))
            index = catalog.generate(root, records)
            self.assertEqual(index['artifacts'], [])
            self.assertNotIn('download_url', index['unavailable'][0])
            self.assertEqual(catalog.verify(root), 0)


if __name__ == '__main__':
    unittest.main()
