"""App-server cancellation must bind the actual thread and turn identities."""

from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from codex_app_adapter import AppTurn


class AppTurnTests(unittest.TestCase):
    def initialized(self):
        state = AppTurn("/fixture", "fixture-model", "read fixture")
        self.assertEqual(state.start()["method"], "initialize")
        messages = state.accept({"id": 1, "result": {}})
        self.assertEqual([item["method"] for item in messages], ["initialized", "thread/start"])
        messages = state.accept({"id": 2, "result": {"thread": {"id": "thread-real"}}})
        self.assertEqual(messages[0]["method"], "turn/start")
        self.assertEqual(state.cancel(), [])
        state.accept({"id": 3, "result": {"turn": {"id": "turn-real", "status": "inProgress"}}})
        return state

    def test_interrupt_requires_real_turn_identity_and_ack_plus_matching_terminal(self):
        state = self.initialized()
        self.assertEqual(state.cancel(), [{"id": 4, "method": "turn/interrupt",
                                           "params": {"threadId": "thread-real", "turnId": "turn-real"}}])
        self.assertEqual(state.cancel(), [])
        self.assertFalse(state.cancel_verified)
        state.accept({"id": 4, "result": {}})
        self.assertFalse(state.cancel_verified)
        state.accept({"method": "turn/completed", "params": {"threadId": "thread-real",
                     "turn": {"id": "turn-real", "status": "interrupted"}}})
        self.assertTrue(state.cancel_verified)

    def test_unrelated_terminal_and_completed_turn_cannot_prove_interruption(self):
        state = self.initialized()
        state.cancel()
        state.accept({"id": 4, "result": {}})
        with self.assertRaises(ValueError):
            state.accept({"method": "turn/completed", "params": {"threadId": "another-thread",
                         "turn": {"id": "turn-real", "status": "interrupted"}}})
        self.assertFalse(state.cancel_verified)
        state.accept({"method": "turn/completed", "params": {"threadId": "thread-real",
                     "turn": {"id": "turn-real", "status": "completed"}}})
        self.assertFalse(state.cancel_verified)

    def test_rpc_error_is_not_an_interrupt_ack_and_server_requests_are_denied(self):
        state = self.initialized()
        state.cancel()
        with self.assertRaises(ValueError):
            state.accept({"id": 4, "error": {"code": -1, "message": "no turn"}})
        self.assertFalse(state.cancel_verified)
        reply = state.accept({"id": "server-request", "method": "item/tool/requestApproval", "params": {}})
        self.assertEqual(reply[0]["error"]["code"], -32601)
        self.assertNotIn("result", reply[0])

    def test_unsolicited_responses_and_empty_identifiers_are_rejected(self):
        state = AppTurn("/fixture", "fixture-model", "read fixture")
        state.start()
        with self.assertRaises(ValueError):
            state.accept({"id": 2, "result": {"thread": {"id": "unsolicited"}}})
        state.accept({"id": 1, "result": {}})
        with self.assertRaises(ValueError):
            state.accept({"id": 2, "result": {"thread": {"id": None}}})

    def test_unadmitted_null_terminal_identities_cannot_match(self):
        state = AppTurn("/fixture", "fixture-model", "read fixture")
        with self.assertRaises(ValueError):
            state.accept({"method": "turn/completed", "params": {"threadId": None,
                         "turn": {"id": None, "status": "completed"}}})
        self.assertIsNone(state.terminal_status)


if __name__ == "__main__":
    unittest.main()
