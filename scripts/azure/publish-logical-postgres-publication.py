#!/usr/bin/env python3
"""Resume one sealed staging logical backup into exact versioned Azure objects."""
import argparse
import base64
import hashlib
import hmac
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import selectors
import shutil
import signal
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid


HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module


STORE = load_module("sideyard_logical_store", HERE / "logical-backup-store.py")
PREPARE = load_module("sideyard_logical_prepare", HERE / "prepare-logical-postgres-publication.py")
CERT = load_module("sideyard_recovery_certificate", HERE / "verify-recovery-certificate.py")
RECEIPT_MAGIC = b"Sideyard logical publication receipt v1\n"
HOST_RELEASE_MAGIC = b"Sideyard logical host release receipt v1\n"
PHASES = ("intent", "ciphertext", "components", "completion", "verified")
UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")
IDENTITY_FIELDS = {
    "SIDEYARD_RECOVERY_PURPOSE", "AZURE_SUBSCRIPTION_ID", "AZURE_TENANT_ID",
    "AZURE_CLIENT_ID", "AZURE_PRINCIPAL_OBJECT_ID", "AZURE_CERTIFICATE_SHA256",
    "AZURE_CREDENTIAL_KEY_ID", "AZURE_CREDENTIAL_EXPIRES", "AZURE_BLOB_CONTAINER_RESOURCE_ID",
}
AUTHORITY_FIELDS = {
    "SIDEYARD_RECOVERY_PURPOSE", "AZURE_SUBSCRIPTION_ID", "AZURE_TENANT_ID",
    "AZURE_BLOB_CONTAINER_RESOURCE_ID",
}


def canonical_file(value):
    return STORE.canonical(value) + b"\n"


def read_canonical(path):
    PREPARE.private_path(path)
    raw = path.read_bytes()
    try:
        value = json.loads(raw)
    except json.JSONDecodeError:
        raise ValueError(f"{path.name} is invalid") from None
    if raw != canonical_file(value):
        raise ValueError(f"{path.name} is not canonical")
    return value


def inspect_publication_folder(folder):
    PREPARE.private_path(folder, directory=True)
    regular = {
        "recovery.tar.enc", "recovery.tar.enc.mac", "descriptor.json", "source-journal.json",
        "publication-journal.json", "completion-receipt.json",
        "host-release-intent.json", "host-release-receipt.json",
        ".publication-journal.json.new", ".completion-receipt.json.new",
        ".host-release-intent.json.new", ".host-release-receipt.json.new",
    }
    for path in folder.iterdir():
        abandoned = path.name.removeprefix("publication-abandoned-").removesuffix(".json")
        if path.name in regular or (path.name.startswith("publication-abandoned-") and path.name.endswith(".json") and UUID.fullmatch(abandoned)):
            PREPARE.private_path(path)
            continue
        if re.fullmatch(r"verification-[0-9a-f]{16}", path.name):
            PREPARE.private_path(path, directory=True); STORE.inspect_private_restore(path)
            continue
        if re.fullmatch(r"\.verification-([0-9a-f]{16})\.sideyard-\1\.partial", path.name):
            PREPARE.private_path(path, directory=True)
            for item in path.iterdir():
                if item.name not in {".recovery.partial", ".recovery.mac.partial", "sideyard-recovery.tar.enc", "sideyard-recovery.tar.enc.mac"}:
                    raise ValueError("logical publication verification staging has unexpected contents")
                PREPARE.private_path(item)
            continue
        raise ValueError("logical publication folder has unexpected contents")


def source_journal_matches(source, descriptor):
    if not isinstance(source, dict):
        raise ValueError("sealed source journal is invalid")
    PREPARE.validate_journal(source, source.get("revision", ""))
    if (
        source["generation"] != descriptor["generation"]
        or source["export_id"] != descriptor["export_id"]
        or source["image"] != descriptor["release_artifact"]
        or source["root_sha256"] != descriptor["root_sha256"]
        or source["package_sha256"] != descriptor["package_sha256"]
        or source["package_bytes"] != descriptor["package_bytes"]
    ):
        raise ValueError("sealed source journal does not match its descriptor")


def identity_record(values):
    fields = (
        "SIDEYARD_RECOVERY_PURPOSE", "AZURE_SUBSCRIPTION_ID", "AZURE_TENANT_ID",
        "AZURE_CLIENT_ID", "AZURE_PRINCIPAL_OBJECT_ID", "AZURE_CERTIFICATE_SHA256",
        "AZURE_CREDENTIAL_KEY_ID", "AZURE_CREDENTIAL_EXPIRES",
        "AZURE_BLOB_CONTAINER_RESOURCE_ID",
    )
    record = {name: values.get(name, "") for name in fields}
    if any(not value for value in record.values()):
        raise ValueError("recovery identity binding is incomplete")
    return record


def authority_record(identity):
    return {name: identity[name] for name in AUTHORITY_FIELDS}


def validate_identities(identities, backup, photos, descriptor):
    if not isinstance(identities, dict) or set(identities) != {"publisher", "photo_reader"}:
        raise ValueError("logical recovery identities are invalid")
    publisher, reader = identities["publisher"], identities["photo_reader"]
    if not all(isinstance(value, dict) and set(value) == IDENTITY_FIELDS for value in (publisher, reader)):
        raise ValueError("logical recovery identity record is invalid")
    if publisher["SIDEYARD_RECOVERY_PURPOSE"] != "logical-backup-publisher" or reader["SIDEYARD_RECOVERY_PURPOSE"] != "logical-photo-reader":
        raise ValueError("logical recovery identity purpose is invalid")
    if publisher["AZURE_SUBSCRIPTION_ID"] != reader["AZURE_SUBSCRIPTION_ID"] or publisher["AZURE_TENANT_ID"] != reader["AZURE_TENANT_ID"]:
        raise ValueError("logical recovery identities do not share their bound Azure context")
    for field in ("AZURE_CLIENT_ID", "AZURE_PRINCIPAL_OBJECT_ID", "AZURE_CREDENTIAL_KEY_ID", "AZURE_CERTIFICATE_SHA256"):
        if publisher[field].lower() == reader[field].lower():
            raise ValueError("logical recovery publisher and photo reader must be distinct")
    expected_backup = backup.source["resource_id"] + "/blobServices/default/containers/backups"
    expected_photos = photos.source["resource_id"] + "/blobServices/default/containers/photos"
    if publisher["AZURE_BLOB_CONTAINER_RESOURCE_ID"].lower() != expected_backup.lower() or reader["AZURE_BLOB_CONTAINER_RESOURCE_ID"].lower() != expected_photos.lower():
        raise ValueError("logical recovery identity container binding is invalid")
    if photos.source != {key: descriptor["photo_source"][key] for key in ("account", "container", "resource_id")}:
        raise ValueError("logical recovery photo client does not match the descriptor")


def local_spec(path, key):
    PREPARE.private_path(path)
    size = path.stat().st_size
    return {"name": path.name, "key": key, "sha256": STORE.file_digest(path).hex(), "size": size, "ref": None}


def new_journal(folder, descriptor, source, backup, photos, identities, publication_id=None):
    publication_id = publication_id or str(uuid.uuid4())
    _, component_keys, completion_key = STORE.publication_keys(descriptor, publication_id)
    return {
        "format": 1, "phase": "intent", "publication_id": publication_id,
        "environment": "staging", "generation": descriptor["generation"], "export_id": descriptor["export_id"],
        "descriptor_sha256": hashlib.sha256(canonical_file(descriptor)).hexdigest(),
        "source_journal_sha256": hashlib.sha256(canonical_file(source)).hexdigest(),
        "backup_source": backup.source, "photo_source": photos.source,
        "publisher_authority": authority_record(identities["publisher"]),
        "photo_reader_authority": authority_record(identities["photo_reader"]),
        "components": [
            local_spec(folder / "recovery.tar.enc", component_keys[0]),
            local_spec(folder / "recovery.tar.enc.mac", component_keys[1]),
        ],
        "completion": {"key": completion_key, "sha256": None, "size": None, "ref": None},
    }


def validate_ref_or_none(ref, suffix, maximum):
    if ref is not None:
        STORE.validate_ref(ref, suffix, maximum)


def validate_journal(journal, expected):
    fields = {
        "format", "phase", "publication_id", "environment", "generation", "export_id",
        "descriptor_sha256", "source_journal_sha256", "backup_source", "photo_source",
        "publisher_authority", "photo_reader_authority", "components", "completion",
    }
    if not isinstance(journal, dict) or set(journal) != fields or journal.get("format") != 1 or journal.get("phase") not in PHASES:
        raise ValueError("publication journal has unknown, missing or invalid fields")
    for field in fields - {"phase", "components", "completion"}:
        if journal[field] != expected[field]:
            raise ValueError("publication journal does not match the sealed recovery attempt")
    if not isinstance(journal["components"], list) or len(journal["components"]) != 2:
        raise ValueError("publication journal component state is invalid")
    for actual, wanted, suffix, maximum in zip(
        journal["components"], expected["components"],
        ("recovery.tar.enc", "recovery.tar.enc.mac"), (STORE.MAX_CIPHERTEXT, len(STORE.PAIR_MAGIC) + 64),
    ):
        if not isinstance(actual, dict) or set(actual) != set(wanted) or any(actual[name] != wanted[name] for name in wanted if name != "ref"):
            raise ValueError("publication journal component binding changed")
        validate_ref_or_none(actual["ref"], suffix, maximum)
        if actual["ref"] is not None and (
            actual["ref"]["key"] != actual["key"] or actual["ref"]["sha256"] != actual["sha256"] or actual["ref"]["size"] != actual["size"]
        ):
            raise ValueError("publication journal component reference changed")
    completion = journal["completion"]
    if not isinstance(completion, dict) or set(completion) != {"key", "sha256", "size", "ref"} or completion["key"] != expected["completion"]["key"]:
        raise ValueError("publication journal completion binding changed")
    if completion["sha256"] is None:
        if completion["size"] is not None or completion["ref"] is not None:
            raise ValueError("publication journal completion state is invalid")
    else:
        if not STORE.SHA256.fullmatch(completion["sha256"]) or type(completion["size"]) is not int:
            raise ValueError("publication journal completion expectation is invalid")
        validate_ref_or_none(completion["ref"], "completion.json", STORE.MAX_COMPLETION)
        if completion["ref"] is not None and (
            completion["ref"]["key"] != completion["key"] or completion["ref"]["sha256"] != completion["sha256"] or completion["ref"]["size"] != completion["size"]
        ):
            raise ValueError("publication journal completion reference changed")
    return journal


def write_state(path, value):
    stale = path.with_name("." + path.name + ".new")
    if stale.exists() or stale.is_symlink():
        PREPARE.private_path(stale); stale.unlink(); PREPARE.fsync_directory(path.parent)
    PREPARE.atomic_write(path, canonical_file(value))
    return read_canonical(path)


def update_state(path, journal, **changes):
    updated = dict(journal); updated.update(changes)
    return write_state(path, updated)


def phase_index(value):
    return PHASES.index(value)


def ensure_phase(journal, phase):
    if phase_index(journal["phase"]) < phase_index(phase):
        journal["phase"] = phase
    return journal


def deadline_check(deadline):
    if time.monotonic() >= deadline:
        raise ValueError("logical publication exceeded its bounded 50-minute window")


def audit_photos(photos, descriptor, deadline):
    for asset in descriptor["assets"]:
        if asset["required"]:
            deadline_check(deadline); photos.audit_photo(asset)


def reconcile_component(backup, folder, spec, deadline):
    deadline_check(deadline)
    if spec["ref"] is not None:
        backup.download(spec["ref"], None, "application/octet-stream")
        return spec["ref"]
    adopted = backup.adopt_current(spec["key"], spec["size"], spec["sha256"], "application/octet-stream")
    if adopted is not None:
        return adopted
    deadline_check(deadline)
    with (folder / spec["name"]).open("rb") as source:
        return backup.put(spec["key"], source, spec["size"], spec["sha256"], "application/octet-stream")


def expected_completion(backup, descriptor, journal, authentication_key):
    refs = [component["ref"] for component in journal["components"]]
    payload = STORE.completion_payload(backup, descriptor, refs, journal["publication_id"])
    marker = STORE.seal_completion(payload, authentication_key)
    return payload, marker, hashlib.sha256(marker).hexdigest()


def reconcile_marker(backup, journal, marker, digest, deadline):
    completion = dict(journal["completion"])
    if completion["sha256"] is None:
        completion.update({"sha256": digest, "size": len(marker)})
    elif completion["sha256"] != digest or completion["size"] != len(marker):
        raise ValueError("publication completion expectation changed")
    if completion["ref"] is not None:
        target = io.BytesIO(); backup.download(completion["ref"], target, "application/json")
        if target.getvalue() != marker:
            raise ValueError("published completion marker changed")
        return completion
    deadline_check(deadline)
    adopted = backup.adopt_current(completion["key"], len(marker), digest, "application/json")
    if adopted is None:
        adopted = backup.put(completion["key"], io.BytesIO(marker), len(marker), digest, "application/json")
    completion["ref"] = adopted
    return completion


def abandon_attempt(folder, path, journal):
    publication_id = journal["publication_id"]
    archive = folder / f"publication-abandoned-{publication_id}.json"
    if archive.exists():
        raise ValueError("publication attempt archive already exists")
    os.replace(path, archive); PREPARE.fsync_directory(folder)


def receipt_document(journal, descriptor, source, identities, authentication_key):
    payload = {
        "format": 1, "publication_id": journal["publication_id"], "descriptor": descriptor,
        "source_journal": source, "backup_source": journal["backup_source"],
        "photo_source": journal["photo_source"], "files": [item["ref"] for item in journal["components"]],
        "completion": journal["completion"]["ref"], "verified_by": identities,
    }
    signature = hmac.new(authentication_key, RECEIPT_MAGIC + STORE.canonical(payload), hashlib.sha256).hexdigest()
    return {"payload": payload, "hmac": signature}


def validate_receipt(document, expected, authentication_key):
    if not isinstance(document, dict) or set(document) != {"payload", "hmac"}:
        raise ValueError("logical publication receipt is invalid")
    signature = hmac.new(authentication_key, RECEIPT_MAGIC + STORE.canonical(document["payload"]), hashlib.sha256).hexdigest()
    actual_payload = document.get("payload")
    expected_payload = expected.get("payload")
    actual_core = dict(actual_payload) if isinstance(actual_payload, dict) else {}
    expected_core = dict(expected_payload) if isinstance(expected_payload, dict) else {}
    actors = actual_core.pop("verified_by", None); expected_core.pop("verified_by", None)
    valid_actors = isinstance(actors, dict) and set(actors) == {"publisher", "photo_reader"} and all(
        isinstance(value, dict) and set(value) == IDENTITY_FIELDS for value in actors.values()
    )
    if not hmac.compare_digest(str(document["hmac"]), signature) or not valid_actors or actual_core != expected_core:
        raise ValueError("logical publication receipt authentication failed")


def verify_remote_recovery(backup, marker_ref, folder, authentication_key, passphrase, descriptor, source):
    required = 2 * STORE.MAX_CIPHERTEXT + STORE.MAX_PACKAGE + 64 * 1024 * 1024
    if shutil.disk_usage(folder).free < required:
        raise ValueError("logical recovery verification has insufficient free space")
    verification = folder / f"verification-{marker_ref['sha256'][:16]}"
    if not verification.exists(): verification.mkdir(mode=0o700)
    PREPARE.private_path(verification, directory=True)
    recovered = STORE.retrieve(backup, marker_ref, verification, authentication_key, descriptor)
    read_fd, write_fd = os.pipe()
    process = None
    try:
        os.write(write_fd, passphrase + b"\n"); os.close(write_fd); write_fd = -1
        with tempfile.TemporaryFile(dir=folder) as package, tempfile.TemporaryFile(dir=folder) as diagnostics:
            process = subprocess.Popen(
                ["openssl", "enc", "-d", "-aes-256-cbc", "-pbkdf2", "-iter", "200000", "-md", "sha256", "-pass", f"fd:{read_fd}", "-in", str(recovered)],
                stdout=subprocess.PIPE, stderr=diagnostics, pass_fds=(read_fd,), start_new_session=True,
            )
            digest=hashlib.sha256(); size=0
            while True:
                block=process.stdout.read(1024*1024)
                if not block: break
                size += len(block)
                if size > descriptor["package_bytes"]:
                    os.killpg(process.pid, signal.SIGKILL); process.wait()
                    raise ValueError("retrieved logical package exceeds its descriptor")
                digest.update(block); package.write(block)
            process.stdout.close()
            if process.wait() or size != descriptor["package_bytes"] or digest.hexdigest() != descriptor["package_sha256"]:
                raise ValueError("retrieved logical package does not decrypt to its descriptor")
            package.flush(); package.seek(0)
            if PREPARE.read_manifest(package, 0, source) != descriptor:
                raise ValueError("retrieved logical package manifest changed")
    finally:
        if process is not None and process.poll() is None:
            try: os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError: pass
            process.wait()
        if process is not None and process.stdout is not None and not process.stdout.closed: process.stdout.close()
        if write_fd >= 0: os.close(write_fd)
        os.close(read_fd)
    for item in verification.iterdir():
        PREPARE.private_path(item); item.unlink()
    verification.rmdir(); PREPARE.fsync_directory(folder)


def run_publication(folder, backup, photos, identities, passphrase, authentication_key, verify=verify_remote_recovery):
    inspect_publication_folder(folder)
    descriptor = STORE.validate_descriptor(read_canonical(folder / "descriptor.json"))
    source = read_canonical(folder / "source-journal.json")
    source_journal_matches(source, descriptor)
    ciphertext = folder / "recovery.tar.enc"
    STORE.verify_pair(ciphertext, authentication_key, descriptor)
    PREPARE.verify_decryption(ciphertext, descriptor, passphrase, folder)
    validate_identities(identities, backup, photos, descriptor)
    path = folder / "publication-journal.json"
    if (folder / "completion-receipt.json").exists() and not path.exists():
        raise ValueError("logical publication receipt is missing its durable journal")
    deadline = time.monotonic() + 50 * 60
    for _ in range(2):
        if path.exists():
            current = read_canonical(path)
            expected = new_journal(folder, descriptor, source, backup, photos, identities, current.get("publication_id"))
            journal = validate_journal(current, expected)
        else:
            journal = write_state(path, new_journal(folder, descriptor, source, backup, photos, identities))
        audit_photos(photos, descriptor, deadline)
        for index in range(2):
            ref = reconcile_component(backup, folder, journal["components"][index], deadline)
            journal["components"][index]["ref"] = ref
            phase = "ciphertext" if index == 0 else "components"
            journal = update_state(path, ensure_phase(journal, phase))
        refs = [item["ref"] for item in journal["components"]]
        STORE.verify_remote_pair(backup, refs, authentication_key, descriptor)
        payload, marker, digest = expected_completion(backup, descriptor, journal, authentication_key)
        completion = dict(journal["completion"])
        completion.update({"sha256": digest, "size": len(marker)})
        journal["completion"] = completion
        journal = update_state(path, ensure_phase(journal, "completion"))
        adopted = backup.adopt_current(completion["key"], len(marker), digest, "application/json")
        if adopted is None:
            try:
                STORE.validate_completion_start(refs, backup.current_server_time(refs[-1]))
            except ValueError as error:
                if "insufficient retention-safe" not in str(error): raise
                abandon_attempt(folder, path, journal)
                continue
        completion = reconcile_marker(backup, journal, marker, digest, deadline)
        journal["completion"] = completion
        journal = update_state(path, journal)
        marker_data=io.BytesIO(); backup.download(completion["ref"],marker_data,"application/json")
        binding=dict(descriptor,asset_count=len(descriptor["assets"]),required_asset_count=len([a for a in descriptor["assets"] if a["required"]]),asset_root_sha256=STORE.asset_root(descriptor["assets"]))
        opened=STORE.open_completion(marker_data.getvalue(),authentication_key,binding,backup)
        if opened != payload: raise ValueError("authenticated completion payload changed")
        try:
            STORE.validate_publication_span(refs,completion["ref"])
        except ValueError as error:
            if "retention-safe window" not in str(error): raise
            abandon_attempt(folder,path,journal)
            continue
        journal = update_state(path, ensure_phase(journal, "verified"))
        STORE.verify_remote_pair(backup,refs,authentication_key,descriptor)
        audit_photos(photos,descriptor,deadline)
        verify(backup,completion["ref"],folder,authentication_key,passphrase,descriptor,source)
        receipt=receipt_document(journal,descriptor,source,identities,authentication_key)
        receipt_path=folder/"completion-receipt.json"
        if receipt_path.exists():
            existing=read_canonical(receipt_path); validate_receipt(existing,receipt,authentication_key); receipt=existing
        else: write_state(receipt_path,receipt); validate_receipt(read_canonical(receipt_path),receipt,authentication_key)
        return receipt
    raise ValueError("logical publication could not establish a fresh retention-safe attempt")


def release_over_ssh(target, authorization, helper_source):
    PREPARE.validate_ssh_target(target)
    ssh=shutil.which("ssh")
    if not ssh: raise ValueError("OpenSSH is required for logical source release")
    encoded=base64.urlsafe_b64encode(canonical_file(authorization)).decode()
    if len(encoded)>32768: raise ValueError("logical source release authorization is too large")
    command=[ssh,"-o","BatchMode=yes","-o","StrictHostKeyChecking=yes","-o","ConnectTimeout=10","-o","ServerAliveInterval=10","-o","ServerAliveCountMax=3",target,f"bash -s -- staging release {encoded}"]
    process=subprocess.Popen(command,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
    output=bytearray(); diagnostics=bytearray(); deadline=time.monotonic()+180
    try:
        process.stdin.write(helper_source); process.stdin.close()
        streams=selectors.DefaultSelector(); streams.register(process.stdout,selectors.EVENT_READ,"stdout"); streams.register(process.stderr,selectors.EVENT_READ,"stderr")
        while streams.get_map():
            remaining=deadline-time.monotonic()
            if remaining<=0: raise ValueError("logical source release exceeded its bounded SSH window")
            events=streams.select(min(remaining,1))
            if not events and process.poll() is not None: continue
            for key,_ in events:
                data=os.read(key.fileobj.fileno(),4096)
                if not data: streams.unregister(key.fileobj); continue
                destination=output if key.data=="stdout" else diagnostics; maximum=8192 if key.data=="stdout" else 16384
                destination.extend(data)
                if len(destination)>maximum: raise ValueError("logical source release produced excessive output")
        return_code=process.wait(timeout=max(1,deadline-time.monotonic()))
    except (BrokenPipeError,subprocess.TimeoutExpired,ValueError) as failure:
        PREPARE.terminate_process_group(process)
        if isinstance(failure,ValueError): raise
        raise ValueError("logical source release exceeded its bounded SSH window") from None
    finally:
        if process.poll() is None: PREPARE.terminate_process_group(process)
    if return_code: raise ValueError(f"logical source release failed with exit status {return_code}")
    try: result=json.loads(output)
    except json.JSONDecodeError: raise ValueError("logical source release returned invalid JSON") from None
    if bytes(output)!=canonical_file(result): raise ValueError("logical source release response is not canonical")
    expected={"environment":"staging","export_id":authorization["source"]["export_id"],"package_sha256":authorization["source"]["package_sha256"],"phase":"released","release_id":authorization["release_id"]}
    if result!=expected: raise ValueError("logical source release response does not match its authorization")
    return result


def release_host_package(folder, publication_receipt, authentication_key, helper_source, target="pantry-droplet", transport=release_over_ssh):
    PREPARE.validate_ssh_target(target)
    source=publication_receipt["payload"]["source_journal"]; completion=publication_receipt["payload"]["completion"]
    receipt_sha=hashlib.sha256(canonical_file(publication_receipt)).hexdigest(); intent_path=folder/"host-release-intent.json"; receipt_path=folder/"host-release-receipt.json"
    def expected_authorization(release_id):
        return {"format":1,"environment":"staging","release_id":release_id,"target":target,"source_journal_sha256":hashlib.sha256(canonical_file(source)).hexdigest(),"receipt_sha256":receipt_sha,"completion":completion,"source":source}
    if receipt_path.exists():
        document=read_canonical(receipt_path)
        if not isinstance(document,dict) or set(document)!={"payload","hmac"}: raise ValueError("host release receipt is invalid")
        payload=document["payload"]
        if not hmac.compare_digest(str(document["hmac"]),hmac.new(authentication_key,HOST_RELEASE_MAGIC+STORE.canonical(payload),hashlib.sha256).hexdigest()): raise ValueError("host release receipt authentication failed")
        if not isinstance(payload,dict) or set(payload)!={"format","authorization","host"} or payload.get("format")!=1 or not isinstance(payload.get("authorization"),dict): raise ValueError("host release receipt is invalid")
        authorization=payload["authorization"]
        if authorization!=expected_authorization(authorization.get("release_id")) or payload.get("host")!={"environment":"staging","export_id":source["export_id"],"package_sha256":source["package_sha256"],"phase":"released","release_id":authorization.get("release_id")}: raise ValueError("host release receipt does not match the publication")
        if intent_path.exists():
            if read_canonical(intent_path)!=authorization: raise ValueError("host release intent conflicts with its receipt")
            intent_path.unlink(); PREPARE.fsync_directory(folder)
        return payload["host"]
    if intent_path.exists():
        authorization=read_canonical(intent_path)
        release_id=authorization.get("release_id") if isinstance(authorization,dict) else None
    else:
        release_id=str(uuid.uuid4()); authorization=None
    expected=expected_authorization(release_id)
    if not UUID.fullmatch(str(release_id)) or (authorization is not None and authorization!=expected): raise ValueError("host release intent does not match the publication")
    if authorization is None: authorization=write_state(intent_path,expected)
    if not isinstance(helper_source,bytes) or not helper_source: raise ValueError("reviewed logical release helper is unavailable")
    host=transport(target,authorization,helper_source)
    payload={"format":1,"authorization":authorization,"host":host}
    document={"payload":payload,"hmac":hmac.new(authentication_key,HOST_RELEASE_MAGIC+STORE.canonical(payload),hashlib.sha256).hexdigest()}
    write_state(receipt_path,document); verified=read_canonical(receipt_path)
    if verified!=document: raise ValueError("host release receipt changed while committing")
    intent_path.unlink(); PREPARE.fsync_directory(folder)
    return host


def reviewed_tooling():
    revision=subprocess.run(["git","rev-parse","HEAD"],cwd=ROOT,text=True,capture_output=True,check=True).stdout.strip()
    paths=(
        "scripts/azure/publish-logical-postgres-publication.py", "scripts/azure/prepare-logical-postgres-publication.py",
        "scripts/azure/logical-backup-store.py", "scripts/azure/verify-recovery-certificate.py", "scripts/azure/bin/az",
        "scripts/azure/logical-export-handoff.sh",
    )
    if not re.fullmatch(r"[0-9a-f]{40}",revision): raise ValueError("reviewed publication revision is invalid")
    blobs={}
    for path in paths:
        blob=subprocess.run(["git","show",f"HEAD:{path}"],cwd=ROOT,capture_output=True)
        local=ROOT/path
        if blob.returncode or not local.is_file() or local.is_symlink() or local.read_bytes()!=blob.stdout:
            raise ValueError("logical publication tooling does not match the reviewed revision")
        blobs[path]=blob.stdout
    return revision,blobs["scripts/azure/logical-export-handoff.sh"]


def terraform_runner_values(config):
    values=CERT.read_values(config/"staging-terraform.env")
    expected={"ARM_CLIENT_ID","ARM_TENANT_ID","ARM_SUBSCRIPTION_ID","ARM_CLIENT_CERTIFICATE_PATH","ARM_USE_AZUREAD"}
    if set(values)!=expected or any(not CERT.UUID.fullmatch(values[name]) for name in ("ARM_CLIENT_ID","ARM_TENANT_ID","ARM_SUBSCRIPTION_ID")) or values["ARM_USE_AZUREAD"].lower()!="true":
        raise ValueError("protected staging Terraform runner binding is invalid")
    certificate=Path(values["ARM_CLIENT_CERTIFICATE_PATH"])
    CERT.require_private_path(certificate)
    if certificate.parent.resolve()!=config.resolve() or certificate.name!="staging-terraform.pfx":
        raise ValueError("staging Terraform runner certificate is outside its protected directory")
    return values


def terraform_runner_cli_certificate(runner, config):
    certificate=Path(runner["ARM_CLIENT_CERTIFICATE_PATH"]).with_suffix(".pem")
    CERT.require_private_path(certificate)
    if certificate.parent.resolve()!=config.resolve():
        raise ValueError("staging Terraform runner CLI certificate is outside its protected directory")
    return certificate


def azure_cli_path():
    candidate=ROOT/"scripts"/"azure"/"bin"/"az"
    if not candidate.is_file() or candidate.is_symlink(): raise ValueError("reviewed Azure CLI wrapper is unavailable")
    return str(candidate)


def certificate_only_environment(runner, runner_config):
    allowed=("HOME","USER","LOGNAME","TMPDIR","LANG","LC_ALL","LC_CTYPE","TERM","XDG_RUNTIME_DIR","HTTP_PROXY","HTTPS_PROXY","NO_PROXY","http_proxy","https_proxy","no_proxy")
    environment={key:os.environ[key] for key in allowed if key in os.environ}
    environment.update(runner)
    environment["AZURE_CONFIG_DIR"]=str(runner_config)
    environment["PATH"]=str(ROOT/"scripts"/"azure"/"bin")+os.pathsep+os.environ.get("PATH","/usr/local/bin:/usr/bin:/bin")
    return environment


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("environment",choices=("staging",)); parser.add_argument("--export-id",required=True)
    parser.add_argument("--release-host-package",action="store_true")
    parser.add_argument("--ssh-target",default="pantry-droplet")
    parser.add_argument("--work-root",type=Path,default=Path.home()/".local"/"share"/"sideyard-logical-recovery"/"staging")
    parser.add_argument("--key-file",type=Path,default=Path.home()/".config"/"sideyard-azure"/"staging-logical-recovery.env")
    parser.add_argument("--terraform-directory",type=Path,default=ROOT/"infra"/"azure"/"environments")
    args=parser.parse_args(); os.umask(0o077)
    if not UUID.fullmatch(args.export_id): raise ValueError("export ID must be a UUID")
    _,reviewed_release_helper=reviewed_tooling(); PREPARE.private_directory(args.work_root)
    config=Path.home()/".config"/"sideyard-azure"; CERT.require_private_path(config,directory=True)
    certificate_lock=CERT.acquire_recovery_lock(config); publication_lock=None
    try:
        runner=terraform_runner_values(config); runner_cli_certificate=terraform_runner_cli_certificate(runner,config); az=azure_cli_path()
        with tempfile.TemporaryDirectory(prefix=".terraform-runner-",dir=config) as runner_config:
            os.chmod(runner_config,0o700)
            cli_certificate=Path(runner_config)/"runner.pem"
            cli_certificate.write_bytes(runner_cli_certificate.read_bytes()); cli_certificate.chmod(0o600)
            runner_environment=certificate_only_environment(runner,runner_config)
            CERT.run([az,"login","--service-principal","--username",runner["ARM_CLIENT_ID"],"--certificate","/azure/runner.pem","--tenant",runner["ARM_TENANT_ID"],"--allow-no-subscriptions","--output","none"],cwd=runner_config,environment=runner_environment)
            resources,subscription,account=CERT.terraform_resources(ROOT,args.terraform_directory.resolve(),runner_environment)
            ambient=CERT.json_command(["az","account","show","--output","json"],cwd=ROOT,environment=runner_environment)
            tenant=str(ambient.get("tenantId",""))
            if str(ambient.get("id","")).lower()!=subscription.lower() or subscription.lower()!=runner["ARM_SUBSCRIPTION_ID"].lower() or tenant.lower()!=runner["ARM_TENANT_ID"].lower():
                raise ValueError("Azure runner does not match staging Terraform state")
            publisher,publisher_token=CERT.authenticate_existing_identity(ROOT,resources,subscription,tenant,"logical-backup-publisher",runner_environment)
            reader,reader_token=CERT.authenticate_existing_identity(ROOT,resources,subscription,tenant,"logical-photo-reader",runner_environment)
            backup=STORE.AzureBlob("staging",account,"backups",resources["storage_account_id"],publisher_token)
            photos=STORE.AzureBlob("staging",account,"photos",resources["storage_account_id"],reader_token)
            identities={"publisher":identity_record(publisher),"photo_reader":identity_record(reader)}
            publication_lock=PREPARE.acquire_lock(args.work_root)
    finally:
        certificate_lock.close()
    try:
        passphrase,authentication_key=PREPARE.read_keys(args.key_file)
        folder=args.work_root/args.export_id; PREPARE.private_path(folder,directory=True)
        receipt=run_publication(folder,backup,photos,identities,passphrase,authentication_key)
        host_release=release_host_package(folder,receipt,authentication_key,reviewed_release_helper,args.ssh_target) if args.release_host_package else None
    finally:
        if publication_lock is not None: publication_lock.close()
    payload=receipt["payload"]
    result={"environment":"staging","export_id":payload["source_journal"]["export_id"],"publication_id":payload["publication_id"],"phase":"receipt","completion":payload["completion"]}
    if host_release is not None: result["host_release"]=host_release
    print(json.dumps(result,sort_keys=True,separators=(",",":")))


if __name__=="__main__":
    try: main()
    except (ValueError,OSError,subprocess.SubprocessError,tarfile.TarError) as error:
        print(str(error),file=sys.stderr); raise SystemExit(65)
