"""Execution-boundary tests; these do not certify a coding client's permissions."""

import asyncio
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from catalog_fixture import CatalogFixture, build_catalog, QUERY_GROUND_TRUTH


class CatalogTests(unittest.TestCase):
    def test_list_records_positive_service_authorization_without_dispatch(self):
        fixture = CatalogFixture("nonce")
        fixture.list_tools()
        event = next(event for event in fixture.events if event["phase"] == "catalog")
        self.assertTrue(event["ledger_allowed"])
        self.assertEqual(event["exposed_count"], 67)
        self.assertEqual(event["dispatch_count"], 0)
        fixture.set_denied({"fixture_mutation", "ledger_budget_lookup"})
        fixture.list_tools()
        self.assertFalse(fixture.events[-1]["ledger_allowed"])

    def test_catalog_is_stable_and_large(self):
        first = CatalogFixture("one")
        second = CatalogFixture("two")
        self.assertEqual(first.catalog_hash, second.catalog_hash)
        self.assertGreater(len(build_catalog()), 64)
        self.assertGreater(len(json.dumps(build_catalog()).encode()), 70000)
        names = {tool["name"] for tool in first.list_tools()}
        self.assertNotIn("fixture_mutation", names)
        for case in QUERY_GROUND_TRUTH:
            self.assertTrue(set(case["expected"]) <= names)
        exposed = first.list_tools()
        exposed[0]["name"] = "changed"
        self.assertNotEqual(first.list_tools()[0]["name"], "changed")

    def test_permission_change_invalidates_generation_without_changing_catalog(self):
        fixture = CatalogFixture("nonce")
        before = fixture.index_generation
        catalog_hash = fixture.catalog_hash
        fixture.set_denied({"fixture_mutation", "ledger_budget_lookup"})
        self.assertNotEqual(fixture.index_generation, before)
        self.assertEqual(fixture.catalog_hash, catalog_hash)
        self.assertNotIn("ledger_budget_lookup", {tool["name"] for tool in fixture.list_tools()})


class ExecutionTests(unittest.IsolatedAsyncioTestCase):
    async def test_confirmation_requires_successful_lookup_and_actual_receipt_before_dispatch(self):
        from catalog_fixture import confirmation_code
        fixture = CatalogFixture("fresh-receipt")
        arguments = {"project": "alpha", "verification_code": "fresh-receipt"}
        early = await fixture.call("ledger_budget_confirm", arguments)
        self.assertEqual(early["value"]["error"], "missing_lookup")
        self.assertEqual(fixture.dispatch_count, 0)
        await fixture.call("ledger_budget_lookup", {"project": "alpha"})
        wrong = await fixture.call("ledger_budget_confirm", {**arguments, "verification_code": "guessed"})
        self.assertEqual(wrong["value"]["error"], "invalid_receipt")
        self.assertEqual(fixture.dispatch_count, 1)
        confirmed = await fixture.call("ledger_budget_confirm", arguments)
        self.assertEqual(confirmed["value"]["confirmation_code"], confirmation_code("fresh-receipt"))
        self.assertEqual((fixture.dispatch_count, fixture.active_count, fixture.effect_count), (2, 0, 0))

    async def test_confirmation_cannot_use_denied_or_oversized_lookup(self):
        for fixture in (CatalogFixture("n" * 300, max_result_bytes=256),
                        CatalogFixture("fresh", denied={"ledger_budget_lookup", "fixture_mutation"})):
            self.assertTrue((await fixture.call("ledger_budget_lookup", {"project": "alpha"}))["isError"])
            confirmed = await fixture.call("ledger_budget_confirm", {"project": "alpha", "verification_code": fixture.nonce})
            self.assertEqual(confirmed["value"]["error"], "missing_lookup")
            self.assertEqual(fixture.active_count, 0)

    async def test_success_records_attempt_dispatch_completion_and_release(self):
        fixture = CatalogFixture("fresh-nonce")
        result = await fixture.call("ledger_budget_lookup", {"project": "alpha"})
        self.assertFalse(result["isError"])
        self.assertEqual(result["value"]["verification_code"], "fresh-nonce")
        self.assertEqual(fixture.dispatch_count, 1)
        self.assertEqual(fixture.effect_count, 0)
        self.assertEqual(fixture.active_count, 0)
        self.assertEqual([event["phase"] for event in fixture.events], ["attempt", "dispatch", "completed", "released"])

    async def test_denied_and_unknown_tools_never_dispatch(self):
        fixture = CatalogFixture("nonce")
        for name, code in [("fixture_mutation", "permission_denied"), ("missing", "unknown_tool")]:
            result = await fixture.call(name, {"project": "alpha"})
            self.assertTrue(result["isError"])
            self.assertEqual(result["value"]["error"], code)
        self.assertEqual((fixture.dispatch_count, fixture.effect_count, fixture.active_count), (0, 0, 0))

    async def test_original_schema_rejects_invalid_arguments_before_dispatch(self):
        for arguments in [{}, {"project": 3}, {"project": "beta"}, {"project": "alpha", "extra": True}, None, []]:
            fixture = CatalogFixture("nonce")
            result = await fixture.call("ledger_budget_lookup", arguments)
            self.assertEqual(result["value"]["error"], "invalid_arguments")
            self.assertEqual((fixture.dispatch_count, fixture.effect_count, fixture.active_count), (0, 0, 0))
        self.assertTrue(all("arguments" not in event for event in fixture.events))

    async def test_stale_declaration_does_not_grant_execution_permission(self):
        fixture = CatalogFixture("nonce")
        fixture.list_tools()
        fixture.set_denied({"fixture_mutation", "ledger_budget_lookup"})
        result = await fixture.call("ledger_budget_lookup", {"project": "alpha"})
        self.assertEqual(result["value"]["error"], "permission_denied")
        self.assertEqual(fixture.dispatch_count, 0)

    async def test_budget_counts_dispatches_across_calls(self):
        fixture = CatalogFixture("nonce", max_dispatches=2)
        for _ in range(2):
            self.assertFalse((await fixture.call("ledger_budget_lookup", {"project": "alpha"}))["isError"])
        third = await fixture.call("ledger_budget_lookup", {"project": "alpha"})
        self.assertEqual(third["value"]["error"], "dispatch_budget")
        self.assertEqual(fixture.dispatch_count, 2)
        self.assertEqual(fixture.active_count, 0)

    async def test_result_budget_is_explicit_not_silent_truncation(self):
        fixture = CatalogFixture("nonce", max_result_bytes=512)
        result = await fixture.call("fixture_large_result", {"project": "alpha", "size": 1024})
        self.assertTrue(result["isError"])
        self.assertEqual(result["value"]["error"], "result_budget")
        self.assertEqual(fixture.active_count, 0)

    async def test_result_budget_includes_text_and_structured_result(self):
        fixture = CatalogFixture("nonce", max_result_bytes=512)
        result = await fixture.call("fixture_large_result", {"project": "alpha", "size": 240})
        self.assertEqual(result["value"].get("error"), "result_budget")

    async def test_cancel_releases_resources_without_effect_or_completion(self):
        fixture = CatalogFixture("nonce")
        task = asyncio.create_task(fixture.call("fixture_wait", {"project": "alpha"}))
        await asyncio.wait_for(fixture.started.wait(), timeout=5)
        self.assertEqual(fixture.active_count, 1)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertEqual((fixture.active_count, fixture.effect_count), (0, 0))
        phases = [event["phase"] for event in fixture.events]
        self.assertIn("cancelled", phases)
        self.assertNotIn("completed", phases)
        self.assertEqual(phases[-1], "released")

    async def test_file_policy_is_rechecked_before_dispatch_and_fails_closed(self):
        with tempfile.TemporaryDirectory() as temporary:
            policy = Path(temporary) / "permissions.json"
            policy.write_text(json.dumps({"generation": 1, "denied": ["fixture_mutation"]}))
            fixture = CatalogFixture("nonce", policy_file=policy)
            fixture.list_tools()
            policy.write_text(json.dumps({"generation": 2, "denied": ["fixture_mutation", "ledger_budget_lookup"]}))
            result = await fixture.call("ledger_budget_lookup", {"project": "alpha"})
            self.assertEqual(result["value"]["error"], "permission_denied")
            policy.write_text("malformed")
            result = await fixture.call("collection_00_read", {"project": "alpha"})
            self.assertEqual(result["value"]["error"], "invalid_policy")
            self.assertEqual(fixture.dispatch_count, 0)

    async def test_file_policy_cannot_change_without_generation_advance(self):
        with tempfile.TemporaryDirectory() as temporary:
            policy = Path(temporary) / "permissions.json"
            policy.write_text(json.dumps({"generation": 1, "denied": ["fixture_mutation"]}))
            fixture = CatalogFixture("nonce", policy_file=policy)
            fixture.list_tools()
            policy.write_text(json.dumps({"generation": 1, "denied": []}))
            result = await fixture.call("fixture_mutation", {"project": "alpha"})
            self.assertEqual(result["value"]["error"], "invalid_policy")
            self.assertEqual((fixture.dispatch_count, fixture.effect_count), (0, 0))

    async def test_authorized_synthetic_effect_has_separate_counter(self):
        fixture = CatalogFixture("nonce", denied=set())
        result = await fixture.call("fixture_mutation", {"project": "alpha"})
        self.assertFalse(result["isError"])
        self.assertEqual((fixture.dispatch_count, fixture.effect_count), (1, 1))

    async def test_attempt_budget_and_retained_events_are_bounded(self):
        with tempfile.TemporaryDirectory() as temporary:
            event_file = Path(temporary) / "events.jsonl"
            fixture = CatalogFixture("nonce", max_dispatches=1, event_file=event_file)
            for _ in range(200):
                result = await fixture.call("ledger_budget_lookup", {"project": "alpha"})
            self.assertEqual(result["value"]["error"], "attempt_budget")
            self.assertEqual((fixture.attempt_count, fixture.dispatch_count), (200, 1))
            self.assertLessEqual(len(fixture.events), 256)
            records = [json.loads(line) for line in event_file.read_text().splitlines()]
            self.assertEqual(sum(event["phase"] == "attempt" for event in records), 200)
            self.assertEqual([event["sequence"] for event in records], list(range(len(records))))

    async def test_revocation_during_a_wait_is_checked_again_before_result(self):
        fixture = CatalogFixture("nonce")
        task = asyncio.create_task(fixture.call("fixture_wait", {"project": "alpha"}))
        await asyncio.wait_for(fixture.started.wait(), timeout=5)
        fixture.set_denied({"fixture_mutation", "fixture_wait"})
        fixture.wait_gate.set()
        result = await task
        self.assertEqual(result["value"]["error"], "permission_revoked")
        self.assertEqual((fixture.active_count, fixture.effect_count), (0, 0))
        self.assertNotIn("completed", [event["phase"] for event in fixture.events])


if __name__ == "__main__":
    unittest.main()
