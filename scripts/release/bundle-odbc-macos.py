#!/usr/bin/env python3
"""Relocate package copies of unixODBC into versioned, flat companion files."""
import argparse
import pathlib
import shutil
import subprocess


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def dependencies(path, library=False):
    lines = run('otool', '-L', str(path)).splitlines()[1:]
    if library:
        lines = lines[1:]  # dylib's own install name
    return [line.strip().split(' (compatibility version', 1)[0] for line in lines]


def bundle(binary, resources, unixodbc, libtool, version, arch):
    resources.mkdir(parents=True, exist_ok=True)
    prefix = 'dbx-odbc-runtime-' + version.removeprefix('v') + '-'
    sources = [unixodbc / 'lib' / name for name in
               ('libodbc.2.dylib', 'libodbcinst.2.dylib', 'libodbccr.2.dylib')]
    sources.append(libtool / 'lib/libltdl.7.dylib')
    names = {source.name: prefix + source.name for source in sources}
    if not any(pathlib.Path(dep).name == 'libodbc.2.dylib' for dep in dependencies(binary)):
        raise ValueError('ODBC executable must link unixODBC')
    for path in [binary, *sources]:
        if arch not in run('lipo', '-archs', str(path)).split():
            raise ValueError(f'missing {arch} architecture: {path}')
    copies = []
    for source in sources:
        target = resources / names[source.name]
        shutil.copyfile(source, target)
        target.chmod(0o755)
        copies.append(target)
    for path in [*copies, binary]:
        is_library = path != binary
        for dependency in dependencies(path, is_library):
            if dependency.startswith(('/usr/lib/', '/System/Library/')):
                continue
            name = pathlib.Path(dependency).name
            if name not in names:
                raise ValueError(f'unbundled dependency in {path.name}: {dependency}')
            subprocess.run(['install_name_tool', '-change', dependency,
                            '@loader_path/' + names[name], str(path)], check=True)
        if is_library:
            subprocess.run(['install_name_tool', '-id', '@rpath/' + path.name, str(path)], check=True)
        subprocess.run(['codesign', '--force', '--sign', '-', str(path)], check=True)
        subprocess.run(['codesign', '--verify', '--strict', str(path)], check=True)
        if any(not dep.startswith(('@loader_path/', '/usr/lib/', '/System/Library/'))
               for dep in dependencies(path, is_library)):
            raise ValueError(f'nonrelocatable dependency in {path.name}')
    for label, source in [
        ('unixODBC-COPYING.txt', unixodbc / 'COPYING'),
        ('unixODBC-AUTHORS.txt', unixodbc / 'AUTHORS'),
        ('libltdl-COPYING.LIB.txt', libtool / 'share/libtool/COPYING.LIB'),
        ('libltdl-AUTHORS.txt', libtool / 'AUTHORS'),
    ]:
        shutil.copyfile(source, resources / (prefix + label))
    (resources / (prefix + 'NOTICE.txt')).write_text(
        'unixODBC and libltdl are replaceable shared libraries. Package copies have\n'
        'relocated load paths and local ad-hoc signatures. Vendor drivers are not included.\n'
        'Upstream source: https://www.unixodbc.org/ and https://www.gnu.org/software/libtool/\n'
        'Release distributors must provide corresponding library sources with releases.\n', encoding='utf-8')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('binary', 'resources', 'unixodbc', 'libtool'):
        parser.add_argument('--' + name, type=pathlib.Path, required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--arch', choices=['arm64', 'amd64'], required=True)
    args = parser.parse_args()
    bundle(args.binary, args.resources, args.unixodbc, args.libtool,
           args.version, 'x86_64' if args.arch == 'amd64' else args.arch)
