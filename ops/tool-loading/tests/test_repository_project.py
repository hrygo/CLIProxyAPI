"""Multi-file ownership, revision and atomic publication boundaries."""

from concurrent.futures import ThreadPoolExecutor
from copy import deepcopy
import errno
import hashlib
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from repository_project import BASELINE, RepositoryError, RepositoryProject


class RepositoryProjectTests(unittest.TestCase):
    def create(self, directory):
        return RepositoryProject.create(Path(directory) / "repository")

    def change(self, snapshot, path, source=None):
        return {"path": path, "expected_sha256": snapshot["files"][path]["sha256"],
                "source": source or snapshot["files"][path]["source"] + "\n# Changed by the fixture test.\n"}

    def test_fresh_baseline_is_real_multifile_and_reopens_only_with_owner(self):
        with tempfile.TemporaryDirectory() as directory:
            project = self.create(directory)
            snapshot = project.read()
            self.assertEqual(set(snapshot["files"]), set(BASELINE))
            self.assertGreaterEqual(len(snapshot["files"]), 4)
            self.assertEqual(snapshot["revision"], snapshot["baseline_revision"])
            reopened = RepositoryProject.open(project.root, project.owner)
            self.assertEqual(reopened.read(), snapshot)
            for path, value in snapshot["files"].items():
                self.assertEqual(value["sha256"], hashlib.sha256(value["source"].encode()).hexdigest())
                source_path = project.root / ("generation_" + snapshot["revision"]) / path
                self.assertEqual(source_path.read_text(), BASELINE[path])
            with self.assertRaises(RepositoryError):
                RepositoryProject.open(project.root, "0" * 64)
            with self.assertRaises(RepositoryError):
                RepositoryProject.create(project.root)

    def test_two_file_patch_publishes_one_new_revision_and_keeps_baseline(self):
        with tempfile.TemporaryDirectory() as directory:
            project = self.create(directory)
            before = project.read()
            changes = [self.change(before, "api.py"), self.change(before, "ledger.py")]
            result = project.patch(before["revision"], changes)
            after = project.read()
            self.assertEqual(result["revision"], after["revision"])
            self.assertEqual(result["changed_files"], ["api.py", "ledger.py"])
            self.assertNotEqual(before["revision"], after["revision"])
            self.assertEqual(after["baseline_revision"], before["revision"])
            self.assertEqual(after["files"]["validation.py"], before["files"]["validation.py"])
            self.assertEqual((project.root / ("generation_" + before["revision"]) / "api.py").read_text(), BASELINE["api.py"])

    def test_invalid_second_edit_and_stale_tree_or_file_have_zero_writes(self):
        with tempfile.TemporaryDirectory() as directory:
            project = self.create(directory)
            before = project.read()
            entries = sorted(path.name for path in project.root.iterdir())
            invalid = [self.change(before, "api.py"), self.change(before, "ledger.py")]
            invalid[1]["expected_sha256"] = "0" * 64
            for revision, changes in ((before["revision"], invalid), ("0" * 64, [self.change(before, "api.py")])):
                with self.assertRaises(RepositoryError):
                    project.patch(revision, changes)
                self.assertEqual(project.read(), before)
                self.assertEqual(sorted(path.name for path in project.root.iterdir()), entries)

    def test_paths_duplicate_edits_utf8_bytes_and_unknown_fields_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            project = self.create(directory)
            before = project.read()
            edit = self.change(before, "api.py")
            for changes in ([{**edit, "path": "../private.py"}], [{**edit, "path": "/tmp/out.py"}],
                            [edit, edit], [{**edit, "unexpected": True}],
                            [{**edit, "source": "界" * 8192}], [{**edit, "source": "\ud800"}], []):
                with self.subTest(changes=changes), self.assertRaises(RepositoryError):
                    project.patch(before["revision"], changes)
            self.assertEqual(project.read(), before)

    def test_same_revision_two_writers_commit_only_one(self):
        with tempfile.TemporaryDirectory() as directory:
            project = self.create(directory)
            other = RepositoryProject.open(project.root, project.owner)
            before = project.read()
            barrier = threading.Barrier(2)
            def attempt(actor, name):
                barrier.wait()
                try:
                    actor.patch(before["revision"], [self.change(before, name)])
                    return "committed"
                except RepositoryError as error:
                    return error.code
            with ThreadPoolExecutor(max_workers=2) as pool:
                outcomes = [future.result() for future in [pool.submit(attempt, project, "api.py"), pool.submit(attempt, other, "ledger.py")]]
            self.assertEqual(outcomes.count("committed"), 1)
            self.assertIn(next(outcome for outcome in outcomes if outcome != "committed"),
                          {"repository_revision_mismatch", "repository_busy"})
            self.assertEqual(project.read(), other.read())

    def test_symlink_hardlink_content_drift_and_root_replacement_fail_closed(self):
        for kind in ("symlink", "hardlink", "drift", "root"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as directory:
                project = self.create(directory)
                before = project.read()
                target = project.root / ("generation_" + before["revision"]) / "api.py"
                external = Path(directory) / "external.py"
                external.write_text("sentinel\n")
                if kind == "symlink":
                    target.unlink()
                    target.symlink_to(external)
                elif kind == "hardlink":
                    target.unlink()
                    os.link(external, target)
                elif kind == "drift":
                    target.write_text("changed externally\n")
                else:
                    project.root.rename(Path(directory) / "original")
                    project.root.mkdir()
                with self.assertRaises(RepositoryError):
                    project.read()
                with self.assertRaises(RepositoryError):
                    project.patch(before["revision"], [self.change(before, "api.py")])
                self.assertEqual(external.read_text(), "sentinel\n")

    def test_duplicate_state_fields_and_unknown_snapshot_files_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            project = self.create(directory)
            before = project.read()
            state = project.root / "state.json"
            content = state.read_text()
            state.write_text(content[:-1] + ',"version":1}')
            with self.assertRaises(RepositoryError):
                project.read()
            state.write_text(content)
            (project.root / ("generation_" + before["revision"]) / "gold.py").write_text("unexpected\n")
            with self.assertRaises(RepositoryError):
                project.read()

    def test_failure_before_pointer_publication_leaves_entire_visible_tree_unchanged(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory:
            project = self.create(directory)
            before = project.read()
            with patch("repository_project.os.replace", side_effect=OSError("synthetic publication failure")):
                with self.assertRaises(RepositoryError):
                    project.patch(before["revision"], [self.change(before, "api.py"), self.change(before, "ledger.py")])
            self.assertEqual(project.read(), before)

    def test_interrupted_generation_can_retry_the_same_patch(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory:
            project = self.create(directory)
            before = project.read()
            changes = [self.change(before, "api.py"), self.change(before, "ledger.py")]
            with patch("repository_project.os.fsync", side_effect=OSError("synthetic generation failure")):
                with self.assertRaises(RepositoryError):
                    project.patch(before["revision"], changes)
            self.assertEqual(project.read(), before)
            result = project.patch(before["revision"], changes)
            self.assertEqual(project.read()["revision"], result["revision"])
            self.assertCountEqual(result["changed_files"], ["api.py", "ledger.py"])

    def test_pointer_visible_but_directory_fsync_failed_has_uncertain_receipt(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory:
            project = self.create(directory)
            before = project.read()
            actual_fsync = os.fsync
            def fail_after_publication(descriptor):
                info = os.fstat(descriptor)
                if ((info.st_dev, info.st_ino) == project.identity
                        and json.loads((project.root / "state.json").read_text())["revision"] != before["revision"]):
                    raise OSError("synthetic durability failure")
                return actual_fsync(descriptor)
            with patch("repository_project.os.fsync", side_effect=fail_after_publication):
                with self.assertRaises(RepositoryError) as raised:
                    project.patch(before["revision"], [self.change(before, "api.py")])
            after = project.read()
            self.assertNotEqual(after["revision"], before["revision"])
            self.assertEqual(raised.exception.code, "repository_commit_uncertain")
            self.assertEqual(raised.exception.receipt, {"revision": after["revision"], "changed_files": ["api.py"]})

    def test_lock_contention_is_nonblocking_and_distinct_from_boundary_failure(self):
        from unittest.mock import patch
        import fcntl
        with tempfile.TemporaryDirectory() as directory:
            project = self.create(directory)
            with patch("repository_project.fcntl.flock", side_effect=BlockingIOError(errno.EWOULDBLOCK, "busy")) as locking:
                with self.assertRaises(RepositoryError) as raised:
                    project.read()
            self.assertEqual(raised.exception.code, "repository_busy")
            self.assertEqual(locking.call_args.args[1], fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.assertEqual(project.read()["revision"], project.read()["baseline_revision"])

    def test_uncertain_initialization_keeps_a_host_only_recovery_handle(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "repository"
            actual_fsync = os.fsync
            def fail_after_initialization(descriptor):
                info = os.fstat(descriptor)
                if root.exists() and (root / "state.json").exists():
                    expected = root.stat()
                    if (info.st_dev, info.st_ino) == (expected.st_dev, expected.st_ino):
                        raise OSError("synthetic initialization durability failure")
                return actual_fsync(descriptor)
            with patch("repository_project.os.fsync", side_effect=fail_after_initialization):
                with self.assertRaises(RepositoryError) as raised:
                    RepositoryProject.create(root)
            self.assertEqual(raised.exception.code, "repository_commit_uncertain")
            project = raised.exception.project
            self.assertEqual(project.root, root)
            self.assertEqual(project.read()["revision"], raised.exception.receipt["revision"])
            self.assertEqual(RepositoryProject.open(root, project.owner).read(), project.read())
            self.assertNotIn("owner", raised.exception.receipt)


if __name__ == "__main__":
    unittest.main()
