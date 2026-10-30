#!/usr/bin/env python3
"""Exercise Windows release ZIPs against local databases without logging secrets.

Requires the current user's DPAPI CLIXML credentials under --databases-home.
Only dbx_ip_<run-id>_* test tables are created and removed. No servers or
existing application data are changed. Reports contain assertions, not passwords.
"""
import argparse
import concurrent.futures
import csv
import ctypes
from ctypes import wintypes
import decimal
import hashlib
import io
import json
import os
import pathlib
import shutil
import subprocess
import sys
import time
import uuid
import zipfile

ROOT = pathlib.Path(__file__).resolve().parent.parent


def child_process_handles(parent_pid):
    """Hold handles to this test parent's children, avoiding PID reuse races."""
    class ProcessEntry(ctypes.Structure):
        _fields_ = [('size', wintypes.DWORD), ('usage', wintypes.DWORD),
                    ('pid', wintypes.DWORD), ('heap', ctypes.c_size_t),
                    ('module', wintypes.DWORD), ('threads', wintypes.DWORD),
                    ('parent', wintypes.DWORD), ('priority', wintypes.LONG),
                    ('flags', wintypes.DWORD), ('exe', wintypes.WCHAR * 260)]
    kernel = ctypes.WinDLL('kernel32', use_last_error=True)
    kernel.CreateToolhelp32Snapshot.restype = wintypes.HANDLE
    kernel.Process32FirstW.argtypes = [wintypes.HANDLE, ctypes.POINTER(ProcessEntry)]
    kernel.Process32NextW.argtypes = [wintypes.HANDLE, ctypes.POINTER(ProcessEntry)]
    kernel.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
    kernel.OpenProcess.restype = wintypes.HANDLE
    kernel.CloseHandle.argtypes = [wintypes.HANDLE]
    snapshot = kernel.CreateToolhelp32Snapshot(2, 0)
    assert snapshot != wintypes.HANDLE(-1).value, 'cannot enumerate test child processes'
    handles = []
    try:
        entry = ProcessEntry()
        entry.size = ctypes.sizeof(entry)
        found = kernel.Process32FirstW(snapshot, ctypes.byref(entry))
        while found:
            if entry.parent == parent_pid and (entry.exe.lower().startswith('dbx-driver-') or entry.exe.lower() == 'dbx.exe'):
                handle = kernel.OpenProcess(0x100001, False, entry.pid)  # synchronize + terminate
                if handle:
                    handles.append(handle)
            found = kernel.Process32NextW(snapshot, ctypes.byref(entry))
    finally:
        kernel.CloseHandle(snapshot)
    return kernel, handles


def assert_no_driver_children(process, duration=.8):
    """Observe module operations without starting an independent driver."""
    deadline = time.monotonic() + duration
    while time.monotonic() < deadline and process.poll() is None:
        kernel, handles = child_process_handles(process.pid)
        try:
            assert not handles, 'in-process module unexpectedly spawned a driver child'
        finally:
            for handle in handles:
                kernel.CloseHandle(handle)
        time.sleep(.02)


class Suite:
    def __init__(self, release, output, home, shared=False):
        self.release, self.output, self.home = release.resolve(), output.resolve(), home.resolve()
        self.output.mkdir(parents=True, exist_ok=False)
        self.shared = shared
        self.prefix = ('dbx_sh_' if shared else 'dbx_ip_') + uuid.uuid4().hex[:8]
        self.results, self.passwords, self.configs = [], [], {}
        self.extracted = self.output / '解压 中文 # 100%'
        self.extracted.mkdir()
        for archive in sorted(self.release.glob('*.zip')):
            if archive.name.startswith(('dbx_', 'dbx-shared-host_', 'dbx-module-', 'dbx-driver-', 'builtin.dbx_')):
                with zipfile.ZipFile(archive) as z:
                    assert z.testzip() is None, archive.name
                    for info in z.infolist():
                        target = (self.extracted / info.filename).resolve()
                        assert target.is_relative_to(self.extracted.resolve()), 'unsafe archive path'
                    z.extractall(self.extracted)
        self.host = next(p for p in self.extracted.rglob('dbx.exe') if 'runtime' not in p.parts)
        self.platform = self.extracted / 'runtime/connectors/builtin.dbx/bin/dbx.exe'
        extension = '.dbmod' if shared else '.exe'
        self.drivers = {e: next(self.extracted.rglob('dbx-driver-' + e + '-*'+extension)) for e in ['postgres','oracle','dm','sqlserver']}

    def redact(self, text):
        for secret in sorted(self.passwords, key=len, reverse=True):
            if secret:
                text = text.replace(secret, '<redacted>')
        return text

    def check(self, engine, label, callback):
        start = time.monotonic()
        try:
            callback()
            result = dict(engine=engine, case=label, status='pass')
        except Exception as error:
            result = dict(engine=engine, case=label, status='fail', error=self.redact(str(error))[:2400])
        result['seconds'] = round(time.monotonic()-start, 3)
        self.results.append(result)
        print(engine, label, result['status'], flush=True)
        (self.output/'results.json').write_text(json.dumps(self.results,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
        return result['status'] == 'pass'

    def call(self, args, config=None, ok=True, code=None, host=None, text=False):
        command = [str(host or self.host), *map(str,args)]
        if config:
            command += ['--config',str(config)]
        process = subprocess.run(command,capture_output=True,encoding='utf-8',errors='replace',timeout=25)
        combined = process.stdout + process.stderr
        assert not any(p and p in combined for p in self.passwords), 'CLI exposed a database password'
        if text:
            assert process.returncode == 0, self.redact(combined)
            return process.stdout
        try:
            value = json.loads(process.stdout,parse_float=decimal.Decimal)
        except Exception:
            raise AssertionError(self.redact('invalid JSON: '+combined)) from None
        assert (process.returncode == 0) == ok and value.get('ok') == ok, self.redact(json.dumps(value,ensure_ascii=False,default=str))
        if code:
            assert value.get('code') == code, self.redact(json.dumps(value,ensure_ascii=False,default=str))
        return value

    def credentials(self):
        paths = dict(postgres=self.home/'PostgreSQL/18/localdev.credential.xml',sqlserver=self.home/'SQLServer/2025/localdev.credential.xml',dm=self.home/'Dameng/DM8/LOCALDEV.credential.xml')
        # Plaintext exists only in the private child pipe and this process memory.
        code = '[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); $r=@{}; '
        for engine,path in paths.items():
            code += "$c=Import-Clixml -LiteralPath '"+str(path).replace("'","''")+"'; $r['"+engine+"']=@{user=$c.UserName;password=$c.GetNetworkCredential().Password}; "
        code += '$r | ConvertTo-Json -Compress'
        child = subprocess.run(['powershell.exe','-NoProfile','-Command',code],capture_output=True,encoding='utf-8',timeout=20)
        if child.returncode:
            raise RuntimeError('cannot load current-user database credentials')
        values = json.loads(child.stdout)
        self.passwords = [v['password'] for v in values.values()]
        for engine, value in values.items():
            child = subprocess.run([str(self.host),'secret','encrypt'],input=value['password']+'\n',capture_output=True,encoding='utf-8',timeout=20)
            if child.returncode or not child.stdout.startswith('password = "dbx-aes-gcm:v2:'):
                raise RuntimeError('system credential-store encryption failed for '+engine)
            value['encrypted_line'] = child.stdout.strip()
            value.pop('password')
        return values

    def configure(self, engine, credential, name=None, **extra):
        name = name or engine
        config = self.output/(name+'.toml')
        settings = dict(engine=engine,allow_actions=['query','update','schema','admin'],timeout='10s')
        if engine == 'sqlite':
            settings['path'] = str(self.output/'数据库 中文 # 100%.db')+'?_pragma=busy_timeout(5000)'
        else:
            settings.update(host='127.0.0.1',port={'postgres':5432,'sqlserver':1433,'dm':5236}[engine],user=credential['user'],database={'postgres':'localdb','sqlserver':'localdb','dm':'DAMENG'}[engine],driver_path=str(self.drivers[engine]))
            if engine == 'postgres': settings['sslmode']='disable'
        settings.update(extra)
        body = '[connection]\n'+'\n'.join(k+'='+json.dumps(v,ensure_ascii=False) for k,v in settings.items())+'\n'
        if credential: body += credential['encrypted_line']+'\n'
        config.write_text('\ufeff'+body,encoding='utf-8')
        self.configs[name]=config
        return config

    def database(self, engine, credential):
        config = self.configure(engine,credential)
        name = engine
        table = self.prefix+'_'+engine
        imported, child = table+'_i', table+'_c'
        quote = lambda value: '['+value+']' if engine == 'sqlserver' else '"'+value+'"'
        q, qi, qc = map(quote,[table,imported,child])
        call = lambda *a, **kw: self.call(a,config=config,**kw)
        query = lambda sql, **kw: call('query',name,sql,**kw)['data']['rows']
        scalar = lambda sql: next(iter(query(sql)[0].values()))
        check = lambda label, fn: self.check(engine,label,fn)
        if not check('conn test from ZIP',lambda: call('conn','test',name)): return
        check('conn show redacted',lambda: call('conn','show',name))
        check('conn list explicit file',lambda: call('conn','list'))
        check('inspect schema',lambda: call('inspect','schema',name))
        texttype = 'nvarchar(200)' if engine == 'sqlserver' else 'varchar(200)'
        amounttype = 'text' if engine == 'sqlite' else 'decimal(30,10)'
        timetype = 'datetime2' if engine == 'sqlserver' else 'timestamp' if engine != 'sqlite' else 'text'
        binarytype = {'postgres':'bytea','sqlserver':'varbinary(20)','dm':'varbinary(20)','sqlite':'blob'}[engine]
        literal = lambda text: ('N' if engine=='sqlserver' else '')+"'"+text.replace("'","''")+"'"
        blob = {'postgres':"decode('00ff','hex')",'sqlserver':'0x00ff','dm':"hextoraw('00ff')",'sqlite':"x'00ff'"}[engine]
        amount = '12345678901234567890.1234567890'
        owned = []
        try:
            def create():
                call('schema',name,f'create table {q}(id bigint primary key, name {texttype}, amount {amounttype}, payload {binarytype}, happened {timetype})')
                owned.append(table)
            if not check('schema create',create): return
            check('schema alter',lambda: call('schema',name,f'alter table {q} add marker integer default 0'))
            check('update insert typed values',lambda: call('update',name,f"insert into {q}(id,name,amount,payload,happened) values(1,{literal('中文😀')},'{amount}',{blob},'2026-10-13 01:02:03')"))
            check('update insert NULL',lambda: call('update',name,f'insert into {q}(id,name) values(2,NULL)'))
            check('update insert int64 max',lambda: call('update',name,f'insert into {q}(id,name) values(9223372036854775807,{literal("大整数")})'))
            def fidelity():
                rows = query(f'select id,name,amount,payload,happened from {q} order by id')
                rows = [{k.lower():v for k,v in r.items()} for r in rows]
                assert rows[0]['name']=='中文😀', 'Unicode mismatch: '+str(rows[0]['name'])
                assert decimal.Decimal(str(rows[0]['amount'])) == decimal.Decimal(amount), 'decimal precision lost: '+str(rows[0]['amount'])
                assert rows[0]['payload']=='AP8=', 'binary lost: '+str(rows[0]['payload'])
                assert '2026-10-13' in str(rows[0]['happened']), 'date lost'
                assert rows[1]['name'] is None, 'NULL lost'
                assert rows[2]['id']==9223372036854775807, 'int64 precision lost'
            check('query exact types Unicode binary datetime NULL',fidelity)
            def duplicate():
                row=query(f'select id,id from {q} where id=1')[0]
                assert len(row)==2 and set(k.lower() for k in row)=={'id','id_2'}, str(row)
            check('query duplicate columns',duplicate)
            def pages():
                first=call('query',name,f'select id from {q} order by id','--page-size','2')
                assert len(first['data']['rows'])==2 and first['more']
                second=call('query',name,f'select id from {q} order by id','--page-size','2','--cursor',first['data']['next_cursor'])
                assert len(second['data']['rows'])==1 and not second['more']
            check('query pagination',pages)
            check('query table output',lambda: call('query',name,f'select name from {q} where id=1','--format','table',text=True))
            sqlfile=self.output/(engine+' 中文 # 100%.sql')
            sqlfile.write_text('\ufeff'+f'select id from {q} where id=1',encoding='utf-8')
            check('query BOM SQL special path',lambda: call('query','file',name,sqlfile))
            check('inspect table',lambda: call('inspect','table',name,table))
            check('inspect connection',lambda: call('inspect','connection',name))
            admin={'postgres':f'analyze {q}','sqlserver':'set nocount off','dm':'set schema LOCALDEV','sqlite':f'analyze {q}'}[engine]
            check('admin safe operation',lambda: call('admin',name,admin))
            def create_import():
                call('schema',name,f'create table {qi}(id bigint primary key,name {texttype},amount {amounttype}, constraint {quote(self.prefix+"_uq")} unique(id,name))')
                owned.append(imported)
            if check('schema import table',create_import):
                def create_child():
                    call('schema',name,f'create table {qc}(id bigint primary key,parent_id bigint,parent_name {texttype}, constraint {quote(self.prefix+"_fk")} foreign key(parent_id,parent_name) references {qi}(id,name))')
                    owned.append(child)
                if check('schema composite foreign key',create_child):
                    def unique_metadata():
                        meta=call('inspect','table',name,imported)['data']
                        assert [c.lower() for c in meta['primary_key']] == ['id'], str(meta)
                        assert any([c.lower() for c in k['columns']]==['id','name'] for k in meta['unique_keys']), str(meta)
                    check('inspect ordered primary and composite unique keys',unique_metadata)
                    def fk_metadata():
                        keys=call('inspect','table',name,child)['data']['foreign_keys']
                        assert any([c.lower() for c in k['columns']]==['parent_id','parent_name'] and k['ref_table'].lower()==imported.lower() and [c.lower() for c in k['ref_columns']]==['id','name'] for k in keys), str(keys)
                    check('inspect ordered composite foreign key',fk_metadata)
                    def relations():
                        keys=call('inspect','schema',name)['data']['relations']
                        assert any(k['from_table'].lower()==child.lower() and [c.lower() for c in k['from_columns']]==['parent_id','parent_name'] and k['to_table'].lower()==imported.lower() and [c.lower() for c in k['to_columns']]==['id','name'] for k in keys), str(keys)
                    check('inspect schema composite relationship',relations)
                data=self.output/(engine+' 中文.csv')
                header = 'ID,NAME,AMOUNT' if engine == 'dm' else 'id,name,amount'
                # Explicit bytes prevent Windows translating CRLF into CRCRLF.
                data.write_bytes(('\ufeff'+header+'\r\n1,"中文,逗号",1.2500000000\r\n2,第二行,2.5000000000\r\n').encode('utf-8'))
                check('import CSV BOM Unicode',lambda: call('import','file',data,name,imported))
                doc=self.output/(engine+' 精确.json')
                exact_json = '[{"id":9223372036854775807,"name":"JSON中文","amount":12345678901234567890.1234567890}]'
                if engine == 'dm':
                    for key in ['id', 'name', 'amount']:
                        exact_json = exact_json.replace('"'+key+'":', '"'+key.upper()+'":')
                doc.write_text('\ufeff'+exact_json,encoding='utf-8')
                check('import JSON exact values',lambda: call('import','file',doc,name,imported))
                def imported_exact():
                    rows=query(f'select id,amount from {qi} where id=9223372036854775807')
                    row={k.lower():v for k,v in rows[0].items()}
                    assert row['id']==9223372036854775807 and decimal.Decimal(str(row['amount']))==decimal.Decimal(amount), str(row)
                check('import JSON precision verified',imported_exact)
                for fmt in ['json','csv']:
                    target=self.output/(engine+' 导出.'+fmt)
                    check('export '+fmt,lambda fmt=fmt,target=target: call('export','table',imported,name,target,'--format',fmt))
                    check('export '+fmt+' content',lambda target=target: self.require('中文' in target.read_text(encoding='utf-8-sig'),'export lost Unicode'))
                overflow=self.output/(engine+' overflow.csv')
                header = 'ID,NAME' if engine == 'dm' else 'id,name'
                overflow.write_text(header+'\n10,over\n11,over\n',encoding='utf-8')
                def over_import():
                    call('import','file',overflow,name,imported,'--max-rows-affected','1',ok=False)
                    assert scalar(f'select count(*) from {qi} where id in(10,11)')==0, 'over-limit import committed'
                check('import over-limit rollback',over_import)
                duplicatefile=self.output/(engine+' duplicate.csv')
                duplicatefile.write_text(header+'\n12,rollback\n1,duplicate\n',encoding='utf-8')
                def failed_import():
                    call('import','file',duplicatefile,name,imported,ok=False)
                    assert scalar(f'select count(*) from {qi} where id=12')==0,'failed import partially committed'
                check('import SQL error rollback',failed_import)
            def plan_run(steps,filename='plan.json',ok=True):
                plan=self.output/(engine+filename)
                plan.write_text('\ufeff'+json.dumps({'steps':steps},ensure_ascii=False),encoding='utf-8')
                return call('tx',name,'--plan',plan,ok=ok)
            check('tx commit',lambda: plan_run([{'action':'update','sql':f'update {q} set marker=1 where id=1','max_rows_affected':1},{'action':'query','sql':f'select marker from {q} where id=1'}]))
            def rollback():
                plan_run([{'action':'update','sql':f'update {q} set marker=9 where id=1'},{'action':'update','sql':f'insert into {quote(table+"_missing")}(id) values(1)'}],ok=False)
                assert scalar(f'select marker from {q} where id=1')==1,'transaction partially committed'
            check('tx SQL error rollback',rollback)
            def write_limit():
                call('update',name,f'update {q} set marker=8 where id in(1,2)','--max-rows-affected','1',ok=False)
                assert scalar(f'select marker from {q} where id=1')==1,'guarded update committed'
            check('update affected-row rollback',write_limit)
            def tx_limit():
                plan_run([{'action':'update','sql':f'update {q} set marker=7 where id in(1,2)','max_rows_affected':1}],ok=False)
                assert scalar(f'select marker from {q} where id=1')==1,'over-limit transaction committed'
            check('tx affected-row rollback',tx_limit)
            for label,args,code in [
                ('missing WHERE',['update',name,f'delete from {q}'],'missing_where'),
                ('multiple statements',['query',name,f'select id from {q}; select id from {q}'],'multiple_statements_blocked'),
                ('action mismatch',['query',name,f'update {q} set marker=1 where id=1'],'action_mismatch')]:
                check('policy '+label,lambda args=args,code=code: call(*args,ok=False,code=code))
            for mode,extra,expected in [('actions',dict(allow_actions=['query']),'action_blocked'),('tables',dict(allow_tables=[table+'_forbidden']),'table_blocked')]:
                policy=self.configure(engine,credential,name=engine+'_'+mode,**extra)
                args=['query',engine+'_'+mode,f'select id from {q}'] if mode=='tables' else ['update',engine+'_'+mode,f'update {q} set marker=1 where id=1']
                check('policy '+mode,lambda args=args,policy=policy,expected=expected: self.call(args,config=policy,ok=False,code=expected))
            readonly=self.configure(engine,credential,name=engine+'_readonly',readonly=True)
            check('readonly metadata preserves allow_actions semantics',lambda:self.call(['update',engine+'_readonly',f'update {q} set marker=1 where id=1'],config=readonly))
            if engine=='sqlite':
                ro=self.configure(engine,None,name='sqlite_ro',path=str(self.output/'数据库 中文 # 100%.db')+'?mode=ro&_pragma=busy_timeout(5000)')
                def read_only():
                    result=self.call(['query','sqlite_ro',f'select id from {q} where id=1'],config=ro)
                    assert len(result['data']['rows']) == 1, 'read-only URI did not open the existing database'
                    result=self.call(['update','sqlite_ro',f'update {q} set marker=1 where id=1'],config=ro,ok=False)
                    assert 'readonly' in json.dumps(result).lower(), 'write failed for a reason other than read-only mode'
                check('SQLite URI mode=ro rejects write',read_only)
            def concurrent_reads():
                with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
                    results=list(pool.map(lambda _:query(f'select id from {q} where id=1'),range(8)))
                assert all(len(r)==1 for r in results)
            check('concurrent independent calls',concurrent_reads)
            check('Platform ZIP real connection',lambda: self.call(['conn','test',name],config=config,host=self.platform))
            check('Platform ZIP typed query',lambda: self.call(['query',name,f'select id,name from {q} where id=1'],config=config,host=self.platform))
        finally:
            for owned_table in reversed(owned):
                assert owned_table.startswith(self.prefix+'_')
                check('cleanup '+owned_table,lambda t=owned_table: call('schema',name,'drop table '+quote(t)))

    @staticmethod
    def require(condition,message):
        assert condition,message

    def package_cases(self,credential):
        engine='package'
        for args in [['--help'],['query','--help'],['tx','--help'],['version']]:
            self.check(engine,'CLI '+' '.join(args),lambda args=args:self.call(args,text=True))
        self.check(engine,'Platform executable matches core',lambda:self.require(self.platform.read_bytes()==self.host.read_bytes(),'Platform binary mismatch'))
        for driver_engine,driver in self.drivers.items():
            metadata=json.loads(pathlib.Path(str(driver)+'.json').read_text())
            self.check(engine,driver_engine+' SHA256',lambda driver=driver,metadata=metadata:self.require(hashlib.sha256(driver.read_bytes()).hexdigest()==metadata['sha256'],'driver digest mismatch'))
        for mode,change,code in [
            ('missing',None,'driver_missing'),
            ('digest',{'sha256':'0'*64},'driver_integrity_failed'),
            ('protocol',{'protocol_version':999},'driver_protocol_mismatch'),
            ('platform',{'arch':'arm64'},'driver_invalid'),
            ('engine',{'engine':'dm'},'driver_invalid')]:
            driver=self.extracted/('invalid-'+mode+('.dbmod' if self.shared else '.exe'))
            if change:
                shutil.copyfile(self.drivers['postgres'],driver)
                metadata=json.loads(pathlib.Path(str(self.drivers['postgres'])+'.json').read_text())
                metadata.update(change)
                metadata['module' if self.shared else 'executable'] = driver.name
                pathlib.Path(str(driver)+'.json').write_text(json.dumps(metadata),encoding='utf-8')
            name='bad_'+mode
            cfg=self.configure('postgres',credential,name=name,driver_path=str(driver))
            expected = 'driver_invalid' if self.shared and mode != 'missing' else code
            self.check(engine,'reject '+mode,lambda cfg=cfg,name=name,code=expected:self.call(['query',name,'select 1'],config=cfg,ok=False,code=code))
            guarded=self.configure('postgres',credential,name=name+'_guard',driver_path=str(driver),allow_actions=['query'])
            self.check(engine,'policy before '+mode,lambda cfg=guarded,name=name:self.call(['schema',name+'_guard','create table '+self.prefix+'_blocked(id integer)'],config=cfg,ok=False,code='action_blocked'))

    def lifecycle_cases(self, credential, engine='postgres'):
        slow_sql = {
            'postgres': 'select pg_sleep(8)',
            'sqlserver': 'select sum(cast(checksum(a.object_id,b.object_id,c.object_id) as bigint)) from sys.all_objects a cross join sys.all_objects b cross join sys.all_objects c',
            'dm': 'select sum(mod(a.object_id+b.object_id*c.object_id,123457)) from all_objects a cross join all_objects b cross join all_objects c',
        }[engine]
        timeout_name = engine + '_timeout'
        timeout_config = self.configure(engine, credential, name=timeout_name, timeout='400ms' if engine == 'postgres' else '2s')
        def timeout():
            started = time.monotonic()
            result = self.call(['query', timeout_name, slow_sql], config=timeout_config, ok=False)
            assert time.monotonic() - started < 5, 'timeout failed to stop long database query'
            assert 'deadline' in json.dumps(result).lower() or 'timeout' in json.dumps(result).lower(), 'unclear timeout error'
        self.check(engine, 'real database query timeout', timeout)
        parent_name = engine + '_parent_cancel'
        config = self.configure(engine, credential, name=parent_name, timeout='15s')
        def cancel_parent():
            parent = subprocess.Popen([str(self.host), 'query', parent_name, slow_sql, '--config', str(config)],
                                      stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            kernel, handles = None, []
            try:
                if self.shared:
                    assert_no_driver_children(parent)
                else:
                    deadline = time.monotonic() + 5
                    while time.monotonic() < deadline and not handles:
                        kernel, handles = child_process_handles(parent.pid)
                        if not handles:
                            time.sleep(.05)
                    assert len(handles) == 1, 'expected one driver child for database query'
                    time.sleep(.8)  # allow the request to reach the real server
                assert parent.poll() is None, 'long database query finished before parent termination'
                parent.kill()
                parent.communicate(timeout=3)
                if handles:
                    kernel.WaitForSingleObject.argtypes = [wintypes.HANDLE, wintypes.DWORD]
                    assert kernel.WaitForSingleObject(handles[0], 5000) == 0, 'driver survived parent termination'
            finally:
                if parent.poll() is None:
                    parent.kill()
                parent.communicate(timeout=3)
                if kernel:
                    kernel.TerminateProcess.argtypes = [wintypes.HANDLE, wintypes.UINT]
                    for handle in handles:
                        kernel.TerminateProcess(handle, 1)  # only this test's held child handles
                        kernel.CloseHandle(handle)
        self.check(engine, 'terminate in-process CLI without driver children' if self.shared else 'parent termination reaps real driver', cancel_parent)
        self.check(engine, 'connection usable after cancellation', lambda: self.call(['query', parent_name, 'select 1'], config=config))

    def metadata_cases(self):
        def primary_order():
            table=self.prefix+'_pk_order'
            cfg=self.configs['sqlite']
            self.call(['schema','sqlite',f'create table "{table}"(a integer,b integer,primary key(b,a))'],config=cfg)
            try:
                meta=self.call(['inspect','table','sqlite',table],config=cfg)['data']
                assert meta['primary_key']==['b','a'],str(meta)
            finally:
                self.call(['schema','sqlite',f'drop table "{table}"'],config=cfg)
        self.check('sqlite','composite primary-key definition order',primary_order)
        def same_names():
            cfg=self.configs['postgres']
            parent=self.prefix+'_relation_parent'
            children=[self.prefix+'_relation_c1',self.prefix+'_relation_c2']
            fk=self.prefix+'_same_fk'
            owned=[]
            try:
                self.call(['schema','postgres',f'create table "{parent}"(id bigint primary key)'],config=cfg)
                owned.append(parent)
                for child in children:
                    self.call(['schema','postgres',f'create table "{child}"(parent_id bigint,constraint "{fk}" foreign key(parent_id) references "{parent}"(id))'],config=cfg)
                    owned.append(child)
                relations=self.call(['inspect','schema','postgres'],config=cfg)['data']['relations']
                found=[r for r in relations if r['name']==fk]
                assert {r['from_table'] for r in found}==set(children) and len(found)==2,str(found)
                assert all(r['from_columns']==['parent_id'] and r['to_columns']==['id'] for r in found),str(found)
            finally:
                for table in reversed(owned):
                    self.call(['schema','postgres',f'drop table "{table}"'],config=cfg)
        self.check('postgres','same-named foreign keys stay separate',same_names)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--release-dir',type=pathlib.Path,default=ROOT/'dist'/((ROOT/'VERSION').read_text().strip()))
    parser.add_argument('--output',type=pathlib.Path,default=ROOT/'dist/driver-process-real-tests'/time.strftime('%Y%m%d-%H%M%S'))
    parser.add_argument('--databases-home',type=pathlib.Path,default=pathlib.Path.home()/'Databases')
    parser.add_argument('--shared-host',action='store_true',help='Test shared host and .dbmod ZIPs with dbx_sh_ test tables')
    args=parser.parse_args()
    suite=Suite(args.release_dir,args.output,args.databases_home,args.shared_host)
    credentials=suite.credentials()
    suite.package_cases(credentials['postgres'])
    for engine in ['postgres', 'sqlserver', 'dm']:
        suite.lifecycle_cases(credentials[engine], engine)
    for engine in ['sqlite','postgres','sqlserver','dm']:
        suite.check(engine,'database workflow',lambda engine=engine:suite.database(engine,credentials.get(engine)))
    suite.metadata_cases()
    failures=sum(r['status']=='fail' for r in suite.results)
    print('Report:',suite.output/'results.json', 'cases:',len(suite.results),'failures:',failures)
    return 1 if failures else 0


if __name__=='__main__':
    sys.exit(main())
