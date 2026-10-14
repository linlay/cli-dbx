#!/usr/bin/env python3
"""Archive existing exact-host modules; never rebuild or modify their bytes."""
import argparse
import hashlib
import json
import pathlib
import stat
import zipfile


def archive(path, files):
    with zipfile.ZipFile(path, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as z:
        for name, data, executable in sorted(files):
            info = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
            info.create_system = 3
            info.external_attr = (stat.S_IFREG | (0o755 if executable else 0o644)) << 16
            z.writestr(info, data, compress_type=zipfile.ZIP_DEFLATED, compresslevel=9)


def package(directory, goos, arch):
    root = pathlib.Path(__file__).resolve().parent.parent
    version = (root / 'VERSION').read_text().strip()
    host = directory / ('dbx.exe' if goos == 'windows' else 'dbx')
    host_bytes = host.read_bytes()
    host_sha = hashlib.sha256(host_bytes).hexdigest()
    archives = []
    path = directory / f'dbx-shared-host_{version}_{goos}_{arch}.zip'
    files = [(host.name, host_bytes, True),
             ('goloader-LICENSE', (root / 'third_party/goloader/LICENSE').read_bytes(), False),
             ('shared-host.md', (root / 'docs/shared-host.md').read_bytes(), False)]
    if (root / 'LICENSE').is_file():
        files.append(('LICENSE', (root / 'LICENSE').read_bytes(), False))
    archive(path, files)
    archives.append(path)
    connector = directory / f'builtin.dbx_{version}_{goos}_{arch}.zip'
    if connector.is_file():
        with zipfile.ZipFile(connector) as z:
            if z.read('runtime/connectors/builtin.dbx/bin/' + host.name) != host_bytes:
                raise SystemExit('connector binary differs from shared host')
        archives.append(connector)
    measurements = {'host_bytes': len(host_bytes), 'host_sha256': host_sha, 'modules': {}}
    for module in sorted(directory.glob('*.dbmod')):
        manifest = pathlib.Path(str(module) + '.json')
        metadata = json.loads(manifest.read_text())
        content = module.read_bytes()
        if metadata['host_sha256'] != host_sha or metadata['sha256'] != hashlib.sha256(content).hexdigest():
            raise SystemExit(f'module does not match host or manifest: {module.name}')
        if metadata['os'] != goos or metadata['arch'] != arch or metadata['version'] != version:
            raise SystemExit(f'module target/version mismatch: {module.name}')
        path = directory / f"dbx-module-{metadata['engine']}_{version}_{goos}_{arch}.zip"
        archive(path, [(module.name, content, False), (manifest.name, manifest.read_bytes(), False)])
        archives.append(path)
        measurements['modules'][metadata['engine']] = {'module_bytes': len(content), 'manifest_bytes': manifest.stat().st_size}
        measurements['modules'][metadata['engine']]['decoded_bytes']=metadata['decoded_bytes']
        stats=pathlib.Path(str(module)+'.stats.json')
        if stats.is_file():measurements['modules'][metadata['engine']]['prelink']=json.loads(stats.read_text())
    (directory / 'measurements.json').write_text(json.dumps(measurements, indent=2) + '\n', encoding='utf-8')
    checksum = ''.join(f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n' for p in sorted(archives))
    (directory / f'dbx-shared-host_{version}_{goos}_{arch}_checksums.txt').write_text(checksum, encoding='utf-8')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=pathlib.Path, required=True)
    parser.add_argument('--os', required=True, choices=['windows', 'darwin'])
    parser.add_argument('--arch', required=True, choices=['amd64', 'arm64'])
    args = parser.parse_args()
    package(args.directory, args.os, args.arch)
