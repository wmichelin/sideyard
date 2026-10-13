#!/usr/bin/env python3
import datetime
import importlib.util
import io
import json
import os
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch
from unittest.mock import Mock
import time
import archive


def module(name):
    spec=importlib.util.spec_from_file_location(name,Path(__file__).with_name(name+'.py'))
    item=importlib.util.module_from_spec(spec);spec.loader.exec_module(item);return item
runner=module('run-backup')
monitor=module('monitor')


class BackupTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name);self.key=b'synthetic-key'

    def fixture(self, identity='20261012T000000Z-'+'a'*32, when=1000000):
        folder=self.root/identity;folder.mkdir()
        cipher=folder/archive.FILES[0];cipher.write_bytes(b'synthetic encrypted fixture')
        (folder/archive.FILES[1]).write_bytes(archive.MAGIC+archive.file_digest(cipher,self.key))
        payload={'format':1,'id':identity,'created':datetime.datetime.fromtimestamp(when,datetime.timezone.utc).isoformat(),'environment':'production','backend':'postgres','source_revision':'a'*40,'data_generation':'fixture','files':{name:{'bytes':(folder/name).stat().st_size,'sha256':archive.file_digest(folder/name).hex()} for name in archive.FILES[:2]}}
        (folder/'manifest.json').write_bytes(archive.manifest(payload,self.key));return folder

    def test_authentication_scope_and_partial_sets(self):
        folder=self.fixture()
        self.assertEqual(archive.verify(folder,'production',self.key)['backend'],'postgres')
        for environment,key in [('staging',self.key),('production',b'wrong-key')]:
            with self.assertRaises(ValueError):archive.verify(folder,environment,key)
        marker=folder/'manifest.json';data=json.loads(marker.read_bytes());data['payload']['environment']='staging';marker.write_text(json.dumps(data))
        with self.assertRaises(ValueError):archive.verify(folder,'staging',self.key)
        marker.unlink()
        with self.assertRaises(ValueError):archive.verify(folder,'production',self.key)

    def test_transport_traversal_symlink_duplicate_and_missing_members(self):
        for name,kind in [('../escape',tarfile.REGTYPE),('database.sql.enc',tarfile.SYMTYPE),('manifest.json',tarfile.REGTYPE)]:
            with self.subTest(name=name,kind=kind):
                stream=io.BytesIO()
                with tarfile.open(fileobj=stream,mode='w') as out:
                    info=tarfile.TarInfo(name);info.type=kind;info.size=1;out.addfile(info,io.BytesIO(b'x'))
                stream.seek(0)
                directory=Path(tempfile.mkdtemp(dir=self.root))
                with self.assertRaises(ValueError):runner.receive(stream,directory)
        folder=self.fixture();stream=io.BytesIO()
        with tarfile.open(fileobj=stream,mode='w') as out:
            for name in archive.FILES:out.add(folder/name,arcname=name)
        stream.seek(0);directory=Path(tempfile.mkdtemp(dir=self.root));runner.receive(stream,directory)
        self.assertEqual(archive.verify(directory,'production',self.key)['id'],folder.name)

    def test_retention_exact_cutoff_keeps_newest_unknown_and_incomplete(self):
        now=2000000;cutoff=now-7*86400
        old=self.fixture('20261001T000000Z-'+'b'*32,cutoff-1)
        boundary=self.fixture('20261002T000000Z-'+'c'*32,cutoff)
        newest=self.fixture('20261012T000000Z-'+'d'*32,now)
        unknown=self.root/'operator-file';unknown.write_text('keep')
        partial=self.root/('20260901T000000Z-'+'e'*32);partial.mkdir()
        runner.prune_local(self.root,self.key,'production',7,now)
        self.assertFalse(old.exists());self.assertTrue(boundary.exists());self.assertTrue(newest.exists());self.assertTrue(unknown.exists());self.assertTrue(partial.exists())
        runner.prune_local(self.root,self.key,'production',7,now+20*86400)
        self.assertTrue(newest.exists(),'last known good set must survive an extended outage')

    def test_upload_verifies_components_before_manifest_and_stops_on_error(self):
        config=self.root/'rclone.conf';config.write_text('[sideyard-drive]\ntype = drive\nroot_folder_id = '+runner.FOLDER_ID+'\n');config.chmod(0o600)
        commands=[]
        class Result:returncode=0
        def call(args,**kw):commands.append(args);return Result()
        with patch.object(runner,'CONFIG_ROOT',self.root),patch.object(runner.subprocess,'run',call):
            runner.upload_drive(self.fixture(),'production','fixture')
        self.assertEqual([x[9] for x in commands],['copy','check','copy','check'])
        self.assertIn('--exclude',commands[0]);self.assertIn('--download',commands[1]);self.assertIn('--include',commands[2])
        commands.clear()
        class Failure:returncode=1
        def fail(args,**kw):commands.append(args);return Failure()
        with patch.object(runner,'CONFIG_ROOT',self.root),patch.object(runner.subprocess,'run',fail):
            with self.assertRaises(ValueError):runner.upload_drive(self.root,'production','fixture')
        self.assertEqual(len(commands),1)

    def test_freshness_and_destination_are_checked_independently_of_source_host(self):
        now=2000000;marker={'environment':'production','destination':'google-drive','verified_at':now}
        self.assertFalse(monitor.problem(marker,'production',False,now,'google-drive'))
        for changed,failed,check_time,destination in [(marker,True,now,'google-drive'),({},False,now,'google-drive'),(marker,False,now+27*3600,'google-drive'),(marker,False,now,'g5')]:
            self.assertTrue(monitor.problem(changed,'production',failed,check_time,destination))

    def test_notifications_use_fixed_owner_and_confirm_delivery(self):
        config={'INTEREST_NOTIFY_TO':'wmichelin@gmail.com','INTEREST_NOTIFY_FROM':'Sideyard <alerts@example.test>','RESEND_API_KEY':'fixture'}
        class Opener:
            def open(self,request,timeout):self.request=request;return io.BytesIO(b'{"id":"accepted"}')
        client=Opener();monitor.notice(config,'production','test','Synthetic alert',client)
        self.assertEqual(json.loads(client.request.data)['to'],['wmichelin@gmail.com'])
        config['INTEREST_NOTIFY_TO']='other@example.test'
        with self.assertRaises(ValueError):monitor.notice(config,'production','test','Synthetic alert',client)

    def test_export_and_upload_failures_preserve_success_and_do_not_prune(self):
        fixture=self.fixture(when=int(time.time()))
        stream=io.BytesIO()
        with tarfile.open(fileobj=stream,mode='w') as tar:
            for name in archive.FILES:tar.add(fixture/name,arcname=name)
        source=stream.getvalue()
        config=self.root/'config';config.mkdir()
        for name,text in [('source.env','SOURCE_HOST=example.test\n'),('production-recovery.env','CLUBDUE_BACKUP_HMAC_KEY=synthetic-key\n')]:
            p=config/name;p.write_text(text);p.chmod(0o600)
        class Child:
            def __init__(self,failed):self.stdout=io.BytesIO(source);self.failed=failed
            def wait(self,timeout=None):return int(self.failed)
            def poll(self):return int(self.failed)
        for failed in (True,False):
            state=Path(tempfile.mkdtemp(dir=self.root));state.chmod(0o700)
            archives=Path(tempfile.mkdtemp(dir=self.root));archives.chmod(0o700)
            marker=state/'production.last-success.json';marker.write_bytes(b'previous successful backup')
            prune=Mock()
            with patch.object(runner,'CONFIG_ROOT',config),patch.object(runner,'STATE_ROOT',state),patch.object(runner,'ARCHIVE_ROOT',archives),patch.object(runner.subprocess,'Popen',return_value=Child(failed)),patch.object(runner,'upload_drive',side_effect=ValueError('synthetic upload failure')),patch.object(runner,'prune_local',prune):
                with self.assertRaises(ValueError):runner.run('production',True)
            self.assertEqual(marker.read_bytes(),b'previous successful backup');prune.assert_not_called()

    def test_malformed_future_and_boundary_markers(self):
        now=2000000
        self.assertTrue(monitor.problem(None,'production',False,now,'g5'))
        marker={'environment':'production','destination':'g5','verified_at':now-26*3600}
        self.assertFalse(monitor.problem(marker,'production',False,now,'g5'))
        marker['verified_at']=now+301
        self.assertTrue(monitor.problem(marker,'production',False,now,'g5'))

    def test_remote_retention_only_deletes_authenticated_expired_sets(self):
        old=self.fixture(when=1)
        identity=old.name;marker=(old/'manifest.json').read_bytes();commands=[]
        class Result:
            returncode=0
            def __init__(self,data):self.stdout=data
        def remote(args,**kwargs):
            command=args[9:];commands.append(command)
            if command[0]=='lsjson' and '--dirs-only' in command:
                return Result(json.dumps([{'Name':identity},{'Name':'unrelated-folder'}]).encode())
            if command[0]=='lsjson':
                return Result(json.dumps([{'Name':name,'IsDir':False,'Size':(old/name).stat().st_size} for name in archive.FILES]).encode())
            if command[0]=='cat':return Result(marker)
            return Result(b'')
        with patch.object(runner.subprocess,'run',remote):
            runner.prune_drive('production','current',self.key,2000000)
        self.assertEqual([x for x in commands if x[0]=='purge'],[['purge','sideyard-drive:production/'+identity,'--drive-use-trash=false']])
        commands.clear()
        with patch.object(runner.subprocess,'run',remote):
            runner.prune_drive('staging','current',self.key,2000000)
        self.assertFalse(any(x[0]=='purge' for x in commands))


if __name__=='__main__':unittest.main()
