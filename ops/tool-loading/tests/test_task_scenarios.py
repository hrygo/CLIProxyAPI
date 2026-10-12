import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from catalog_fixture import confirmation_code
from task_scenarios import get_scenario, output_schema, dispatch_matches, intermediate_receipt_replayed
from live_clients import make_case_spec, result_checks


def output(value):
    return json.dumps({"type": "item.completed", "item": {"type": "agent_message", "text": json.dumps(value)}}) + "\n" + json.dumps({"type": "turn.completed"}) + "\n"


class ScenarioTests(unittest.TestCase):
    def test_output_schema_constrains_shape_without_leaking_golden_receipts(self):
        from jsonschema import Draft202012Validator
        for name in ("lookup", "unavailable", "multitool"):
            schema = output_schema(name)
            Draft202012Validator.check_schema(schema)
            self.assertFalse(schema["additionalProperties"])
            self.assertEqual(set(schema["required"]), set(schema["properties"]))
            self.assertNotIn("424242", json.dumps(schema))
            self.assertNotIn("confirmation_code", json.dumps(schema) if name != "multitool" else "")
        schema = output_schema("lookup")
        self.assertFalse(Draft202012Validator(schema).is_valid({"remaining_budget": True, "verification_code": "x"}))
        schema["properties"]["remaining_budget"]["type"] = "string"
        self.assertEqual(output_schema("lookup")["properties"]["remaining_budget"]["type"], "integer")

    def test_structured_profile_has_distinct_scope_but_identical_prompt_and_gold(self):
        with tempfile.TemporaryDirectory() as temporary:
            config, executable = Path(temporary) / "config", Path(temporary) / "client"
            config.write_bytes(b"fixture-config")
            executable.write_bytes(b"fixture-client")
            profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "openai-compatible-fixture"}
            with patch("live_clients.shutil.which", return_value=str(executable)):
                default = make_case_spec(config, profile, "codex", "eager", "a" * 40, "ep_" + "b" * 32, 90)
                eager, lazy = [make_case_spec(config, profile, "codex", mode, "a" * 40, "ep_" + "b" * 32, 90,
                                              structured_output=True) for mode in ("eager", "lazy")]
            self.assertEqual(default.gold, eager.gold)
            self.assertEqual(default.prompt_hash, eager.prompt_hash)
            self.assertNotEqual(default.scope["fixture_hash"], eager.scope["fixture_hash"])
            self.assertNotEqual(default.pair_id, eager.pair_id)
            self.assertEqual(eager.scope["fixture_hash"], lazy.scope["fixture_hash"])
            self.assertEqual(eager.pair_id, lazy.pair_id)

    def test_title_policy_has_distinct_fixture_scope_without_changing_task_gold(self):
        with tempfile.TemporaryDirectory() as temporary:
            config, executable = Path(temporary) / "config", Path(temporary) / "client"
            config.write_bytes(b"fixture-config")
            executable.write_bytes(b"fixture-client")
            profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "openai-compatible-fixture"}
            with patch("live_clients.shutil.which", return_value=str(executable)):
                def spec(mode, title=None):
                    return make_case_spec(config, profile, "opencode", mode, "a" * 40, "ep_" + "b" * 32, 90, opencode_title=title)
                automatic = spec("eager")
                fixed = spec("eager", "Locked title")
                other = spec("eager", "Other title")
                lazy = spec("lazy", "Locked title")
            self.assertEqual(fixed.gold, automatic.gold)
            self.assertEqual(fixed.prompt_hash, automatic.prompt_hash)
            self.assertNotEqual(fixed.scope["fixture_hash"], automatic.scope["fixture_hash"])
            self.assertNotEqual(fixed.scope["fixture_hash"], other.scope["fixture_hash"])
            self.assertEqual(fixed.scope["fixture_hash"], lazy.scope["fixture_hash"])
            self.assertEqual(fixed.pair_id, lazy.pair_id)
            self.assertNotEqual(automatic.pair_id, fixed.pair_id)

    def test_scenarios_have_distinct_locked_inputs_and_paired_gold(self):
        with tempfile.TemporaryDirectory() as temporary:
            config, executable = Path(temporary) / "config", Path(temporary) / "client"
            config.write_bytes(b"fixture-config")
            executable.write_bytes(b"fixture-client")
            profile = {"client_model": "fixture", "upstream_model": "fixture", "executor": "openai-compatible-fixture"}
            with patch("live_clients.shutil.which", return_value=str(executable)):
                cases = [make_case_spec(config, profile, "codex", mode, "a" * 40, "ep_" + "b" * 32, 90, scenario=name)
                         for name in ("lookup", "unavailable", "multitool") for mode in ("eager", "lazy")]
            self.assertEqual(len({case.case_id for case in cases}), 6)
            self.assertEqual(len({case.pair_id for case in cases}), 3)
            self.assertEqual(len({case.prompt_hash for case in cases}), 3)
            for eager, lazy in zip(cases[::2], cases[1::2]):
                self.assertEqual(eager.gold, lazy.gold)
                self.assertEqual(eager.prompt_hash, lazy.prompt_hash)
            self.assertFalse(cases[2].gold["requires_dispatch"])
            self.assertFalse(cases[2].gold["requires_nonce"])
            self.assertTrue(cases[4].gold["requires_nonce"])
        with self.assertRaises(ValueError):
            get_scenario("unknown")

    def test_unavailable_answer_cannot_reuse_alpha_or_invent_a_receipt(self):
        correct = {"status": "unavailable", "project": "omega"}
        self.assertTrue(result_checks("codex", output(correct), "nonce", scenario="unavailable")["answer"])
        for value in ({**correct, "remaining_budget": 424242}, {**correct, "project": "alpha"}, {"status": "ok", "project": "omega"}):
            self.assertFalse(result_checks("codex", output(value), "nonce", scenario="unavailable")["answer"])

    def test_multitool_answer_requires_both_fresh_receipts(self):
        correct = {"remaining_budget": 424242, "verification_code": "fresh", "confirmation_code": confirmation_code("fresh")}
        self.assertTrue(result_checks("codex", output(correct), "fresh", scenario="multitool")["answer"])
        for value in ({**correct, "confirmation_code": "guessed"}, {**correct, "verification_code": "old"},
                      {**correct, "remaining_budget": True}, {key: value for key, value in correct.items() if key != "confirmation_code"}):
            self.assertFalse(result_checks("codex", output(value), "fresh", scenario="multitool")["answer"])

    def test_dispatch_gold_requires_completed_order_and_no_extra_tool(self):
        events = [{"phase": phase, "tool": name} for name in ("ledger_budget_lookup", "ledger_budget_confirm")
                  for phase in ("dispatch", "completed", "released")]
        self.assertTrue(dispatch_matches("multitool", events))
        self.assertFalse(dispatch_matches("multitool", list(reversed(events))))
        self.assertFalse(dispatch_matches("multitool", [event for event in events if event["phase"] != "completed"]))
        self.assertFalse(dispatch_matches("multitool", events + [{"phase": "dispatch", "tool": "collection_00_read"}]))
        self.assertTrue(dispatch_matches("unavailable", [{"phase": "catalog", "tool": "unknown"}]))
        self.assertFalse(dispatch_matches("unavailable", events))
        self.assertFalse(dispatch_matches("unavailable", []))

    def test_multiround_requires_intermediate_receipt_then_later_confirmation_replay(self):
        first = {"messages": [{"role": "tool", "content": json.dumps({"verification_code": "fresh"})}]}
        second = {"messages": [{"role": "tool", "content": json.dumps({"verification_code": "fresh", "confirmation_code": confirmation_code("fresh")})}]}
        self.assertTrue(intermediate_receipt_replayed([{}, first, second], "fresh"))
        self.assertFalse(intermediate_receipt_replayed([{}, second], "fresh"))
        self.assertFalse(intermediate_receipt_replayed([second, first], "fresh"))
        self.assertFalse(intermediate_receipt_replayed([first, second], "other"))

    def test_multiround_reads_wrapped_results_but_never_prompt_or_arguments(self):
        lookup = 'Wall time: 0.001 seconds\nOutput:\n' + json.dumps({"verification_code": "fresh", "remaining_budget": 424242})
        first = {"messages": [{"role": "tool", "content": lookup}]}
        second = {"messages": [{"role": "tool", "content": json.dumps({"confirmation_code": confirmation_code("fresh")})}]}
        self.assertTrue(intermediate_receipt_replayed([first, second], "fresh"))
        for role in ("user", "assistant", "system"):
            fake = {"messages": [{"role": role, "content": json.dumps({"verification_code": "fresh"})}]}
            self.assertFalse(intermediate_receipt_replayed([fake, second], "fresh"))
        fake = {"input": [{"type": "function_call", "arguments": json.dumps({"verification_code": "fresh"})}]}
        self.assertFalse(intermediate_receipt_replayed([fake, second], "fresh"))


if __name__ == "__main__":
    unittest.main()
