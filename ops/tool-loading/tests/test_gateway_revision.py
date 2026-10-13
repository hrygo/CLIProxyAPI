"""Live evidence cannot claim a caller-supplied, unverified binary revision."""

from contextlib import redirect_stderr
import io
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))


class GatewayRevisionTests(unittest.TestCase):
    def test_binary_build_info_requires_exact_clean_git_revision(self):
        from live_gateway import verify_gateway_revision
        revision = "a" * 40
        settings = "\tbuild\tvcs=git\n\tbuild\tvcs.revision=" + revision + "\n\tbuild\tvcs.modified=false\n"
        with tempfile.TemporaryDirectory() as temporary:
            binary = Path(temporary) / "gateway"
            binary.write_bytes(b"synthetic-go-binary")
            with patch("live_gateway.subprocess.run", return_value=subprocess.CompletedProcess([], 0, settings, "")) as read:
                verify_gateway_revision(binary, revision)
            self.assertEqual(read.call_args.args[0][-3:], ["version", "-m", str(binary.resolve())])
            self.assertEqual(read.call_args.kwargs["env"]["GOTOOLCHAIN"], "local")
            for value in (settings.replace(revision, "b" * 40), settings.replace("false", "true"),
                          settings.replace("vcs=git", "vcs=svn"), settings.replace("vcs.modified=false\n", ""),
                          settings + "\tbuild\tvcs.revision=" + revision + "\n", "not a Go executable"):
                with self.subTest(value=value), patch("live_gateway.subprocess.run", return_value=subprocess.CompletedProcess([], 0, value, "")), self.assertRaises(ValueError):
                    verify_gateway_revision(binary, revision)
            with patch("live_gateway.subprocess.run", side_effect=subprocess.TimeoutExpired("go", 10)), self.assertRaises(ValueError):
                verify_gateway_revision(binary, revision)
            with patch("live_gateway.subprocess.run", return_value=subprocess.CompletedProcess([], 1, settings, "")), self.assertRaises(ValueError):
                verify_gateway_revision(binary, revision)
            with patch("live_gateway.subprocess.run") as read, self.assertRaises(ValueError):
                verify_gateway_revision(binary, "short")
            read.assert_not_called()

    def test_replacement_during_build_info_probe_is_rejected(self):
        from live_gateway import verify_gateway_revision
        revision = "a" * 40
        with tempfile.TemporaryDirectory() as temporary:
            binary = Path(temporary) / "gateway"
            binary.write_bytes(b"before")
            def replace(*args, **kwargs):
                binary.write_bytes(b"after")
                return subprocess.CompletedProcess([], 0, "\tbuild\tvcs=git\n\tbuild\tvcs.revision=" + revision + "\n\tbuild\tvcs.modified=false\n", "")
            with patch("live_gateway.subprocess.run", side_effect=replace), self.assertRaises(ValueError):
                verify_gateway_revision(binary, revision)

    def test_live_entry_points_reject_revision_before_any_case_or_candidate(self):
        from live_clients import main as single
        from live_matrix import main as matrix
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for name, main, tail in (("live_clients", single, ["--endpoint-sha256", "b" * 64, "--client-model", "fixture", "--upstream-model", "fixture", "--executor", "codex"]),
                                     ("live_matrix", matrix, ["--declaration", str(root / "unused.json")])):
                output = root / name
                argv = [name, "--gateway-binary", str(root / "gateway"), "--config", str(root / "config"),
                        "--output-dir", str(output), "--gateway-revision", "a" * 40, *tail]
                with patch("sys.argv", argv), patch(name + ".verify_gateway_revision", side_effect=ValueError("binary revision mismatch")), \
                        patch(name + ".run_case") as launch, redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as exit:
                    main()
                self.assertEqual(exit.exception.code, 2)
                self.assertFalse(output.exists())
                launch.assert_not_called()


if __name__ == "__main__":
    unittest.main()
