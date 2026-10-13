"""A pending native discovery must not be canceled by its first catalog read."""
from pathlib import Path
import sys
import unittest
from unittest.mock import MagicMock, patch
import json

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import live_clients


class CatalogReadinessTests(unittest.TestCase):
    def test_pending_state_requires_same_process_and_strict_counter(self):
        ready = {"base_url": "http://127.0.0.1:12345/v1", "pid": 123, "ready_nonce": "same-process"}
        good = {"pid": 123, "ready_nonce": "same-process", "active_auxiliary_work": 1}
        invalid = [json.dumps({**good, "pid": True}), json.dumps({**good, "ready_nonce": "other-process"}),
                   json.dumps({**good, "active_auxiliary_work": True}), json.dumps({**good, "active_auxiliary_work": -1}),
                   json.dumps({**good, "extra": "untrusted"}), json.dumps(good).replace('"pid": 123', '"pid": 999, "pid": 123'),
                   "x" * 8193]
        connection = MagicMock()
        response = connection.getresponse.return_value
        response.status = 200
        response.read.return_value = json.dumps(good).encode()
        with patch("live_clients.http.client.HTTPConnection", return_value=connection):
            self.assertEqual(live_clients._catalog_work_active(ready), 1)
            for body in invalid:
                response.read.return_value = body.encode()
                with self.assertRaises(live_clients.CatalogFailure) as failure:
                    live_clients._catalog_work_active(ready)
                self.assertEqual(failure.exception.reason, "readiness_unverified")
        connection.request.assert_called_with("GET", "/acceptance/catalog-state",
                                             headers={"Authorization": "Bearer local-acceptance-placeholder"})

    def test_expired_task_deadline_never_reads_catalog(self):
        with patch("live_clients._fetch_model_metadata_once") as fetch, \
                patch("live_clients.time.monotonic", return_value=10):
            with self.assertRaises(live_clients.CatalogFailure) as failure:
                live_clients.fetch_model_metadata({}, "fixture", "codex", "lazy", deadline=10)
        self.assertEqual(failure.exception.reason, "startup_budget_exhausted")
        fetch.assert_not_called()

    def test_pending_native_discovery_requeries_actual_catalog(self):
        metadata = {"slug": "fixture", "context_window": 1000}
        evidence = []
        ready = {"profile": {"executor": "antigravity"}}
        with patch("live_clients._fetch_model_metadata_once", side_effect=[
                live_clients.CatalogFailure("model_unavailable"), metadata]) as fetch, \
                patch("live_clients._catalog_work_active", return_value=1), \
                patch("live_clients.time.monotonic", return_value=10), \
                patch("live_clients.time.sleep") as wait:
            self.assertEqual(live_clients.fetch_model_metadata(ready, "fixture", "codex", "lazy",
                             deadline=11, observations=evidence), metadata)
        self.assertEqual(fetch.call_count, 2)
        self.assertEqual(len(evidence), 2)
        self.assertEqual(evidence[0]["active_auxiliary_work"], 1)
        self.assertEqual(evidence[1]["available"], True)
        wait.assert_called_once()

    def test_settled_discovery_rechecks_publication_before_rejecting(self):
        ready = {"profile": {"executor": "antigravity"}}
        with patch("live_clients._fetch_model_metadata_once", side_effect=[
                live_clients.CatalogFailure("model_unavailable"), {"slug": "fixture"}]), \
                patch("live_clients._catalog_work_active", return_value=0), \
                patch("live_clients.time.monotonic", return_value=10), patch("live_clients.time.sleep") as wait:
            self.assertEqual(live_clients.fetch_model_metadata(ready, "fixture", "opencode", "lazy", deadline=11),
                             {"slug": "fixture"})
        wait.assert_not_called()

    def test_settled_missing_model_does_not_loop(self):
        ready = {"profile": {"executor": "antigravity"}}
        with patch("live_clients._fetch_model_metadata_once", side_effect=live_clients.CatalogFailure("model_unavailable")) as fetch, \
                patch("live_clients._catalog_work_active", return_value=0), \
                patch("live_clients.time.monotonic", return_value=10), patch("live_clients.time.sleep") as wait:
            with self.assertRaises(live_clients.CatalogFailure) as failure:
                live_clients.fetch_model_metadata(ready, "fixture", "opencode", "lazy", deadline=11)
        self.assertEqual(failure.exception.reason, "model_unavailable")
        self.assertEqual(fetch.call_count, 2)
        wait.assert_not_called()

    def test_mode_errors_and_other_providers_never_wait(self):
        for executor, reason in (("antigravity", "tool_mode_mismatch"), ("codex", "model_unavailable")):
            with self.subTest(executor=executor), \
                    patch("live_clients._fetch_model_metadata_once", side_effect=live_clients.CatalogFailure(reason)), \
                    patch("live_clients._catalog_work_active") as state, \
                    patch("live_clients.time.monotonic", return_value=10), patch("live_clients.time.sleep") as wait:
                with self.assertRaises(live_clients.CatalogFailure) as failure:
                    live_clients.fetch_model_metadata({"profile": {"executor": executor}}, "fixture", "codex", "lazy", deadline=11)
                self.assertEqual(failure.exception.reason, reason)
                state.assert_not_called()
                wait.assert_not_called()

    def test_task_budget_exhaustion_stops_pending_discovery(self):
        with patch("live_clients._fetch_model_metadata_once", side_effect=live_clients.CatalogFailure("model_unavailable")) as fetch, \
                patch("live_clients._catalog_work_active", return_value=1), \
                patch("live_clients.time.monotonic", side_effect=[10, 10, 10.6, 10.6]), patch("live_clients.time.sleep") as wait:
            with self.assertRaises(live_clients.CatalogFailure) as failure:
                live_clients.fetch_model_metadata({"profile": {"executor": "antigravity"}}, "fixture", "codex", "lazy", deadline=10.5)
        self.assertEqual(failure.exception.reason, "startup_budget_exhausted")
        self.assertEqual(fetch.call_count, 1)
        wait.assert_not_called()


if __name__ == "__main__":
    unittest.main()
