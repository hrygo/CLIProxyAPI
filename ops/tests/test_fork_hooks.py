"""Tests for the fork hook inventory check used during a replay."""

from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
SCRIPT = REPO_ROOT / "ops" / "upstream-intake" / "check-fork-hooks.py"


class ForkHookInventoryTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.repo = Path(self.tmp.name)
        self.git("init", "-q")
        self.hooks = self.repo / "hooks.txt"
        self.hooks.write_text("# comment\nHookSymbol\n", encoding="utf-8")
        (self.repo / "a.go").write_text("func a() { HookSymbol(1) }\n", encoding="utf-8")
        (self.repo / "keep.go").write_text("func k() { HookSymbol(2) }\n", encoding="utf-8")
        self.commit("baseline")

    def git(self, *args: str) -> str:
        return subprocess.run(
            ["git", "-c", "user.name=t", "-c", "user.email=t@example.com", *args],
            cwd=self.repo,
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        ).stdout

    def commit(self, message: str) -> None:
        self.git("add", "-A")
        self.git("commit", "-q", "-m", message)

    def run_check(
        self, baseline: str, allow: Path | None = None
    ) -> subprocess.CompletedProcess:
        return subprocess.run(
            [
                "python3",
                str(SCRIPT),
                "--baseline",
                baseline,
                "--hooks",
                str(self.hooks),
                "--allow",
                str(allow or (self.repo / "allow.txt")),
            ],
            cwd=self.repo,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )

    def test_intact_hook_passes(self) -> None:
        result = self.run_check("HEAD")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_lost_hook_in_existing_file_fails(self) -> None:
        (self.repo / "a.go").write_text("func a() { other() }\n", encoding="utf-8")
        self.commit("take upstream version of a.go")
        result = self.run_check("HEAD~1")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("a.go", result.stdout)

    def test_deleted_file_is_a_retirement_not_a_loss(self) -> None:
        (self.repo / "a.go").unlink()
        self.commit("retire a.go")
        result = self.run_check("HEAD~1")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_recorded_relocation_passes(self) -> None:
        (self.repo / "a.go").write_text("func a() { other() }\n", encoding="utf-8")
        self.commit("take upstream version of a.go")
        allow = self.repo / "allow.txt"
        allow.write_text("HookSymbol a.go  # moved into keep.go\n", encoding="utf-8")
        result = self.run_check("HEAD~1", allow)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("recorded relocation", result.stdout)

    def test_malformed_allow_entry_fails(self) -> None:
        allow = self.repo / "allow.txt"
        allow.write_text("HookSymbol\n", encoding="utf-8")
        result = self.run_check("HEAD", allow)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)

    def test_missing_hook_list_is_an_error(self) -> None:
        result = subprocess.run(
            ["python3", str(SCRIPT), "--hooks", str(self.repo / "absent.txt")],
            cwd=self.repo,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.assertEqual(result.returncode, 2, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
