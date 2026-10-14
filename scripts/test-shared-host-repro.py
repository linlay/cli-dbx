#!/usr/bin/env python3
"""Rebuild each real module in another process and compare exact payload bytes."""
import argparse,hashlib,pathlib,subprocess,tempfile
ROOT=pathlib.Path(__file__).resolve().parent.parent
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--host',type=pathlib.Path,required=True)
parser.add_argument('--packer',type=pathlib.Path,required=True)
args=parser.parse_args()
for original in sorted(args.host.parent.glob('*.dbmod')):
    manifest=__import__('json').loads(pathlib.Path(str(original)+'.json').read_text())
    engine=manifest['engine']
    with tempfile.TemporaryDirectory(prefix='repro-',dir=ROOT/'dist') as directory:
        replica=pathlib.Path(directory)/original.name
        subprocess.run([str(args.packer),'--host',str(args.host),'--package','github.com/linlay/cli-dbx/modules/'+engine,'--out',str(replica)],cwd=ROOT,check=True)
        if replica.read_bytes()!=original.read_bytes():raise SystemExit('module reconstruction is nondeterministic: '+engine)
        print('reproducible module:',engine,hashlib.sha256(original.read_bytes()).hexdigest())
