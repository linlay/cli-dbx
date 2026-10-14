#!/usr/bin/env python3
import importlib.util
import json
import os
import pathlib
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile

spec = importlib.util.spec_from_file_location('packaging', pathlib.Path(__file__).with_name('reproducible-package.py'))
packaging = importlib.util.module_from_spec(spec)
spec.loader.exec_module(packaging)


class PackagingTests(unittest.TestCase):
    def test_explicit_executables_ignore_host_permissions_and_preserve_reproducibility(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            for ext in ['tar.gz', 'zip']:
                outputs = []
                for i in [1, 2]:
                    stage = root / f'explicit-{ext}-{i}'
                    (stage / 'tools').mkdir(parents=True)
                    for name, payload in [('dbx', b'CLI'), ('tools/helper', b'helper'), ('README.md', b'document')]:
                        path = stage / name
                        path.write_bytes(payload)
                        path.chmod(0o644)
                        os.utime(path, (i * 1000, i * 1000))
                    output = root / f'explicit-output-{i}.{ext}'
                    if i == 1:
                        packaging.pack(stage, output, executables=['dbx', pathlib.Path('tools/helper')])
                    else:
                        result = subprocess.run([sys.executable, '-B', packaging.__file__,
                                                 '--stage', str(stage), '--output', str(output),
                                                 '--executable', 'dbx', '--executable', r'tools\helper'],
                                                capture_output=True, text=True)
                        self.assertEqual(result.returncode, 0, result.stderr)
                    outputs.append(output.read_bytes())
                    if ext == 'tar.gz':
                        with tarfile.open(output, 'r:gz') as archive:
                            modes = {name: archive.getmember(name).mode for name in ['dbx', 'tools/helper', 'README.md']}
                    else:
                        with zipfile.ZipFile(output) as archive:
                            modes = {name: (archive.getinfo(name).external_attr >> 16) & 0o777
                                     for name in ['dbx', 'tools/helper', 'README.md']}
                    self.assertEqual(modes, {'dbx': 0o755, 'tools/helper': 0o755, 'README.md': 0o644})
                self.assertEqual(*outputs)

    def test_invalid_executables_are_rejected_before_creating_output(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            stage = root / 'stage'
            stage.mkdir()
            (stage / 'dbx').write_bytes(b'CLI')
            (stage / 'directory').mkdir()
            names = ['', '.', '..', '../dbx', r'..\dbx', 'missing', 'directory',
                     str(stage / 'dbx'), r'C:\dbx', 'C:dbx', r'\\server\share\dbx', r'\dbx']
            for ext in ['tar.gz', 'zip']:
                for i, name in enumerate(names):
                    with self.subTest(ext=ext, name=name):
                        output = root / f'rejected-{ext}-{i}' / f'archive.{ext}'
                        with self.assertRaises(ValueError):
                            packaging.pack(stage, output, executables=[name])
                        self.assertFalse(output.parent.exists())

    @unittest.skipIf(os.name == 'nt', 'symlink creation requires Windows privileges')
    def test_explicit_symlinks_are_rejected_and_default_symlinks_are_preserved(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            stage = root / 'stage'
            stage.mkdir()
            (stage / 'dbx').write_bytes(b'CLI')
            (stage / 'internal-link').symlink_to('dbx')
            outside = root / 'outside'
            outside.mkdir()
            (outside / 'dbx').write_bytes(b'outside')
            (stage / 'external-link').symlink_to(outside / 'dbx')
            (stage / 'external-directory').symlink_to(outside, target_is_directory=True)
            for ext in ['tar.gz', 'zip']:
                for i, name in enumerate(['internal-link', 'external-link', 'external-directory/dbx']):
                    with self.subTest(ext=ext, name=name):
                        output = root / f'link-rejected-{ext}-{i}' / f'archive.{ext}'
                        with self.assertRaises(ValueError):
                            packaging.pack(stage, output, executables=[name])
                        self.assertFalse(output.parent.exists())
                output = root / f'default-links.{ext}'
                packaging.pack(stage, output)
                if ext == 'tar.gz':
                    with tarfile.open(output, 'r:gz') as archive:
                        link = archive.getmember('internal-link')
                        self.assertTrue(link.issym())
                        self.assertEqual(link.linkname, 'dbx')
                else:
                    with zipfile.ZipFile(output) as archive:
                        link = archive.getinfo('internal-link')
                        self.assertEqual(stat.S_IFMT(link.external_attr >> 16), stat.S_IFLNK)
                        self.assertEqual(archive.read('internal-link'), b'dbx')

    def test_different_paths_and_mtimes_have_identical_archives(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            for ext in ['tar.gz', 'zip']:
                outputs = []
                for i in [1, 2]:
                    stage = root / f'{ext}-{i}'
                    stage.mkdir()
                    (stage / 'executable').write_bytes(b'payload')
                    (stage / 'executable').chmod(0o755)
                    os.utime(stage / 'executable', (i * 1000, i * 1000))
                    (stage / 'empty').mkdir()
                    if os.name != 'nt':
                        (stage / 'link').symlink_to('executable')
                    output = root / f'output-{i}.{ext}'
                    packaging.pack(stage, output)
                    outputs.append(output.read_bytes())
                self.assertEqual(*outputs)

    def test_sidecar_retains_dependency_facts_and_normalizes_generated_identity(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            results = []
            for i in [1, 2]:
                source = root / str(i)
                stage = source / 'stage'
                stage.mkdir(parents=True)
                (stage / 'cargo-metadata.json').write_text(json.dumps({'workspace_root': str(source), 'packages': [{'name':'lancedb','version':'0.30.0','license':'Apache-2.0','manifest_path': str(source / 'Cargo.toml')}]}))
                (stage / 'sbom.cdx.json').write_text(json.dumps({'serialNumber':str(i),'metadata':{'timestamp':str(i),'component':{'bom-ref':str(i),'name':'engine','version':'sha256:same'}},'dependencies':[{'ref':str(i),'dependsOn':['dependency']}],'components':[{'name':'dependency','license':'MIT'}]}))
                packaging.normalize_sidecar(stage, source, source / 'target')
                results.append([(stage / n).read_bytes() for n in ['cargo-metadata.json','sbom.cdx.json']])
            self.assertEqual(*results)
            self.assertIn(b'Apache-2.0', results[0][0])
            self.assertIn(b'dependency', results[0][1])
            self.assertIn(b'urn:uuid:', results[0][1])
            sbom = json.loads(results[0][1])
            self.assertEqual(sbom['metadata']['component']['bom-ref'], sbom['dependencies'][0]['ref'])


if __name__ == '__main__':
    unittest.main()
