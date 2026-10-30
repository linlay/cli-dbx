"""Additional ZIP-only shared module compatibility and lifecycle checks."""
import argparse, concurrent.futures, hashlib, importlib.util, json
import pathlib, shutil, subprocess, time

ROOT=pathlib.Path(__file__).resolve().parent.parent
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--release-dir',dest='release',type=pathlib.Path,default=ROOT/'dist'/(ROOT/'VERSION').read_text().strip()/'shared-host')
parser.add_argument('--output',type=pathlib.Path,default=ROOT/'dist/shared-host-extra-tests'/time.strftime('%Y%m%d-%H%M%S'))
parser.add_argument('--databases-home',type=pathlib.Path,default=pathlib.Path.home()/'Databases')
args=parser.parse_args()
source=ROOT/'scripts/test-driver-process-windows.py'
spec=importlib.util.spec_from_file_location('dbx_unified',source)
unified=importlib.util.module_from_spec(spec);spec.loader.exec_module(unified)
suite=unified.Suite(args.release,args.output,args.databases_home,True)
credentials=suite.credentials()

original=suite.drivers['postgres']
manifest=json.loads(pathlib.Path(str(original)+'.json').read_text())
changes={
 'host digest':{'host_sha256':'0'*64},'OS':{'os':'darwin'},'Go toolchain':{'toolchain':'go0.0.0'},
 'API version':{'api_version':999},'format version':{'format_version':999},
 'decoded limit':{'decoded_bytes':129*1024*1024},'filename traversal':{'module':'../evil.dbmod'},
 'missing manifest':{},'malformed manifest':{},'compressed data':{},'incorrect decoded size':{'decoded_bytes':1},
}
for label,change in changes.items():
    directory=suite.output/'negative'/label
    directory.mkdir(parents=True)
    module=directory/original.name
    shutil.copyfile(original,module)
    metadata=dict(manifest,**change)
    if label=='compressed data':
        module.write_bytes(b'not a gzip object module')
        metadata['sha256']=hashlib.sha256(module.read_bytes()).hexdigest()
        if 'compressed_bytes' in metadata:metadata['compressed_bytes']=module.stat().st_size
    if label!='missing manifest':
        pathlib.Path(str(module)+'.json').write_text('{' if label=='malformed manifest' else json.dumps(metadata),encoding='utf-8')
    config=suite.configure('postgres',credentials['postgres'],name='negative',driver_path=str(module))
    def reject(config=config,label=label):
        result=suite.call(['query','negative','select 1'],config=config,ok=False)
        if label in ['compressed data','incorrect decoded size']:
            assert result['code'] in ['driver_invalid','driver_protocol_error'],result['code']
        else:assert result['code']==('driver_missing' if label=='missing manifest' else 'driver_invalid'),result['code']
    suite.check('package','reject '+label,reject)

for engine in ['postgres','sqlserver','dm']:
    config=suite.configure(engine,credentials[engine])
    if not suite.check(engine,'correct-credential connection before stress',lambda:suite.call(['conn','test',engine],config=config)):
        raise SystemExit('Stopped: initial connection failed; no authentication loop attempted')
    def only_one_module():
        isolated=suite.output/'one-module'/engine
        isolated.mkdir(parents=True)
        isolated_host=isolated/suite.host.name
        isolated_module=isolated/suite.drivers[engine].name
        for source,target in [(suite.host,isolated_host),(suite.drivers[engine],isolated_module),(pathlib.Path(str(suite.drivers[engine])+'.json'),pathlib.Path(str(isolated_module)+'.json'))]:
            shutil.copyfile(source,target)
        assert len(list(isolated.glob('*.dbmod')))==1
        isolated_config=suite.configure(engine,credentials[engine],name=engine+'_isolated',driver_path=str(isolated_module))
        result=suite.call(['query',engine+'_isolated','select 1 as marker'],config=isolated_config,host=isolated_host)
        assert next(iter(result['data']['rows'][0].values()))==1
    suite.check(engine,'real query with only its own installed module',only_one_module)
    # Each call is an independent CLI process; every result must be a real DB row.
    def initialize(index):
        result=suite.call(['query',engine,'select 1 as marker'],config=config)
        assert next(iter(result['data']['rows'][0].values()))==1
    def stress():
        with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
            futures=[pool.submit(initialize,i) for i in range(6)]
            scheduled=6
            while futures:
                done,_=concurrent.futures.wait(futures,return_when=concurrent.futures.FIRST_COMPLETED)
                for future in done:
                    future.result()  # abort on first failure; no failed call is retried
                    futures.remove(future)
                    if scheduled<50:
                        futures.append(pool.submit(initialize,scheduled));scheduled+=1
    suite.check(engine,'50 real readonly initializations with six concurrent CLIs',stress)
    if suite.results[-1]['status']!='pass':raise SystemExit('Stopped after initialization failure')

slow_queries={
 'sqlserver':'select sum(cast(checksum(a.object_id,b.object_id,c.object_id) as bigint)) from sys.all_objects a cross join sys.all_objects b cross join sys.all_objects c',
 'dm':'select sum(mod(a.object_id+b.object_id*c.object_id,123457)) from all_objects a cross join all_objects b cross join all_objects c',
}
for engine,sql in slow_queries.items():
    config=suite.configure(engine,credentials[engine],name=engine+'_timeout',timeout='2s')
    def timeout():
        parent=subprocess.Popen([str(suite.host),'query',engine+'_timeout',sql,'--config',str(config)],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        try:
            unified.assert_no_driver_children(parent)
            out,err=parent.communicate(timeout=6)
            assert parent.returncode!=0,'expensive query unexpectedly completed before timeout'
            result=json.loads(out)
            assert any(term in json.dumps(result).lower() for term in ['timeout','deadline']),result.get('code')
        finally:
            if parent.poll() is None:parent.kill()
            parent.communicate(timeout=3)
    suite.check(engine,'real expensive query timeout exits CLI without driver children',timeout)
    config=suite.configure(engine,credentials[engine],name=engine+'_parent',timeout='15s')
    def kill_parent():
        parent=subprocess.Popen([str(suite.host),'query',engine+'_parent',sql,'--config',str(config)],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        try:
            unified.assert_no_driver_children(parent)
            assert parent.poll() is None,'expensive query completed before cancellation'
            parent.kill();parent.communicate(timeout=3)
        finally:
            if parent.poll() is None:parent.kill()
            parent.communicate(timeout=3)
    suite.check(engine,'terminate CLI with module loaded and no driver children',kill_parent)
    suite.check(engine,'connection works after cancellation',lambda:suite.call(['query',engine+'_parent','select 1'],config=config))

summary=dict(cases=len(suite.results),failed=sum(r['status']=='fail' for r in suite.results),readonly_calls=150,oracle='not tested: no instance',macOS='not tested: Windows host only')
(suite.output/'summary.json').write_text(json.dumps(summary,indent=2),encoding='utf-8')
print(json.dumps(summary))
raise SystemExit(1 if summary['failed'] else 0)
