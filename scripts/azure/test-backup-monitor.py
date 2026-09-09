#!/usr/bin/env python3
import importlib.util
import io
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("monitor", Path(__file__).with_name("check-backups.py"))
monitor = importlib.util.module_from_spec(spec)
spec.loader.exec_module(monitor)


class MonitorTests(unittest.TestCase):
    def test_missing_stale_failed_and_future_markers_alert(self):
        now = 1000000
        for last, failed, problem in [(now - 25*3600, False, False), (now - 27*3600, False, True), (None, False, True), (now, True, True), (now + 3600, False, True)]:
            with self.subTest(last=last, failed=failed):
                self.assertEqual(bool(monitor.backup_problem(now, last, failed)), problem)

    def test_notice_is_owner_only_and_idempotent(self):
        class Client:
            def open(self, request, timeout):
                self.request = request
                return io.BytesIO(b'{"id":"accepted"}')
        client = Client()
        env = {"INTEREST_NOTIFY_TO": "wmichelin@gmail.com", "INTEREST_NOTIFY_FROM": "Sideyard <alerts@example.test>", "RESEND_API_KEY": "secret"}
        monitor.send_notice(env, "staging", "incident-1", "Backup failed.", client)
        self.assertEqual(json.loads(client.request.data)["to"], ["wmichelin@gmail.com"])
        self.assertEqual(client.request.get_header("Idempotency-key"), "sideyard-backups/staging/incident-1")
        env["INTEREST_NOTIFY_TO"] = "unrelated@example.test"
        with self.assertRaises(ValueError):
            monitor.send_notice(env, "staging", "incident-1", "Backup failed.", client)


if __name__ == "__main__":
    unittest.main()
