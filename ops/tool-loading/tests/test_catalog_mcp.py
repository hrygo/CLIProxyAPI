"""SDK protocol and real stdio checks, independent of any LLM Provider."""

import asyncio
import json
from pathlib import Path
import sys
import tempfile
import unittest

from mcp import ClientSession, StdioServerParameters, types
from mcp.client.stdio import stdio_client
from mcp.shared.memory import create_connected_server_and_client_session
from mcp.shared.exceptions import McpError

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from catalog_fixture import CatalogFixture, create_server


class MCPTests(unittest.IsolatedAsyncioTestCase):
    async def test_stdio_initialize_list_validate_revoke_and_record(self):
        with tempfile.TemporaryDirectory() as temporary:
            events = Path(temporary) / "events.jsonl"
            policy = Path(temporary) / "permissions.json"
            policy.write_text(json.dumps({"generation": 1, "denied": ["fixture_mutation"]}))
            parameters = StdioServerParameters(command=sys.executable, args=[
                str(Path(__file__).resolve().parents[1] / "catalog_fixture.py"),
                "--nonce", "fresh-fixture-nonce", "--events", str(events), "--permissions", str(policy),
                "--max-result-bytes", "512",
            ])
            changed = asyncio.Event()

            async def message_handler(message):
                if isinstance(message, types.ServerNotification) and message.root.method == "notifications/tools/list_changed":
                    changed.set()

            async with stdio_client(parameters) as (read, write):
                async with ClientSession(read, write, message_handler=message_handler) as session:
                    initialized = await session.initialize()
                    self.assertTrue(initialized.capabilities.tools.listChanged)
                    before = await session.list_tools()
                    self.assertNotIn("fixture_mutation", {tool.name for tool in before.tools})
                    valid = await session.call_tool("ledger_budget_lookup", {"project": "alpha"})
                    self.assertFalse(valid.isError)
                    self.assertEqual(valid.structuredContent["verification_code"], "fresh-fixture-nonce")
                    self.assertLessEqual(len(valid.model_dump_json(by_alias=True, exclude_none=True).encode()), 512)
                    oversized = await session.call_tool("fixture_large_result", {"project": "alpha", "size": 240})
                    self.assertEqual(oversized.structuredContent["error"], "result_budget")
                    self.assertTrue(oversized.isError)
                    invalid = await session.call_tool("ledger_budget_lookup", {"project": 3})
                    self.assertTrue(invalid.isError)
                    self.assertEqual(invalid.structuredContent["error"], "invalid_arguments")
                    forbidden = await session.call_tool("fixture_mutation", {"project": "alpha"})
                    self.assertTrue(forbidden.isError)
                    policy.write_text(json.dumps({"generation": 2, "denied": ["fixture_mutation", "ledger_budget_lookup"]}))
                    stale = await session.call_tool("ledger_budget_lookup", {"project": "alpha"})
                    self.assertEqual(stale.structuredContent["error"], "permission_denied")
                    await asyncio.wait_for(changed.wait(), timeout=5)
                    after = await session.list_tools()
                    self.assertNotIn("ledger_budget_lookup", {tool.name for tool in after.tools})
                    self.assertNotEqual(before.meta["fixture_index_generation"], after.meta["fixture_index_generation"])
            records = [json.loads(line) for line in events.read_text().splitlines()]
            self.assertEqual(sum(event["phase"] == "attempt" for event in records), 5)
            self.assertEqual(sum(event["phase"] == "dispatch" for event in records), 2)
            self.assertEqual(records[-1]["effect_count"], 0)
            self.assertEqual(records[-1]["active_count"], 0)
            self.assertEqual([event["sequence"] for event in records], list(range(len(records))))

    async def test_mcp_cancel_notification_releases_running_tool(self):
        fixture = CatalogFixture("nonce")
        async with create_connected_server_and_client_session(create_server(fixture)) as session:
            task = asyncio.create_task(session.call_tool("fixture_wait", {"project": "alpha"}))
            try:
                await asyncio.wait_for(fixture.started.wait(), timeout=5)
                request_id = next(event["request_id"] for event in fixture.events if event["phase"] == "request")
                await session.send_notification(types.ClientNotification.model_validate({
                    "method": "notifications/cancelled", "params": {"requestId": request_id, "reason": "fixture cancellation"},
                }))
                await asyncio.wait_for(fixture.released.wait(), timeout=5)
                self.assertEqual((fixture.active_count, fixture.effect_count), (0, 0))
                self.assertIn("cancelled", [event["phase"] for event in fixture.events])
                self.assertNotIn("completed", [event["phase"] for event in fixture.events])
            finally:
                task.cancel()
                try:
                    await task
                except asyncio.CancelledError:
                    pass

    async def test_stdio_cancel_notification_is_observed_before_sdk_release(self):
        with tempfile.TemporaryDirectory() as temporary:
            events = Path(temporary) / "events.jsonl"
            parameters = StdioServerParameters(command=sys.executable, args=[
                str(Path(__file__).resolve().parents[1] / "catalog_fixture.py"),
                "--nonce", "cancel-protocol-fixture", "--events", str(events),
            ])
            async def wait_phase(phase):
                while True:
                    if events.exists():
                        raw = events.read_text()
                        if raw.endswith("\n"):
                            records = [json.loads(line) for line in raw.splitlines()]
                            if any(item["phase"] == phase for item in records):
                                return records
                    await asyncio.sleep(0.01)
            async with stdio_client(parameters) as (read, write):
                async with ClientSession(read, write) as session:
                    await session.initialize()
                    task = asyncio.create_task(session.call_tool("fixture_wait", {"project": "alpha"}))
                    try:
                        records = await asyncio.wait_for(wait_phase("dispatch"), timeout=5)
                        request_id = next(item["request_id"] for item in records if item["phase"] == "request")
                        await session.send_notification(types.ClientNotification.model_validate({
                            "method": "notifications/cancelled", "params": {"requestId": request_id},
                        }))
                        records = await asyncio.wait_for(wait_phase("released"), timeout=5)
                        phases = [item["phase"] for item in records]
                        self.assertIn("protocol_cancel_received", phases)
                        self.assertLess(phases.index("protocol_cancel_received"), phases.index("released"))
                        self.assertEqual(records[-1]["active_count"], 0)
                    finally:
                        task.cancel()
                        try:
                            await task
                        except asyncio.CancelledError:
                            pass
                        except McpError as error:
                            self.assertEqual(error.error.message, "Request cancelled")


if __name__ == "__main__":
    unittest.main()
