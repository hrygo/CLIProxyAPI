"""Reject unpinned external CodeMode source before executing it."""

import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class SourcePinTests(unittest.TestCase):
    @unittest.skipUnless(shutil.which("node"), "Node is required to check the external probe admission")
    def test_modified_sdk_source_is_rejected_before_loading_dependencies(self):
        manifest = json.loads((ROOT / "opencode-codemode-source.json").read_text())
        with tempfile.TemporaryDirectory() as temporary:
            sdk = Path(temporary) / "sdk"
            for name in manifest["files"]:
                source = sdk / name
                source.parent.mkdir(parents=True, exist_ok=True)
                source.write_text("throw new Error('unexpected code execution');\n")
            output = Path(temporary) / "summary.json"
            result = subprocess.run([shutil.which("node"), str(ROOT / "client_sdk_probe.mjs"),
                                     "--sdk-root", str(sdk), "--output", str(output)],
                                    capture_output=True, text=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("source digest mismatch", result.stderr)
            self.assertNotIn("unexpected code execution", result.stderr)
            self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()
