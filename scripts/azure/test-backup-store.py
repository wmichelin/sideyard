#!/usr/bin/env python3
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("backup_store", Path(__file__).with_name("backup-store.py"))
store = importlib.util.module_from_spec(spec)
spec.loader.exec_module(store)


class MemoryBlob:
    account = "sideyardtest"

    def __init__(self):
        self.objects, self.writes = {}, []
        self.fail = None

    def put(self, key, source, size, digest):
        if self.fail and key.endswith(self.fail):
            raise ValueError("simulated Azure failure")
        data = source.read()
        if key in self.objects and data != self.objects[key]:
            raise ValueError("cannot overwrite")
        self.objects[key] = data
        self.writes.append(key)
        ref = {"key": key, "version": "saved", "size": size, "sha256": digest}
        self.download(ref, None)
        return ref

    def request(self, method, key):
        return io.BytesIO(self.objects[key])

    def download(self, ref, destination):
        data = self.objects[ref["key"]]
        if ref["version"] != "saved" or len(data) != ref["size"] or hashlib.sha256(data).hexdigest() != ref["sha256"]:
            raise ValueError("corrupt remote version")
        if destination:
            destination.write(data)


class BackupStoreTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / "clubdue-postgres-20260909T120000Z.sql.enc"
        self.path.write_bytes(b"Salted__encrypted-fixture")
        self.secret = b"separate-authentication-secret"
        Path(str(self.path) + ".mac").write_bytes(store.MAGIC + store.file_hash(self.path, self.secret))
        self.blob = MemoryBlob()

    def test_publish_readback_and_restore_exact_pair(self):
        key = store.publish(self.blob, self.path, self.secret)
        self.assertTrue(self.blob.writes[-1].endswith("manifest.json"))
        self.assertEqual(store.publish(self.blob, self.path, self.secret), key)
        self.assertEqual(len(self.blob.objects), 3)
        folder = Path(self.tmp.name) / "restore"
        folder.mkdir()
        result = store.retrieve(self.blob, key, folder, self.secret)
        self.assertEqual(result.read_bytes(), self.path.read_bytes())
        store.verify_pair(result, self.secret)

    def test_wrong_secret_rejected_before_any_upload(self):
        with self.assertRaisesRegex(ValueError, "authentication"):
            store.publish(self.blob, self.path, b"wrong")
        self.assertEqual(self.blob.writes, [])

    def test_failed_component_never_publishes_completion(self):
        self.blob.fail = ".mac"
        with self.assertRaises(ValueError):
            store.publish(self.blob, self.path, self.secret)
        self.assertFalse(any(key.endswith("manifest.json") for key in self.blob.objects))

    def test_tampered_manifest_and_remote_version_rejected(self):
        key = store.publish(self.blob, self.path, self.secret)
        original = self.blob.objects[key]
        changed = json.loads(original)
        changed["payload"]["files"][0]["version"] = "other"
        self.blob.objects[key] = json.dumps(changed).encode()
        folder = Path(self.tmp.name) / "restore"
        folder.mkdir()
        with self.assertRaisesRegex(ValueError, "authentication"):
            store.retrieve(self.blob, key, folder, self.secret)
        self.assertEqual(list(folder.iterdir()), [])
        self.blob.objects[key] = original
        database = changed["payload"]["files"][0]["key"]
        self.blob.objects[database] = b"tampered"
        with self.assertRaisesRegex(ValueError, "corrupt"):
            store.retrieve(self.blob, key, folder, self.secret)
        self.assertEqual(list(folder.iterdir()), [])

    def test_account_path_and_existing_destination_rejected(self):
        key = store.publish(self.blob, self.path, self.secret)
        with self.assertRaises(ValueError):
            store.open_manifest(self.blob.objects[key], self.secret, "sideyardother")
        with self.assertRaises(ValueError):
            store.retrieve(self.blob, key, Path(self.tmp.name), self.secret)
        for account in ("evil.example", "sideyardtest/../", ""):
            with self.assertRaises(ValueError):
                store.Blob(account, "secret", None)
        with self.assertRaises(ValueError):
            store.Blob("sideyardtest", "secret", None).request("GET", "../../secret")

    def test_signed_malformed_timestamp_and_sidecar_rejected(self):
        key = store.publish(self.blob, self.path, self.secret)
        original = json.loads(self.blob.objects[key])["payload"]
        for timestamp in ("../../clubdue-postgres-20260909T120000Z", "20260101T000000Z"):
            payload = dict(original, created=timestamp)
            with self.assertRaises(ValueError):
                store.open_manifest(store.seal_manifest(payload, self.secret), self.secret, self.blob.account)
        original["files"][1]["size"] = 100000000
        with self.assertRaises(ValueError):
            store.open_manifest(store.seal_manifest(original, self.secret), self.secret, self.blob.account)

    def test_transport_sends_date_and_create_only_headers(self):
        class Capture:
            def open(self, request, timeout):
                self.request = request
                return io.BytesIO()
        transport = Capture()
        blob = store.Blob("sideyardtest", "credential", transport)
        with blob.request("PUT", "postgres/20260909T120000Z/database.sql.enc", data=io.BytesIO(b"bytes"), size=5):
            pass
        self.assertEqual(transport.request.get_header("If-none-match"), "*")
        self.assertTrue(transport.request.get_header("X-ms-date").endswith("GMT"))
        self.assertEqual(transport.request.get_header("Content-length"), "5")


if __name__ == "__main__":
    unittest.main()
