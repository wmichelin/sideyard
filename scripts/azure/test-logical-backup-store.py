#!/usr/bin/env python3
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import urllib.error


spec = importlib.util.spec_from_file_location("logical_backup_store", Path(__file__).with_name("logical-backup-store.py"))
store = importlib.util.module_from_spec(spec)
spec.loader.exec_module(store)


SUBSCRIPTION = "11111111-1111-1111-1111-111111111111"
GENERATION = "22222222-2222-2222-2222-222222222222"
EXPORT = "33333333-3333-3333-3333-333333333333"
SHOP = "44444444-4444-4444-4444-444444444444"
PHOTO = "55555555-5555-5555-5555-555555555555"
STORED = "Thu, 10 Sep 2026 12:00:00 GMT"


def source(account, container):
    return {
        "account": account,
        "container": container,
        "resource_id": f"/subscriptions/{SUBSCRIPTION}/resourceGroups/sideyard-staging/providers/Microsoft.Storage/storageAccounts/{account}",
    }


class MemoryBackup:
    container = "backups"

    def __init__(self):
        self.source = source("sideyardbackup", "backups")
        self.versions = {}
        self.writes = []
        self.reads = []
        self.fail_suffix = None
        self.put_times = []
        self.server_now = STORED

    def put(self, key, content, size, digest, content_type="application/octet-stream", require_new=False):
        if self.fail_suffix and key.endswith(self.fail_suffix):
            raise ValueError("simulated backup write failure")
        data = content.read()
        versions = self.versions.setdefault(key, [])
        if versions and require_new:
            raise ValueError("logical backup component already exists; create a fresh export ID")
        if versions and versions[-1][1] != data:
            raise ValueError("create-only conflict")
        if not versions:
            stored_at = self.put_times.pop(0) if self.put_times else STORED
            versions.append(("v1", data, content_type, stored_at))
        self.writes.append(key)
        version, saved, _, stored_at = versions[-1]
        ref = {"key": key, "version": version, "sha256": digest, "size": size, "stored_at": stored_at}
        self.download(ref, None)
        return ref

    def inject(self, key, data, content_type="application/octet-stream", stored_at=STORED):
        versions = self.versions.setdefault(key, [])
        version = f"v{len(versions) + 1}"
        versions.append((version, data, content_type, stored_at))
        return {"key": key, "version": version, "sha256": hashlib.sha256(data).hexdigest(), "size": len(data), "stored_at": stored_at}

    def download(self, ref, destination, expected_type=None):
        self.reads.append((ref["key"], ref["version"]))
        entries = [entry for entry in self.versions.get(ref["key"], []) if entry[0] == ref["version"]]
        if len(entries) != 1:
            raise ValueError("missing exact backup version")
        _, data, content_type, stored_at = entries[0]
        if ref.get("stored_at") != stored_at:
            raise ValueError("wrong exact storage time")
        if len(data) != ref["size"] or hashlib.sha256(data).hexdigest() != ref["sha256"]:
            raise ValueError("corrupt exact backup version")
        if expected_type and expected_type != content_type:
            raise ValueError("wrong content type")
        if destination is not None:
            destination.write(data)

    def current_server_time(self, ref):
        self.download(ref, None)
        return self.server_now


class MemoryPhotos:
    container = "photos"

    def __init__(self, assets):
        self.source = source("sideyardphotos", "photos")
        self.objects = {}
        self.audits = []
        self.failure = None
        for asset in assets:
            content = asset.pop("_bytes")
            if asset["version_id"]:
                self.objects[(asset["object_key"], asset["version_id"])] = (
                    content, asset["content_type"]
                )

    def audit_photo(self, asset):
        self.audits.append((asset["object_key"], asset["version_id"]))
        if self.failure:
            raise ValueError(self.failure)
        value = self.objects.get((asset["object_key"], asset["version_id"]))
        if not value:
            raise ValueError("missing exact photo version")
        data, content_type = value
        if len(data) != asset["byte_length"] or hashlib.sha256(data).hexdigest() != asset["content_sha256"]:
            raise ValueError("photo integrity mismatch")
        if content_type != asset["content_type"]:
            raise ValueError("photo content type mismatch")


def photo_asset(key, content, version="photo-v1", required=True, purged=None, photo_id=PHOTO):
    return {
        "object_key": key,
        "shop_id": SHOP,
        "photo_id": photo_id,
        "content_sha256": hashlib.sha256(content).hexdigest(),
        "content_type": "image/png",
        "byte_length": len(content),
        "version_id": version,
        "created_at": "2026-09-10T12:00:00.000000Z",
        "retired_at": None,
        "purged_at": purged,
        "required": required,
        "_bytes": content,
    }


class LogicalBackupStoreTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.ciphertext = Path(self.tmp.name) / "logical.tar.enc"
        self.ciphertext.write_bytes(b"Salted__12345678encrypted-block!")
        self.secret = b"independent-logical-authentication-secret"
        active = photo_asset("a" * 64, b"active-photo")
        retired = photo_asset("b" * 64, b"retired-photo", version="retired-v1", photo_id="66666666-6666-6666-6666-666666666666")
        retired["retired_at"] = "2026-09-10T12:30:00.000000Z"
        purged = photo_asset("c" * 64, b"purged-photo", version="purged-v1", required=False, purged="2026-09-10T13:00:00.000000Z", photo_id="77777777-7777-7777-7777-777777777777")
        purged["retired_at"] = "2026-09-10T12:30:00.000000Z"
        pending = photo_asset("d" * 64, b"pending-photo", version=None, required=False, photo_id="88888888-8888-8888-8888-888888888888")
        legacy = photo_asset("e" * 64, b"legacy-photo", version=None, required=False, photo_id="99999999-9999-9999-9999-999999999999")
        linked_purged = photo_asset("f" * 64, b"linked-purged", version="linked-v1", required=False, purged="2026-09-10T13:00:00.000000Z", photo_id="aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
        linked_purged["retired_at"] = "2026-09-10T12:30:00.000000Z"
        assets = [active, retired, purged, pending, legacy, linked_purged]
        self.photos = MemoryPhotos(assets)
        self.descriptor = {
            "format": 1,
            "environment": "staging",
            "generation": GENERATION,
            "created": "20260910T120000Z",
            "export_id": EXPORT,
            "release_artifact": "ghcr.io/wmichelin/clubdue@sha256:" + "c" * 64,
            "logical_schema": "sideyard-2026-09-v31",
            "catalog_sha256": "d" * 64,
            "root_sha256": "e" * 64,
            "package_sha256": "f" * 64,
            "package_bytes": 4096,
            "photo_source": {"kind": "azure_blob", **self.photos.source},
            "assets": assets,
        }
        Path(str(self.ciphertext) + ".mac").write_bytes(store.PAIR_MAGIC + store.pair_digest(self.ciphertext, self.secret, self.descriptor))
        self.backup = MemoryBackup()

    def publish(self):
        return store.publish(self.backup, self.photos, self.ciphertext, self.secret, self.descriptor)

    def restore_folder(self, name):
        folder = Path(self.tmp.name) / name
        folder.mkdir(mode=0o700)
        folder.chmod(0o700)
        return folder

    def test_publish_audits_exact_required_photos_and_writes_completion_last(self):
        events = []
        original_audit = self.photos.audit_photo
        original_put = self.backup.put

        def audited(asset):
            events.append("photo:" + asset["object_key"])
            return original_audit(asset)

        def written(key, content, size, digest, content_type="application/octet-stream", require_new=False):
            events.append("blob:" + key.rsplit("/", 1)[-1])
            return original_put(key, content, size, digest, content_type, require_new)

        self.photos.audit_photo = audited
        self.backup.put = written
        marker = self.publish()
        self.assertEqual(events[:2], ["photo:" + "a" * 64, "photo:" + "b" * 64])
        self.assertEqual(events[-1], "blob:completion.json")
        self.assertTrue(self.backup.writes[-1].endswith("completion.json"))
        self.assertEqual(self.photos.audits, [("a" * 64, "photo-v1"), ("b" * 64, "retired-v1")])
        document = self.backup.versions[marker["key"]][0][1]
        for private_value in ("a" * 64, SHOP, PHOTO):
            self.assertNotIn(private_value.encode(), document)
        payload = json.loads(document)["payload"]
        self.assertEqual(payload["asset_count"], 6)
        self.assertEqual(payload["required_asset_count"], 2)
        self.assertEqual(payload["asset_root_sha256"], store.asset_root(self.descriptor["assets"]))

    def test_round_trip_uses_exact_completion_and_component_versions(self):
        marker = self.publish()
        self.backup.inject(marker["key"], b"newer-untrusted-marker", "application/json")
        for ref in json.loads(self.backup.versions[marker["key"]][0][1])["payload"]["files"]:
            self.backup.inject(ref["key"], b"newer-untrusted-object")
        folder = self.restore_folder("restore")
        restored = store.retrieve(self.backup, marker, folder, self.secret, self.descriptor)
        self.assertEqual(restored.read_bytes(), self.ciphertext.read_bytes())
        store.verify_pair(restored, self.secret, self.descriptor)
        self.assertEqual(restored.stat().st_mode & 0o777, 0o600)
        self.assertEqual(Path(str(restored) + ".mac").stat().st_mode & 0o777, 0o600)

    def test_wrong_secret_and_malformed_descriptor_fail_before_network(self):
        with self.assertRaisesRegex(ValueError, "authentication"):
            store.publish(self.backup, self.photos, self.ciphertext, b"wrong", self.descriptor)
        self.assertEqual(self.backup.writes, [])

    def test_valid_pair_cannot_be_swapped_to_another_descriptor(self):
        changed = dict(self.descriptor, root_sha256="0" * 64)
        with self.assertRaisesRegex(ValueError, "authentication"):
            store.publish(self.backup, self.photos, self.ciphertext, self.secret, changed)
        self.assertEqual(self.backup.writes, [])
        changed = dict(self.descriptor, environment="other")
        with self.assertRaisesRegex(ValueError, "staging-only"):
            store.publish(self.backup, self.photos, self.ciphertext, self.secret, changed)
        self.assertEqual(self.backup.writes, [])

    def test_photo_failure_never_publishes_completion(self):
        self.photos.failure = "photo readback failed"
        with self.assertRaisesRegex(ValueError, "photo readback"):
            self.publish()
        self.assertFalse(any(key.endswith("completion.json") for key in self.backup.versions))

    def test_remote_pair_is_authenticated_together_before_completion(self):
        original_put = self.backup.put

        def corrupt_after_individual_readback(key, content, size, digest, content_type="application/octet-stream", require_new=False):
            ref = original_put(key, content, size, digest, content_type, require_new)
            if key.endswith(".mac"):
                ciphertext_key = key.removesuffix(".mac")
                version, _, mime, stored_at = self.backup.versions[ciphertext_key][0]
                self.backup.versions[ciphertext_key][0] = (version, b"changed-after-readback", mime, stored_at)
            return ref

        self.backup.put = corrupt_after_individual_readback
        with self.assertRaisesRegex(ValueError, "corrupt|authentication"):
            self.publish()
        self.assertFalse(any(key.endswith("completion.json") for key in self.backup.versions))

    def test_missing_tampered_or_wrong_type_photo_never_completes(self):
        tests = ("missing", "bytes", "type")
        for mode in tests:
            with self.subTest(mode=mode):
                backup = MemoryBackup()
                photos = MemoryPhotos([])
                photos.source = self.photos.source
                asset = self.descriptor["assets"][0]
                if mode == "bytes":
                    photos.objects[(asset["object_key"], asset["version_id"])] = (b"wrong", asset["content_type"])
                if mode == "type":
                    photos.objects[(asset["object_key"], asset["version_id"])] = (b"active-photo", "image/jpeg")
                with self.assertRaises(ValueError):
                    store.publish(backup, photos, self.ciphertext, self.secret, self.descriptor)
                self.assertFalse(any(key.endswith("completion.json") for key in backup.versions))

    def test_completion_tamper_and_resigned_binding_changes_are_rejected(self):
        marker = self.publish()
        original = self.backup.versions[marker["key"]][0][1]
        tampered = json.loads(original)
        tampered["payload"]["generation"] = "99999999-9999-9999-9999-999999999999"
        bad_ref = self.backup.inject(marker["key"], store.canonical(tampered), "application/json")
        folder = self.restore_folder("tampered")
        with self.assertRaisesRegex(ValueError, "authentication"):
            store.retrieve(self.backup, bad_ref, folder, self.secret, self.descriptor)
        self.assertEqual(list(folder.iterdir()), [])

        payload = json.loads(original)["payload"]
        payload["release_artifact"] = "sha256:other"
        resigned = store.seal_completion(payload, self.secret)
        resigned_ref = self.backup.inject(marker["key"], resigned, "application/json")
        with self.assertRaisesRegex(ValueError, "release_artifact mismatch"):
            store.retrieve(self.backup, resigned_ref, folder, self.secret, self.descriptor)
        self.assertEqual(list(folder.iterdir()), [])

    def test_corrupt_exact_component_leaves_no_restore_files(self):
        marker = self.publish()
        payload = json.loads(self.backup.versions[marker["key"]][0][1])["payload"]
        ref = payload["files"][0]
        entries = self.backup.versions[ref["key"]]
        entries[0] = (entries[0][0], b"corrupt", entries[0][2], entries[0][3])
        folder = self.restore_folder("restore-corrupt")
        with self.assertRaisesRegex(ValueError, "corrupt"):
            store.retrieve(self.backup, marker, folder, self.secret, self.descriptor)
        self.assertEqual(list(folder.iterdir()), [])

    def test_existing_component_requires_a_fresh_export_id(self):
        marker = self.publish()
        with self.assertRaisesRegex(ValueError, "fresh export ID"):
            self.publish()
        self.assertIn(marker["key"], self.backup.versions)

    def test_each_blob_write_failure_has_no_marker_and_retry_converges(self):
        for suffix in ("recovery.tar.enc", ".mac", "completion.json"):
            with self.subTest(suffix=suffix):
                backup = MemoryBackup()
                backup.fail_suffix = suffix
                with self.assertRaises(ValueError):
                    store.publish(backup, self.photos, self.ciphertext, self.secret, self.descriptor)
                self.assertFalse(any(key.endswith("completion.json") for key in backup.versions))
                backup.fail_suffix = None
                if suffix == "recovery.tar.enc":
                    marker = store.publish(backup, self.photos, self.ciphertext, self.secret, self.descriptor)
                    self.assertTrue(marker["key"].endswith("completion.json"))
                else:
                    with self.assertRaisesRegex(ValueError, "fresh export ID"):
                        store.publish(backup, self.photos, self.ciphertext, self.secret, self.descriptor)
                    self.assertFalse(any(key.endswith("completion.json") for key in backup.versions))

    def test_publication_window_uses_trusted_server_preflight_and_marker_time(self):
        for server_now, marker_time, completes in (
            ("Thu, 10 Sep 2026 12:54:59 GMT", "Thu, 10 Sep 2026 12:59:59 GMT", True),
            ("Thu, 10 Sep 2026 12:55:00 GMT", "Thu, 10 Sep 2026 12:55:01 GMT", False),
        ):
            with self.subTest(server_now=server_now):
                backup = MemoryBackup()
                backup.put_times = [STORED, STORED, marker_time]
                backup.server_now = server_now
                if completes:
                    marker = store.publish(backup, self.photos, self.ciphertext, self.secret, self.descriptor)
                    self.assertTrue(marker["key"].endswith("completion.json"))
                else:
                    with self.assertRaisesRegex(ValueError, "insufficient retention-safe"):
                        store.publish(backup, self.photos, self.ciphertext, self.secret, self.descriptor)
                    marker_keys = [key for key in backup.versions if key.endswith("completion.json")]
                    self.assertEqual(marker_keys, [])

    def test_post_write_span_check_rejects_inconsistent_late_marker(self):
        backup = MemoryBackup()
        backup.server_now = "Thu, 10 Sep 2026 12:54:59 GMT"
        backup.put_times = [STORED, STORED, "Thu, 10 Sep 2026 13:00:00 GMT"]
        with self.assertRaisesRegex(ValueError, "retention-safe"):
            store.publish(backup, self.photos, self.ciphertext, self.secret, self.descriptor)
        key = next(key for key in backup.versions if key.endswith("completion.json"))
        version, data, _, stored_at = backup.versions[key][0]
        marker = {"key": key, "version": version, "sha256": hashlib.sha256(data).hexdigest(), "size": len(data), "stored_at": stored_at}
        folder = self.restore_folder("expired-span")
        with self.assertRaisesRegex(ValueError, "retention-safe"):
            store.retrieve(backup, marker, folder, self.secret, self.descriptor)

    def test_descriptor_rejects_unknown_fields_inconsistent_assets_and_cross_environment_sources(self):
        changed = dict(self.descriptor, surprise=True)
        with self.assertRaises(ValueError):
            store.validate_descriptor(changed)
        assets = [dict(self.descriptor["assets"][0], required=False)]
        with self.assertRaisesRegex(ValueError, "requirement"):
            store.validate_descriptor(dict(self.descriptor, assets=assets))
        cross = dict(self.descriptor["photo_source"])
        cross["resource_id"] = cross["resource_id"].replace("sideyard-staging", "sideyard-production")
        with self.assertRaisesRegex(ValueError, "environment"):
            store.validate_descriptor(dict(self.descriptor, photo_source=cross))
        with self.assertRaisesRegex(ValueError, "staging-only"):
            store.validate_descriptor(dict(self.descriptor, environment="production"))
        with self.assertRaises(ValueError):
            store.validate_descriptor(dict(self.descriptor, format=True))
        with self.assertRaises(ValueError):
            store.validate_descriptor(dict(self.descriptor, package_bytes=True))
        boolean_asset = dict(self.descriptor["assets"][0], byte_length=True)
        with self.assertRaises(ValueError):
            store.validate_descriptor(dict(self.descriptor, assets=[boolean_asset]))
        malformed = dict(self.descriptor["assets"][0], created_at="2026-99-99T12:00:00.000000Z")
        with self.assertRaisesRegex(ValueError, "timestamp"):
            store.validate_descriptor(dict(self.descriptor, assets=[malformed]))
        reversed_lifecycle = dict(
            self.descriptor["assets"][2],
            retired_at="2026-09-10T14:00:00.000000Z",
            purged_at="2026-09-10T13:00:00.000000Z",
        )
        with self.assertRaisesRegex(ValueError, "order"):
            store.validate_descriptor(dict(self.descriptor, assets=[reversed_lifecycle]))

    def test_restore_rejects_binding_and_private_folder_errors_before_network(self):
        marker = {
            "key": f"logical-completions/staging/{GENERATION}/20260910T120000Z-{EXPORT}/completion.json",
            "version": "v1", "sha256": "a" * 64, "size": 100, "stored_at": STORED,
        }
        folder = self.restore_folder("preflight")
        changed = dict(self.descriptor, generation="77777777-7777-7777-7777-777777777777")
        with self.assertRaisesRegex(ValueError, "does not match"):
            store.retrieve(self.backup, marker, folder, self.secret, changed)
        self.assertEqual(self.backup.reads, [])
        wrong_source = MemoryBackup()
        wrong_source.source = dict(wrong_source.source, resource_id=wrong_source.source["resource_id"].replace("sideyard-staging", "sideyard-production"))
        with self.assertRaisesRegex(ValueError, "environment"):
            store.retrieve(wrong_source, marker, folder, self.secret, self.descriptor)
        self.assertEqual(wrong_source.reads, [])
        folder.chmod(0o755)
        with self.assertRaisesRegex(ValueError, "private owner"):
            store.retrieve(self.backup, marker, folder, self.secret, self.descriptor)
        self.assertEqual(self.backup.reads, [])

    def test_size_ceilings_are_closed(self):
        with self.assertRaisesRegex(ValueError, "package size"):
            store.validate_descriptor(dict(self.descriptor, package_bytes=store.MAX_PACKAGE + 1))
        oversized = Path(self.tmp.name) / "oversized.enc"
        with oversized.open("wb") as output:
            output.truncate(store.MAX_CIPHERTEXT + 1)
        with self.assertRaisesRegex(ValueError, "bounded ciphertext"):
            store.verify_pair(oversized, self.secret, self.descriptor)
        store.validate_descriptor(dict(self.descriptor, package_bytes=1))
        store.validate_descriptor(dict(self.descriptor, package_bytes=store.MAX_PACKAGE))
        boundary = dict(self.descriptor["assets"][0], byte_length=store.MAX_PHOTO)
        store.validate_descriptor(dict(self.descriptor, assets=[boundary]))

    def test_restore_directory_promotion_is_atomic_on_failure(self):
        marker = self.publish()
        folder = self.restore_folder("atomic")
        with mock.patch.object(store.os, "replace", side_effect=OSError("simulated power boundary")):
            with self.assertRaises(OSError):
                store.retrieve(self.backup, marker, folder, self.secret, self.descriptor)
        self.assertEqual(list(folder.iterdir()), [])
        self.assertFalse(any(path.name.startswith(".atomic.sideyard-") for path in folder.parent.iterdir()))

    def test_post_promotion_durability_error_converges_on_retry(self):
        marker = self.publish()
        folder = self.restore_folder("durability")
        calls = 0

        def fail_parent_fsync(_fd):
            nonlocal calls
            calls += 1
            if calls == 4:
                raise OSError("simulated parent fsync failure")

        with mock.patch.object(store.os, "fsync", side_effect=fail_parent_fsync):
            with self.assertRaisesRegex(OSError, "parent fsync"):
                store.retrieve(self.backup, marker, folder, self.secret, self.descriptor)
        self.assertEqual({path.name for path in folder.iterdir()}, {"sideyard-recovery.tar.enc", "sideyard-recovery.tar.enc.mac"})
        restored = store.retrieve(self.backup, marker, folder, self.secret, self.descriptor)
        self.assertEqual(restored.read_bytes(), self.ciphertext.read_bytes())

    def test_existing_promoted_output_must_match_exact_remote_refs(self):
        marker = self.publish()
        folder = self.restore_folder("existing")
        restored = store.retrieve(self.backup, marker, folder, self.secret, self.descriptor)
        restored.write_bytes(b"tampered")
        with self.assertRaisesRegex(ValueError, "does not match"):
            store.retrieve(self.backup, marker, folder, self.secret, self.descriptor)

    def test_transport_is_create_only_and_version_pinned(self):
        class Response(io.BytesIO):
            headers = {"x-ms-version-id": "version-1", "Content-Type": "application/octet-stream"}

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

        class Capture:
            calls = 0

            def open(self, request, timeout):
                self.calls += 1
                self.request = request
                return Response(b"")

        transport = Capture()
        client = store.AzureBlob("staging", "sideyardbackup", "backups", self.backup.source["resource_id"], "token", transport)
        key = f"logical-completions/staging/{GENERATION}/20260910T120000Z-{EXPORT}/completion.json"
        with client.request("PUT", key, data=io.BytesIO(b"x"), size=1):
            pass
        self.assertEqual(transport.request.get_header("If-none-match"), "*")
        self.assertEqual(transport.request.get_header("Content-length"), "1")
        with client.request("GET", key, version="exact-version"):
            pass
        self.assertIn("versionid=exact-version", transport.request.full_url)
        calls = transport.calls
        with self.assertRaises(ValueError):
            client.request("GET", f"logical-completions/production/{GENERATION}/20260910T120000Z-{EXPORT}/completion.json")
        with self.assertRaises(ValueError):
            client.request("PUT", f"logical-completions/staging/{GENERATION}/20260910T120000Z-{EXPORT}/recovery.tar.enc")
        with self.assertRaises(ValueError):
            client.request("PUT", f"logical-components/staging/{GENERATION}/20260910T120000Z-{EXPORT}/completion.json")
        self.assertEqual(transport.calls, calls)

    def test_transport_conflict_reconciles_and_reads_exact_version(self):
        class Response(io.BytesIO):
            def __init__(self, data=b"", headers=None):
                super().__init__(data)
                self.headers = headers or {}

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

        class Scripted:
            def __init__(self):
                self.methods = []

            def open(self, request, timeout):
                self.methods.append(request.get_method())
                if request.get_method() == "PUT":
                    raise urllib.error.HTTPError(request.full_url, 412, "exists", {}, None)
                if request.get_method() == "HEAD":
                    return Response(headers={"x-ms-version-id": "exact-v1", "Last-Modified": STORED, "Date": "Thu, 10 Sep 2026 12:01:00 GMT"})
                return Response(b"x", {"Content-Type": "application/octet-stream", "Last-Modified": STORED})

        transport = Scripted()
        client = store.AzureBlob("staging", "sideyardbackup", "backups", self.backup.source["resource_id"], "token", transport)
        key = f"logical-completions/staging/{GENERATION}/20260910T120000Z-{EXPORT}/completion.json"
        ref = client.put(key, io.BytesIO(b"x"), 1, hashlib.sha256(b"x").hexdigest())
        self.assertEqual(ref["version"], "exact-v1")
        self.assertEqual(transport.methods, ["PUT", "HEAD", "GET"])
        self.assertEqual(client.current_server_time(ref), "Thu, 10 Sep 2026 12:01:00 GMT")
        self.assertEqual(transport.methods, ["PUT", "HEAD", "GET", "HEAD"])
        rejecting = Scripted()
        client.opener = rejecting
        with self.assertRaisesRegex(ValueError, "fresh export ID"):
            client.put(key, io.BytesIO(b"x"), 1, hashlib.sha256(b"x").hexdigest(), require_new=True)
        self.assertEqual(rejecting.methods, ["PUT"])

    def test_transport_rejects_missing_version_and_download_integrity_failures(self):
        class Response(io.BytesIO):
            def __init__(self, data=b"", headers=None):
                super().__init__(data)
                self.headers = headers or {}

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

        class OneResponse:
            def __init__(self, response):
                self.response = response

            def open(self, request, timeout):
                return self.response

        key = f"logical-completions/staging/{GENERATION}/20260910T120000Z-{EXPORT}/completion.json"
        client = store.AzureBlob(
            "staging", "sideyardbackup", "backups", self.backup.source["resource_id"], "token",
            OneResponse(Response(headers={})),
        )
        with self.assertRaisesRegex(ValueError, "versioning"):
            client.put(key, io.BytesIO(b"x"), 1, hashlib.sha256(b"x").hexdigest())

        photo_source = source("sideyardphotos", "photos")
        for name, data, mime, size, digest, message in (
            ("truncated", b"x", "image/png", 2, hashlib.sha256(b"xx").hexdigest(), "integrity"),
            ("oversized", b"xx", "image/png", 1, hashlib.sha256(b"x").hexdigest(), "exceeds"),
            ("digest", b"x", "image/png", 1, hashlib.sha256(b"y").hexdigest(), "integrity"),
            ("mime", b"x", "image/jpeg", 1, hashlib.sha256(b"x").hexdigest(), "content type"),
        ):
            with self.subTest(name=name):
                photo = store.AzureBlob("staging", "sideyardphotos", "photos", photo_source["resource_id"], "token", OneResponse(Response(data, {"Content-Type": mime})))
                ref = {"key": "a" * 64, "version": "exact-v1", "sha256": digest, "size": size}
                with self.assertRaisesRegex(ValueError, message):
                    photo.download(ref, None, "image/png")


if __name__ == "__main__":
    unittest.main()
