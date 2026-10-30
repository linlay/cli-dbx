#!/usr/bin/env python3
"""Generate an ignored shared-build workspace without changing root module policy."""
import argparse
import json
import os
import pathlib

def prepare(output):
    root = pathlib.Path(__file__).resolve().parent.parent
    output = output.resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    def quote(path):
        resolved = path.resolve()
        try:
            value = pathlib.Path(os.path.relpath(resolved, output.parent)).as_posix()
        except ValueError:
            # Windows has no relative path between different drive letters.
            # Preserve the native absolute spelling and quote its backslashes.
            value = str(resolved)
        return json.dumps(value)
    content = 'go 1.26.6\n\nuse (\n\t' + quote(root) + '\n\t' + quote(root / 'modules') + '\n)\n'
    for module, directory in [('github.com/spf13/cobra', 'cobra'), ('github.com/pkujhd/goloader', 'goloader')]:
        content += '\nreplace ' + module + ' => ' + quote(root / 'third_party' / directory) + '\n'
    output.write_text(content, encoding='utf-8')
    print(output)

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    prepare(parser.parse_args().output)
