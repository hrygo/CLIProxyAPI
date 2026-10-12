"""A successful abort HTTP response alone never proves cancelled execution."""

from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from opencode_server_adapter import abort_terminal


class AbortTerminalTests(unittest.TestCase):
    def test_requires_actual_session_error_ack_and_idle_state(self):
        message = {"info": {"sessionID": "ses_fixture", "role": "assistant",
                            "error": {"name": "MessageAbortedError", "data": {"message": "Aborted"}}}}
        self.assertFalse(abort_terminal("ses_fixture", True, {}, []))
        self.assertFalse(abort_terminal("ses_fixture", False, {}, [message]))
        self.assertFalse(abort_terminal("ses_fixture", True, {"ses_fixture": {"type": "busy"}}, [message]))
        self.assertFalse(abort_terminal("another_session", True, {}, [message]))
        self.assertTrue(abort_terminal("ses_fixture", True, {}, [message]))


if __name__ == "__main__":
    unittest.main()
