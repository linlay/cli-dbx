#!/usr/bin/env python3
"""Generate and verify a flat, portable download catalog. Never installs drivers."""
import argparse
import datetime
import hashlib
import json
import pathlib
import re
import stat
import struct
import tarfile
import zipfile

EXTENSIONS = ('.tar.gz', '.tar.xz', '.zip', '.dmg', '.msi', '.deb', '.rpm', '.apk')
DBX_NAME = re.compile(r'^dbx-driver-(postgres|oracle|dm|sqlserver)(-odbc)?_(v[^_]+)_(darwin|linux|windows)_(amd64|arm64)(?:_snapshot-[^.]*)?\.(?:tar\.gz|zip)$')
DBX_FIELDS = ('kind', 'engine', 'adapter', 'version', 'os', 'arch', 'protocol_version',
              'executable', 'layout', 'dbx_supported')


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def safe_name(name):
    return bool(name) and not name.startswith('/') and '\\' not in name and ':' not in name and all(part not in ('', '.', '..') for part in name.split('/'))


def check_binary(body, os_name, arch):
    if os_name == 'linux':
        valid = len(body) >= 20 and body[:4] == b'\x7fELF' and body[4] == 2 and body[5] == 1
        machine = struct.unpack_from('<H', body, 18)[0] if valid else None
        valid = valid and machine == {'amd64': 62, 'arm64': 183}[arch]
    elif os_name == 'darwin':
        valid = len(body) >= 8 and body[:4] == b'\xcf\xfa\xed\xfe'
        machine = struct.unpack_from('<I', body, 4)[0] if valid else None
        valid = valid and machine == {'amd64': 0x01000007, 'arm64': 0x0100000C}[arch]
    else:
        offset = struct.unpack_from('<I', body, 60)[0] if len(body) >= 64 else len(body)
        valid = body[:2] == b'MZ' and len(body) >= offset + 6 and body[offset:offset+4] == b'PE\0\0'
        machine = struct.unpack_from('<H', body, offset + 4)[0] if valid else None
        valid = valid and machine == {'amd64': 0x8664, 'arm64': 0xAA64}[arch]
    if not valid:
        raise ValueError(f'executable header does not match {os_name}/{arch}')


def dbx_record(path):
    match = DBX_NAME.fullmatch(path.name)
    if not match:
        raise ValueError(f'unrecognized DBX filename: {path.name}')
    if path.suffix == '.zip':
        with zipfile.ZipFile(path) as archive:
            if any(stat.S_ISLNK(item.external_attr >> 16) for item in archive.infolist()):
                raise ValueError('DBX archive must not contain symbolic links')
            names = archive.namelist()
            files = {name: archive.read(name) for name in names}
    else:
        with tarfile.open(path) as archive:
            members = archive.getmembers()
            if any(not member.isfile() for member in members):
                raise ValueError(f'DBX archive must contain only regular files: {path.name}')
            names = [member.name for member in members]
            files = {member.name: archive.extractfile(member).read() for member in members}
    if len(names) != len(files) or any(not safe_name(name) for name in names):
        raise ValueError(f'unsafe or duplicate archive path: {path.name}')
    manifests = [name for name in names if name.endswith('.json')]
    if len(manifests) != 1:
        raise ValueError(f'expected one driver manifest: {path.name}')
    manifest_name = manifests[0]
    manifest = json.loads(files[manifest_name])
    engine, suffix, version, os_name, arch = match.groups()
    adapter = 'odbc' if suffix else 'native'
    if (manifest['engine'], manifest['version'], manifest['os'], manifest['arch'], manifest.get('adapter', 'native'), manifest['protocol_version']) != (engine, version, os_name, arch, adapter, 1):
        raise ValueError(f'filename and manifest disagree: {path.name}')
    directory = pathlib.PurePosixPath(manifest_name).parent
    executable = manifest['executable']
    if not safe_name(executable) or '/' in executable:
        raise ValueError('manifest executable must be a basename')
    if str(directory) == '.' and manifest_name != executable + '.json':
        raise ValueError('flat manifest filename must equal executable + .json')
    resources = manifest.get('files', {})
    if executable in resources:
        raise ValueError('resource must not overlap executable')
    hashes = {executable: manifest['sha256'], **resources}
    expected_names = {manifest_name}
    for name, expected in hashes.items():
        if not safe_name(name) or '/' in name or name == pathlib.PurePosixPath(manifest_name).name:
            raise ValueError('invalid resource filename')
        archived_name = str(directory / name)
        expected_names.add(archived_name)
        if archived_name not in files or hashlib.sha256(files[archived_name]).hexdigest() != expected:
            raise ValueError(f'file digest mismatch: {path.name}: {name}')
    if set(files) != expected_names:
        raise ValueError(f'unlisted files in DBX package: {path.name}')
    check_binary(files[str(directory / executable)], os_name, arch)
    return {'kind': 'dbx-driver', 'engine': engine, 'adapter': adapter, 'version': version,
            'os': os_name, 'arch': arch, 'protocol_version': 1, 'executable': str(directory / executable),
            'layout': 'flat' if str(directory) == '.' else 'legacy-engine-directory',
            'verification': 'manifest-and-executable-header-verified', 'dbx_supported': str(directory) == '.'}


def generate(root, records, base_url=''):
    root = root.resolve()
    extra_records = json.loads(records.read_text())
    available = {record['filename']: record for record in extra_records if record.get('filename')}
    if len(available) != sum(bool(record.get('filename')) for record in extra_records):
        raise ValueError('duplicate catalog metadata filename')
    artifacts = []
    for path in sorted(root.iterdir()):
        if path.is_dir() or path.is_symlink():
            raise ValueError(f'catalog must be flat regular files: {path.name}')
        if not path.name.endswith(EXTENSIONS) and path.name not in available:
            continue
        if path.name.startswith('dbx-driver-'):
            entry = dbx_record(path)
            overrides = available.get(path.name, {})
            for field in DBX_FIELDS:
                if field in overrides and overrides[field] != entry[field]:
                    raise ValueError(f'metadata disagrees with DBX manifest: {path.name}')
            entry.update(overrides)
        else:
            if path.name not in available:
                raise ValueError(f'missing provenance for artifact: {path.name}')
            entry = available[path.name].copy()
        actual = digest(path)
        if entry.get('sha256') and entry['sha256'] != actual:
            raise ValueError(f'provenance hash mismatch: {path.name}')
        sidecar = pathlib.Path(str(path) + '.sha256')
        if sidecar.exists() and sidecar.read_text().split()[0] != actual:
            raise ValueError(f'checksum sidecar mismatch: {path.name}')
        sidecar.write_text(actual + '  ' + path.name + '\n')
        entry.update(filename=path.name, sha256=actual, size_bytes=path.stat().st_size,
                     availability='saved', download_url=(base_url.rstrip('/') + '/' if base_url else '') + path.name)
        artifacts.append(entry)
    missing_saved = set(available) - {entry['filename'] for entry in artifacts}
    if missing_saved:
        raise ValueError(f'metadata refers to absent files: {sorted(missing_saved)}')
    unavailable = [record for record in extra_records if not record.get('filename')]
    if any('download_url' in record for record in unavailable):
        raise ValueError('unavailable items must not have download URLs')
    index = {'schema_version': 1, 'generated_date': datetime.datetime.now(datetime.timezone.utc).date().isoformat(), 'base_url': base_url,
             'artifacts': artifacts, 'unavailable': unavailable}
    (root / 'index.json').write_text(json.dumps(index, ensure_ascii=False, indent=2) + '\n')
    lines = ['# DBX 独立驱动下载入口', '',
             '此目录的压缩包按引擎、版本、操作系统和架构平铺；index.json 可供下载服务或智能体读取。', '',
             '选择 adapter=native 获取 Go 驱动；adapter=odbc 是 DBX 适配程序，还需匹配的 vendor 包。',
             '当前版本包由未提交工作树构建，来源和日期见索引。历史快照保留原始字节。',
             '`.1` 是旧引擎子目录布局，不适用于当前扁平加载规则；`.2`/`.3` 按 driver_path 精确选择。',
             '厂商原始包原样保留；随包存在的许可证一并保留，缺失情况见索引。保存文件不等于实库验收通过。',
             'OceanBase 库仅作为保存和移植材料，当前 DBX 未接入 OceanBase ODBC。',
             'Windows 厂商 ODBC 使用正式安装器注册；Linux 包按发行版、libc、OpenSSL 和 unixODBC 依赖选用。', '',
             '使用前执行 `shasum -a 256 -c SHA256SUMS`（Linux: `sha256sum -c SHA256SUMS`）；',
             'Windows 可用 `Get-FileHash -Algorithm SHA256 <文件>` 与 index.json 比对。', '',
             '详细移植、来源和平台限制见 PORTING.md 与 index.json。', '',
             '| 文件 | 类别 | 平台/架构 | 大小 MiB |', '|---|---|---|---:|']
    for entry in artifacts:
        lines.append(f"| [{entry['filename']}]({entry['filename']}) | {entry['kind']} | {entry.get('os', 'source')}/{entry.get('arch', 'all')} | {entry['size_bytes']/1024**2:.2f} |")
    if unavailable:
        lines += ['', '## 未提供的组合', '']
        for entry in unavailable:
            lines.append(f"- {entry.get('vendor', entry.get('engine', 'ODBC'))} {entry.get('os', '')}/{entry.get('arch', '')}: {entry.get('reason', entry.get('availability', 'unavailable'))}")
    (root / 'README.md').write_text('\n'.join(lines) + '\n')
    checksums = [f'{digest(path)}  {path.name}' for path in sorted(root.iterdir()) if path.name != 'SHA256SUMS']
    (root / 'SHA256SUMS').write_text('\n'.join(checksums) + '\n')
    return index


def verify(root):
    checked = set()
    for line in (root / 'SHA256SUMS').read_text().splitlines():
        expected, filename = line.split('  ', 1)
        if not safe_name(filename) or '/' in filename or filename in checked:
            raise ValueError('unsafe or duplicate checksum filename')
        path = root / filename
        if path.is_symlink() or not path.is_file() or digest(path) != expected:
            raise ValueError(f'catalog digest mismatch: {filename}')
        checked.add(filename)
    if checked != {path.name for path in root.iterdir() if path.name != 'SHA256SUMS'}:
        raise ValueError('checksum inventory does not match directory')
    index = json.loads((root / 'index.json').read_text())
    indexed = set()
    for entry in index['artifacts']:
        path = root / entry['filename']
        if entry['filename'] in indexed:
            raise ValueError('duplicate artifact index filename')
        indexed.add(entry['filename'])
        if not safe_name(entry['filename']) or '/' in entry['filename'] or digest(path) != entry['sha256'] or path.stat().st_size != entry['size_bytes']:
            raise ValueError('artifact index digest/size mismatch')
        if path.name.startswith('dbx-driver-'):
            actual = dbx_record(path)
            if any(entry.get(field) != actual[field] for field in DBX_FIELDS):
                raise ValueError(f'artifact index disagrees with DBX archive: {path.name}')
        elif entry['kind'] == 'dbx-driver':
            raise ValueError('unrecognized DBX artifact filename')
    required = {path.name for path in root.iterdir()
                if path.name.endswith(EXTENSIONS) or pathlib.Path(str(path) + '.sha256').exists()}
    records = root / 'catalog-records.json'
    if records.exists():
        required.update(record['filename'] for record in json.loads(records.read_text()) if record.get('filename'))
    if required != indexed:
        raise ValueError('artifact index inventory does not match directory/metadata')
    if any(record.get('filename') or 'download_url' in record for record in index['unavailable']):
        raise ValueError('unavailable items must not be downloadable')
    return len(index['artifacts'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('root', type=pathlib.Path)
    parser.add_argument('--records', type=pathlib.Path)
    parser.add_argument('--base-url', default='')
    parser.add_argument('--verify', action='store_true')
    args = parser.parse_args()
    if args.verify:
        print(f'verified {verify(args.root)} artifacts')
    else:
        if not args.records:
            parser.error('--records is required when generating')
        print(f"indexed {len(generate(args.root, args.records, args.base_url)['artifacts'])} artifacts")


if __name__ == '__main__':
    main()
