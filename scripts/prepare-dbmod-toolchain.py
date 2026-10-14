#!/usr/bin/env python3
"""Make a private Go 1.26 source tree; never alter the installed SDK."""
import argparse
import json
import os
import pathlib
import re
import shutil
import stat
import subprocess


def write_module(output):
    module = output / 'go.mod'
    # An input SDK may already contain this file. Never truncate a hard link
    # copied from that SDK; only replace our private directory entry.
    if module.exists():
        module.unlink()
    module.write_text('module dbx-private-toolchain\n\ngo 1.26.6\n', encoding='utf-8')


def prepare(output):
    original = pathlib.Path(subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip())
    version = subprocess.check_output(['go', 'env', 'GOVERSION'], text=True).strip()
    match = re.fullmatch(r'go1\.26(?:\.(\d+))?', version)
    if match is None:
        raise SystemExit('A stable Go 1.26 patch release is required')
    if int(match.group(1) or 0) < 6:
        raise SystemExit('Go 1.26.6 or later in the Go 1.26 series is required by the Oracle dependency')
    output = output.resolve()
    if output == original.resolve() or original.resolve() in output.parents:
        raise SystemExit('private toolchain must be outside the installed Go tree')
    marker = output / '.dbx-toolchain.json'
    identity = {'source': str(original.resolve()), 'version': version}
    if marker.exists():
        if json.loads(marker.read_text()) != identity:
            raise SystemExit('private toolchain identity changed; choose a new output directory')
        write_module(output)
        print(output)
        return
    if output.exists():
        raise SystemExit('refusing to overwrite an existing toolchain directory')

    def copy(source, target):
        try:
            os.link(source, target)
        except OSError:
            shutil.copy2(source, target)

    # Existing files are immutable hard links where possible. Only new files are
    # added below; none of the linked system files are edited or patched.
    shutil.copytree(original, output, copy_function=copy)
    # Toolchains downloaded into GOMODCACHE have read-only directory modes.
    # Only make copied directories writable, never hard-linked SDK files.
    output.chmod(output.stat().st_mode | stat.S_IWUSR)
    cmd = output / 'src/cmd'
    cmd.chmod(cmd.stat().st_mode | stat.S_IWUSR)
    if not (cmd / 'objfile').exists():
        shutil.copytree(original / 'src/cmd/internal', cmd / 'objfile', copy_function=copy)
    write_module(output)
    if marker.exists():
        marker.unlink()
    marker.write_text(json.dumps(identity), encoding='utf-8')
    print(output)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    prepare(parser.parse_args().output)
