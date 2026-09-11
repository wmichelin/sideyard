#!/usr/bin/env python3
"""Failure and convergence checks for durable logical Blob publication."""
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock


HERE=Path(__file__).resolve().parent
def module(name,path):
    spec=importlib.util.spec_from_file_location(name,path); value=importlib.util.module_from_spec(spec); spec.loader.exec_module(value); return value
PUBLISH=module("logical_publisher",HERE/"publish-logical-postgres-publication.py")
STORE=PUBLISH.STORE; PREPARE=PUBLISH.PREPARE
SUBSCRIPTION="11111111-1111-1111-1111-111111111111"
GENERATION="22222222-2222-2222-2222-222222222222"
EXPORT="33333333-3333-3333-3333-333333333333"
ACCOUNT="sideyardpubtest"
STORED="Thu, 10 Sep 2026 12:00:00 GMT"


def storage_source(container):
    return {"account":ACCOUNT,"container":container,"resource_id":f"/subscriptions/{SUBSCRIPTION}/resourceGroups/sideyard-staging/providers/Microsoft.Storage/storageAccounts/{ACCOUNT}"}


class MemoryBackup:
    container="backups"
    def __init__(self):
        self.source=storage_source("backups"); self.objects={}; self.events=[]
        self.fail_after=None; self.fail_before=None; self.failed=False
        self.stored_at=STORED; self.server_now=STORED
        self.late_marker_once=False
    def _ref(self,key,data,content_type,stored_at,version="v1"):
        return {"key":key,"version":version,"sha256":hashlib.sha256(data).hexdigest(),"size":len(data),"stored_at":stored_at}
    def adopt_current(self,key,size,digest,content_type="application/octet-stream"):
        self.events.append(("head",key))
        if key not in self.objects: return None
        version,data,mime,stored=self.objects[key]
        ref=self._ref(key,data,mime,stored,version)
        if ref["size"]!=size or ref["sha256"]!=digest or mime!=content_type: raise ValueError("exact adoption collision")
        self.download(ref,None,content_type); return ref
    def put(self,key,content,size,digest,content_type="application/octet-stream",require_new=False):
        if self.fail_before and key.endswith(self.fail_before) and not self.failed:
            self.failed=True; raise OSError("simulated pre-commit transport loss")
        data=content.read(); self.events.append(("put",key))
        if key in self.objects: raise ValueError("unexpected duplicate PUT")
        stored_at=self.stored_at
        if key.endswith("completion.json") and self.late_marker_once:
            stored_at="Thu, 10 Sep 2026 13:00:00 GMT"; self.stored_at=stored_at; self.server_now=stored_at; self.late_marker_once=False
        self.objects[key]=("v1",data,content_type,stored_at)
        if self.fail_after and key.endswith(self.fail_after) and not self.failed:
            self.failed=True; raise OSError("simulated ambiguous committed PUT")
        ref=self._ref(key,data,content_type,self.stored_at)
        if ref["size"]!=size or ref["sha256"]!=digest: raise ValueError("write expectation mismatch")
        self.download(ref,None,content_type); return ref
    def download(self,ref,destination,expected_type=None):
        self.events.append(("get",ref["key"]))
        value=self.objects.get(ref["key"])
        if not value or value[0]!=ref["version"]: raise ValueError("missing exact version")
        version,data,mime,stored=value
        if expected_type and mime!=expected_type: raise ValueError("wrong content type")
        if self._ref(ref["key"],data,mime,stored,version)!=ref: raise ValueError("exact version changed")
        if destination is not None: destination.write(data)
    def current_server_time(self,ref):
        self.download(ref,None); return self.server_now


class MemoryPhotos:
    container="photos"
    def __init__(self): self.source=storage_source("photos"); self.events=[]; self.failure=None
    def audit_photo(self,asset):
        self.events.append(asset["object_key"])
        if self.failure: raise ValueError(self.failure)


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.temporary=tempfile.TemporaryDirectory(); self.addCleanup(self.temporary.cleanup)
        self.root=Path(self.temporary.name); self.folder=self.root/EXPORT; self.folder.mkdir(mode=0o700)
        self.passphrase=b"c"*64; self.authentication_key=bytes.fromhex("d"*64)
        self.photos=MemoryPhotos(); self.backup=MemoryBackup()
        asset={
            "object_key":"a"*64,"shop_id":"99999999-9999-9999-9999-999999999999","photo_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
            "content_sha256":hashlib.sha256(b"photo").hexdigest(),"content_type":"image/png","byte_length":5,"version_id":"photo-v1",
            "created_at":"2026-09-10T11:00:00.000000Z","retired_at":None,"purged_at":None,"required":True,
        }
        self.package=self.root/"package.tar"
        self.descriptor={
            "format":1,"environment":"staging","generation":GENERATION,"created":"20260910T120000Z","export_id":EXPORT,
            "release_artifact":"ghcr.io/wmichelin/clubdue@sha256:"+"b"*64,"logical_schema":"sideyard-2026-09-v31",
            "catalog_sha256":"e"*64,"root_sha256":"1"*64,"package_sha256":"0"*64,"package_bytes":1,
            "photo_source":{"kind":"azure_blob",**self.photos.source},"assets":[asset],
        }
        manifest={
            "type":"logical-package","format":1,"environment":"staging","generation":GENERATION,"source_backend":"postgres",
            "source_identity":"postgres:sideyard_staging_logical_exporter@clubdue-staging-db:5432/clubdue#revision="+"a"*40,
            "source_artifact":self.descriptor["release_artifact"],"release_artifact":self.descriptor["release_artifact"],
            "logical_schema":"sideyard-2026-09-v31","catalog_sha256":"e"*64,"migration_ledger":[],"export_id":EXPORT,
            "snapshot_started_at":"2026-09-10T12:00:00.000000Z","snapshot_completed_at":"2026-09-10T12:00:01.000000Z",
            "archive_sha256":"f"*64,"archive_bytes":2,"root_sha256":"1"*64,"photo_source":self.descriptor["photo_source"],"assets":self.descriptor["assets"],
        }
        with tarfile.open(self.package,"w",format=tarfile.USTAR_FORMAT) as archive:
            for name,data in (("manifest.json",json.dumps(manifest,separators=(",",":")).encode()),("database.ndjson",b"{}")):
                info=tarfile.TarInfo(name); info.mode=0o600; info.size=len(data); info.mtime=0; archive.addfile(info,io.BytesIO(data))
        package_bytes=self.package.read_bytes(); self.descriptor["package_sha256"]=hashlib.sha256(package_bytes).hexdigest(); self.descriptor["package_bytes"]=len(package_bytes)
        self.source={
            "format":1,"phase":"package_ready","environment":"staging","generation":GENERATION,"revision":"a"*40,
            "image":self.descriptor["release_artifact"],"attempt":"44444444-4444-4444-4444-444444444444",
            "path":f"/var/lib/sideyard-logical-export/recovery-{EXPORT}.tar","package_sha256":self.descriptor["package_sha256"],
            "package_bytes":self.descriptor["package_bytes"],"export_id":EXPORT,"root_sha256":"1"*64,
        }
        with self.package.open("rb") as package: PREPARE.seal(package,0,self.folder,self.descriptor,self.passphrase,self.authentication_key)
        PREPARE.atomic_write(self.folder/"descriptor.json",PUBLISH.canonical_file(self.descriptor))
        PREPARE.atomic_write(self.folder/"source-journal.json",PUBLISH.canonical_file(self.source))
        self.identities={
            "publisher":self.identity("logical-backup-publisher","backups","55555555-5555-5555-5555-555555555555","66666666-6666-6666-6666-666666666666","1"*64),
            "photo_reader":self.identity("logical-photo-reader","photos","77777777-7777-7777-7777-777777777777","88888888-8888-8888-8888-888888888888","2"*64),
        }
    def identity(self,purpose,container,client,principal,fingerprint):
        return {
            "SIDEYARD_RECOVERY_PURPOSE":purpose,"AZURE_SUBSCRIPTION_ID":SUBSCRIPTION,"AZURE_TENANT_ID":"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
            "AZURE_CLIENT_ID":client,"AZURE_PRINCIPAL_OBJECT_ID":principal,"AZURE_CERTIFICATE_SHA256":fingerprint,
            "AZURE_CREDENTIAL_KEY_ID":client,"AZURE_CREDENTIAL_EXPIRES":"2027-09-10T00:00:00Z",
            "AZURE_BLOB_CONTAINER_RESOURCE_ID":storage_source(container)["resource_id"]+f"/blobServices/default/containers/{container}",
        }
    def publish(self,verify=lambda *_:None):
        return PUBLISH.run_publication(self.folder,self.backup,self.photos,self.identities,self.passphrase,self.authentication_key,verify)
    def test_publishes_marker_last_and_writes_authenticated_receipt(self):
        receipt=self.publish(PUBLISH.verify_remote_recovery)
        puts=[key for action,key in self.backup.events if action=="put"]
        self.assertTrue(puts[-1].endswith("completion.json")); self.assertEqual(len(puts),3)
        self.assertEqual(receipt,PUBLISH.read_canonical(self.folder/"completion-receipt.json"))
        self.assertFalse(any(path.name.startswith("verification-") for path in self.folder.iterdir()))
        self.publish(PUBLISH.verify_remote_recovery); self.assertEqual(len([e for e in self.backup.events if e[0]=="put"]),3)
    def test_ambiguous_component_commit_converges_without_duplicates(self):
        self.backup.fail_after="recovery.tar.enc"
        with self.assertRaisesRegex(OSError,"ambiguous"): self.publish()
        self.publish(); self.assertEqual(len(self.backup.objects),3)
        self.assertTrue((self.folder/"completion-receipt.json").is_file())
    def test_ambiguous_marker_commit_converges_without_duplicates(self):
        self.backup.fail_after="completion.json"
        with self.assertRaisesRegex(OSError,"ambiguous"): self.publish()
        self.identities["photo_reader"]["AZURE_CLIENT_ID"]="18181818-1818-1818-1818-181818181818"
        self.identities["photo_reader"]["AZURE_PRINCIPAL_OBJECT_ID"]="19191919-1919-1919-1919-191919191919"
        self.identities["photo_reader"]["AZURE_CREDENTIAL_KEY_ID"]="20202020-2020-2020-2020-202020202020"
        self.identities["photo_reader"]["AZURE_CERTIFICATE_SHA256"]="5"*64
        self.publish(); self.assertEqual(len(self.backup.objects),3)
        self.assertEqual(len([event for event in self.backup.events if event[0]=="put"]),3)
    def test_precommit_failure_retries_without_marker(self):
        self.backup.fail_before="recovery.tar.enc"
        with self.assertRaisesRegex(OSError,"pre-commit"): self.publish()
        self.assertFalse(any(key.endswith("completion.json") for key in self.backup.objects))
        self.backup.fail_before=None; self.backup.failed=False; self.publish()
    def test_photo_failure_writes_nothing(self):
        self.photos.failure="missing exact photo"
        with self.assertRaisesRegex(ValueError,"missing exact photo"): self.publish()
        self.assertEqual(self.backup.objects,{})
    def test_collision_and_tampered_journal_fail_closed(self):
        publication="99999999-9999-9999-9999-999999999999"
        _,keys,_=STORE.publication_keys(self.descriptor,publication)
        self.backup.objects[keys[0]]=("v1",b"foreign","application/octet-stream",STORED)
        journal=PUBLISH.new_journal(self.folder,self.descriptor,self.source,self.backup,self.photos,self.identities,publication)
        PUBLISH.write_state(self.folder/"publication-journal.json",journal)
        with self.assertRaisesRegex(ValueError,"collision"): self.publish()
        self.assertFalse(any(key.endswith("completion.json") for key in self.backup.objects))
        journal["descriptor_sha256"]="0"*64; PUBLISH.write_state(self.folder/"publication-journal.json",journal)
        with self.assertRaisesRegex(ValueError,"does not match"): self.publish()
    def test_aged_incomplete_attempt_rolls_to_fresh_publication_namespace(self):
        self.backup.fail_before="completion.json"
        with self.assertRaises(OSError): self.publish()
        first=PUBLISH.read_canonical(self.folder/"publication-journal.json")["publication_id"]
        self.backup.fail_before=None; self.backup.failed=False
        self.backup.server_now="Thu, 10 Sep 2026 12:55:00 GMT"; self.backup.stored_at=self.backup.server_now
        receipt=self.publish(); second=receipt["payload"]["publication_id"]
        self.assertNotEqual(first,second)
        self.assertTrue((self.folder/f"publication-abandoned-{first}.json").is_file())
        self.assertEqual(len([key for key in self.backup.objects if key.endswith("completion.json")]),1)
    def test_identity_reuse_fails_before_network(self):
        self.identities["photo_reader"]["AZURE_CLIENT_ID"]=self.identities["publisher"]["AZURE_CLIENT_ID"]
        with self.assertRaisesRegex(ValueError,"must be distinct"): self.publish()
        self.assertEqual(self.backup.events,[])
    def test_certificate_rotation_does_not_block_ambiguous_resume(self):
        self.backup.fail_after="recovery.tar.enc"
        with self.assertRaises(OSError): self.publish()
        self.identities["publisher"]["AZURE_CLIENT_ID"]="12121212-1212-1212-1212-121212121212"
        self.identities["publisher"]["AZURE_PRINCIPAL_OBJECT_ID"]="13131313-1313-1313-1313-131313131313"
        self.identities["publisher"]["AZURE_CREDENTIAL_KEY_ID"]="14141414-1414-1414-1414-141414141414"
        self.identities["publisher"]["AZURE_CERTIFICATE_SHA256"]="3"*64
        receipt=self.publish(); self.assertEqual(receipt["payload"]["verified_by"]["publisher"],self.identities["publisher"])
        self.identities["photo_reader"]["AZURE_CLIENT_ID"]="15151515-1515-1515-1515-151515151515"
        self.identities["photo_reader"]["AZURE_PRINCIPAL_OBJECT_ID"]="16161616-1616-1616-1616-161616161616"
        self.identities["photo_reader"]["AZURE_CREDENTIAL_KEY_ID"]="17171717-1717-1717-1717-171717171717"
        self.identities["photo_reader"]["AZURE_CERTIFICATE_SHA256"]="4"*64
        self.publish()
    def test_late_authenticated_marker_rolls_to_a_fresh_attempt(self):
        self.backup.server_now="Thu, 10 Sep 2026 12:54:59 GMT"; self.backup.late_marker_once=True
        receipt=self.publish(); publication=receipt["payload"]["publication_id"]
        archives=list(self.folder.glob("publication-abandoned-*.json")); self.assertEqual(len(archives),1)
        abandoned=PUBLISH.read_canonical(archives[0]); self.assertNotEqual(abandoned["publication_id"],publication)
        self.assertEqual(len([key for key in self.backup.objects if key.endswith("completion.json")]),2)
    def test_ambiguous_late_marker_is_adopted_then_rolled_over(self):
        self.backup.server_now="Thu, 10 Sep 2026 12:54:59 GMT"; self.backup.late_marker_once=True; self.backup.fail_after="completion.json"
        with self.assertRaisesRegex(OSError,"ambiguous"): self.publish()
        receipt=self.publish()
        archives=list(self.folder.glob("publication-abandoned-*.json")); self.assertEqual(len(archives),1)
        self.assertEqual(len([key for key in self.backup.objects if key.endswith("completion.json")]),2)
        self.assertEqual(PUBLISH.read_canonical(self.folder/"completion-receipt.json"),receipt)
    def test_stale_atomic_file_is_removed_but_unsafe_one_is_rejected(self):
        stale=self.folder/".publication-journal.json.new"; stale.write_text("interrupted"); stale.chmod(0o600)
        self.publish(); self.assertFalse(stale.exists())
        (self.folder/"completion-receipt.json").unlink(); (self.folder/"publication-journal.json").unlink()
        outside=self.root/"outside"; outside.write_text("unsafe")
        stale.symlink_to(outside)
        with self.assertRaises(ValueError): self.publish()
    def test_verification_space_boundary_fails_before_retrieval(self):
        receipt=self.publish()
        marker=receipt["payload"]["completion"]
        required=2*STORE.MAX_CIPHERTEXT+STORE.MAX_PACKAGE+64*1024*1024
        with mock.patch.object(PUBLISH.shutil,"disk_usage",return_value=mock.Mock(free=required-1)):
            with self.assertRaisesRegex(ValueError,"insufficient free space"):
                PUBLISH.verify_remote_recovery(self.backup,marker,self.folder,self.authentication_key,self.passphrase,self.descriptor,self.source)
    def test_unexpected_publication_folder_content_fails_before_network(self):
        extra=self.folder/"unreviewed"; extra.write_text("unexpected"); extra.chmod(0o600)
        with self.assertRaisesRegex(ValueError,"unexpected contents"): self.publish()
        self.assertEqual(self.backup.events,[])
    def test_terraform_runner_binding_requires_exact_private_certificate_pair(self):
        config=self.root/"config"; config.mkdir(mode=0o700)
        pfx=config/"staging-terraform.pfx"; pem=config/"staging-terraform.pem"
        for path in (pfx,pem): path.write_text("certificate"); path.chmod(0o600)
        values={
            "ARM_CLIENT_ID":"11111111-1111-1111-1111-111111111111","ARM_TENANT_ID":"22222222-2222-2222-2222-222222222222",
            "ARM_SUBSCRIPTION_ID":"33333333-3333-3333-3333-333333333333","ARM_CLIENT_CERTIFICATE_PATH":str(pfx),"ARM_USE_AZUREAD":"true",
        }
        env=config/"staging-terraform.env"; env.write_text("".join(f"{key}={value}\n" for key,value in values.items())); env.chmod(0o600)
        self.assertEqual(PUBLISH.terraform_runner_values(config),values)
        self.assertEqual(PUBLISH.terraform_runner_cli_certificate(values,config),pem)
        pfx.chmod(0o644)
        with self.assertRaises(SystemExit): PUBLISH.terraform_runner_values(config)

    def test_certificate_environment_drops_ambient_credentials_and_pins_wrapper(self):
        runner={
            "ARM_CLIENT_ID":"11111111-1111-1111-1111-111111111111","ARM_TENANT_ID":"22222222-2222-2222-2222-222222222222",
            "ARM_SUBSCRIPTION_ID":"33333333-3333-3333-3333-333333333333","ARM_CLIENT_CERTIFICATE_PATH":"/protected/staging-terraform.pfx","ARM_USE_AZUREAD":"true",
        }
        config=self.root/"runner"; config.mkdir(mode=0o700)
        ambient={"PATH":"/untrusted/bin:/usr/bin","ARM_CLIENT_SECRET":"SENTINEL-ARM","AZURE_CLIENT_SECRET":"SENTINEL-AZURE","AZURE_FEDERATED_TOKEN_FILE":"/sentinel-token","DOCKER_HOST":"tcp://sentinel.invalid:2375","DOCKER_CONTEXT":"sentinel-remote","HOME":str(self.root)}
        with mock.patch.dict(PUBLISH.os.environ,ambient,clear=True):
            environment=PUBLISH.certificate_only_environment(runner,config)
            self.assertEqual(PUBLISH.azure_cli_path(),str(PUBLISH.ROOT/"scripts"/"azure"/"bin"/"az"))
        self.assertEqual(environment["ARM_CLIENT_ID"],runner["ARM_CLIENT_ID"])
        self.assertEqual(environment["AZURE_CONFIG_DIR"],str(config))
        self.assertTrue(environment["PATH"].startswith(str(PUBLISH.ROOT/"scripts"/"azure"/"bin")+os.pathsep))
        self.assertNotIn("ARM_CLIENT_SECRET",environment)
        self.assertNotIn("AZURE_CLIENT_SECRET",environment)
        self.assertNotIn("AZURE_FEDERATED_TOKEN_FILE",environment)
        self.assertNotIn("DOCKER_HOST",environment)
        self.assertNotIn("DOCKER_CONTEXT",environment)

    def test_review_gate_includes_azure_cli_wrapper(self):
        source=PUBLISH.reviewed_tooling.__code__.co_consts
        self.assertTrue(any(isinstance(value,tuple) and "scripts/azure/bin/az" in value for value in source))
        self.assertTrue(any(isinstance(value,tuple) and "scripts/azure/logical-export-handoff.sh" in value for value in source))

    def test_review_gate_rejects_modified_release_helper(self):
        root=self.root/"repo"; paths=("scripts/azure/publish-logical-postgres-publication.py","scripts/azure/prepare-logical-postgres-publication.py","scripts/azure/logical-backup-store.py","scripts/azure/verify-recovery-certificate.py","scripts/azure/bin/az","scripts/azure/logical-export-handoff.sh")
        reviewed={}
        for name in paths:
            path=root/name; path.parent.mkdir(parents=True,exist_ok=True); path.write_bytes((name+"\n").encode()); reviewed[name]=path.read_bytes()
        (root/paths[-1]).write_bytes(b"modified destructive helper\n")
        def git(command,**_):
            if command[1:]==["rev-parse","HEAD"]: return mock.Mock(stdout="a"*40+"\n",returncode=0)
            return mock.Mock(stdout=reviewed[command[2].removeprefix("HEAD:")],returncode=0)
        with mock.patch.object(PUBLISH,"ROOT",root),mock.patch.object(PUBLISH.subprocess,"run",side_effect=git):
            with self.assertRaisesRegex(ValueError,"reviewed revision"): PUBLISH.reviewed_tooling()

    def test_azure_cli_wrapper_pins_local_docker(self):
        wrapper=(PUBLISH.ROOT/"scripts"/"azure"/"bin"/"az").read_text()
        self.assertTrue(wrapper.startswith("#!/bin/bash\n"))
        self.assertIn("exec /usr/bin/docker --host unix:///var/run/docker.sock run",wrapper)
        self.assertNotIn("exec docker ",wrapper)

    def test_explicit_host_release_persists_authenticated_receipt_and_is_idempotent(self):
        publication=self.publish(PUBLISH.verify_remote_recovery); calls=[]
        def release(target,authorization,helper):
            calls.append((target,authorization,helper))
            serialized=PUBLISH.canonical_file(authorization)
            self.assertNotIn(self.passphrase,serialized); self.assertNotIn(self.authentication_key.hex().encode(),serialized)
            return {"environment":"staging","export_id":EXPORT,"package_sha256":self.descriptor["package_sha256"],"phase":"released","release_id":authorization["release_id"]}
        result=PUBLISH.release_host_package(self.folder,publication,self.authentication_key,b"reviewed helper",transport=release)
        self.assertEqual(result["phase"],"released"); self.assertEqual(len(calls),1)
        self.assertFalse((self.folder/"host-release-intent.json").exists()); self.assertTrue((self.folder/"host-release-receipt.json").exists())
        release_document=PUBLISH.read_canonical(self.folder/"host-release-receipt.json")
        PUBLISH.write_state(self.folder/"host-release-intent.json",release_document["payload"]["authorization"])
        result2=PUBLISH.release_host_package(self.folder,publication,self.authentication_key,b"reviewed helper",transport=lambda *_:self.fail("unexpected second SSH"))
        self.assertEqual(result2,result)
        self.assertFalse((self.folder/"host-release-intent.json").exists())

    def test_host_release_transport_failure_retains_exact_intent(self):
        publication=self.publish(PUBLISH.verify_remote_recovery)
        with self.assertRaisesRegex(OSError,"lost acknowledgement"):
            PUBLISH.release_host_package(self.folder,publication,self.authentication_key,b"reviewed helper",transport=lambda *_:(_ for _ in ()).throw(OSError("lost acknowledgement")))
        intent=PUBLISH.read_canonical(self.folder/"host-release-intent.json")
        self.assertEqual(intent["source"],self.source); self.assertEqual(intent["completion"],publication["payload"]["completion"])
        tampered=dict(intent,receipt_sha256="0"*64); PUBLISH.write_state(self.folder/"host-release-intent.json",tampered)
        with self.assertRaisesRegex(ValueError,"does not match"):
            PUBLISH.release_host_package(self.folder,publication,self.authentication_key,b"reviewed helper",transport=lambda *_:None)


if __name__=="__main__": unittest.main()
