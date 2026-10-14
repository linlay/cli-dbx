#!/usr/bin/env python3
"""Verify a host against a canonical rebuild before pairing object modules."""
import argparse
import hashlib
import os
import pathlib
import subprocess
import tempfile

ROOT=pathlib.Path(__file__).resolve().parent.parent

def verify(host, build):
    host=pathlib.Path(host)
    before=hashlib.sha256(host.read_bytes()).digest()
    with tempfile.TemporaryDirectory(prefix='canonical-',dir=ROOT/'dist') as directory:
        canonical=pathlib.Path(directory)/host.name
        build(canonical)
        if hashlib.sha256(canonical.read_bytes()).digest()!=before:
            raise ValueError('host differs from canonical source/SDK/dependency/flag rebuild')
    if hashlib.sha256(host.read_bytes()).digest()!=before:
        raise ValueError('host changed during canonical verification')
    return before.hex()

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--host',type=pathlib.Path,required=True)
    parser.add_argument('--ldflags',required=True)
    args=parser.parse_args()
    def build(destination):
        subprocess.run(['go','build','-tags','dbmod,dbmod_runtime','-trimpath','-ldflags',args.ldflags,'-o',str(destination),'./cmd/dbx'],cwd=ROOT,check=True)
        if os.environ.get('GOOS')=='darwin':
            subprocess.run(['codesign','--force','--sign','-','--identifier','com.linlay.dbx','--entitlements',str(ROOT/'scripts/shared-host-entitlements.plist'),str(destination)],check=True)
    print('canonical host SHA-256:',verify(args.host,build))

if __name__=='__main__':main()
