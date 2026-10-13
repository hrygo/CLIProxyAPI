"""Owned multi-file snapshots; atomic publication without executing model code.

Only the fixed source allowlist is exposed. Golden tests and execution backends
belong to the host and are deliberately outside this repository storage API.
"""

from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import stat
from types import MappingProxyType

MAX_FILE_BYTES = 8192
MAX_TREE_BYTES = 16384
HASH = re.compile(r"[0-9a-f]{64}\Z")
REQUIREMENTS = (
    "Fix this four-module ledger API. handle_request(method, path, payload) returns (status, body). "
    "Unknown paths return 404/{error:not_found}; a method other than POST on /v1/balance returns "
    "405/{error:method_not_allowed}. A valid POST body has exactly limit and entries. limit and "
    "every entry amount must be int, never bool; entries must be a list of dicts with exactly "
    "amount and state. States are posted, pending or void. Validate all entries, including pending "
    "and void. Subtract only posted amounts; negative posted amounts are refunds. Return "
    "200/{remaining_budget:integer} for a valid request, and 422/{error:invalid_request} for invalid "
    "bodies. Preserve the module interfaces and repair validation, accounting and API error mapping."
)
BASELINE = MappingProxyType({
    "ledger_model.py": "from dataclasses import dataclass\n\n@dataclass(frozen=True)\nclass Entry:\n    amount: int\n    state: str\n",
    "validation.py": "from ledger_model import Entry\n\ndef parse_request(payload):\n    limit = payload['limit']\n    if not isinstance(limit, int):\n        raise ValueError('invalid limit')\n    return limit, [Entry(item['amount'], item['state']) for item in payload['entries']]\n",
    "ledger.py": "def remaining_budget(limit, entries):\n    return limit - sum(entry.amount for entry in entries)\n",
    "api.py": "from validation import parse_request\nfrom ledger import remaining_budget\n\ndef handle_request(method, path, payload):\n    if path != '/v1/balance':\n        return 404, {'error': 'not_found'}\n    try:\n        limit, entries = parse_request(payload)\n        return 200, {'remaining_budget': remaining_budget(limit, entries)}\n    except ValueError:\n        return 500, {'error': 'internal_error'}\n",
})


class RepositoryError(ValueError):
    CODES = {"repository_boundary", "invalid_arguments", "repository_revision_mismatch",
             "repository_file_revision_mismatch", "repository_busy", "repository_commit_uncertain", "permission_revoked"}

    def __init__(self, code):
        self.code = code if code in self.CODES else "repository_boundary"
        super().__init__(self.code)


class RepositoryCommitUncertain(RepositoryError):
    """The new pointer is visible; its crash durability could not be confirmed."""

    def __init__(self, revision, changed_files=None):
        super().__init__("repository_commit_uncertain")
        self.receipt = {"revision": revision, "changed_files": changed_files or []}


def _canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode()


def _sha(content):
    return hashlib.sha256(content).hexdigest()


def _tree_revision(files):
    return _sha(_canonical(files))


def _unique(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise RepositoryError("repository_boundary")
        result[key] = value
    return result


def _identity(info):
    return info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns


def _read_regular(directory, name, maximum):
    descriptor = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
    with os.fdopen(descriptor, "rb") as stream:
        before = os.fstat(stream.fileno())
        if not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or not 0 <= before.st_size <= maximum:
            raise RepositoryError("repository_boundary")
        content = stream.read(maximum + 1)
        after = os.fstat(stream.fileno())
    if len(content) != before.st_size or _identity(before) != _identity(after) or _identity(after) != _identity(os.stat(name, dir_fd=directory, follow_symlinks=False)):
        raise RepositoryError("repository_boundary")
    return content


class RepositoryProject:
    def __init__(self, root, owner):
        self.root = Path(root).absolute()
        if not isinstance(owner, str) or not HASH.fullmatch(owner) or self.root.is_symlink():
            raise RepositoryError("repository_boundary")
        info = self.root.stat()
        if not stat.S_ISDIR(info.st_mode):
            raise RepositoryError("repository_boundary")
        self.identity = info.st_dev, info.st_ino
        self.owner = owner

    @classmethod
    def create(cls, root):
        try:
            Path(root).mkdir(mode=0o700)
            project = cls(root, secrets.token_hex(32))
            directory = project._directory()
            try:
                lock = os.open(".repository.lock", os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600, dir_fd=directory)
                os.close(lock)
                revision = _tree_revision(dict(BASELINE))
                project._write_generation(directory, revision, dict(BASELINE))
                project._publish(directory, revision)
            finally:
                os.close(directory)
            return project
        except RepositoryCommitUncertain as error:
            # Only the host receives this handle; the MCP error receipt never
            # includes the owner credential needed to reopen the workspace.
            error.project = project
            raise
        except (OSError, UnicodeError) as error:
            raise RepositoryError("repository_boundary") from error

    @classmethod
    def open(cls, root, expected_owner):
        try:
            project = cls(root, expected_owner)
            project.read()
            return project
        except (OSError, UnicodeError) as error:
            raise RepositoryError("repository_boundary") from error

    def _directory(self):
        descriptor = os.open(self.root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        info = os.fstat(descriptor)
        if (info.st_dev, info.st_ino) != self.identity:
            os.close(descriptor)
            raise RepositoryError("repository_boundary")
        return descriptor

    @contextmanager
    def _locked(self):
        directory, lock = None, None
        try:
            directory = self._directory()
            lock = os.open(".repository.lock", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
            info = os.fstat(lock)
            if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
                raise RepositoryError("repository_boundary")
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError as error:
                raise RepositoryError("repository_busy") from error
            if _identity(info) != _identity(os.stat(".repository.lock", dir_fd=directory, follow_symlinks=False)):
                raise RepositoryError("repository_boundary")
            yield directory
        except (OSError, UnicodeError, json.JSONDecodeError) as error:
            raise RepositoryError("repository_boundary") from error
        finally:
            if lock is not None:
                os.close(lock)
            if directory is not None:
                os.close(directory)

    def _state(self, directory):
        value = json.loads(_read_regular(directory, "state.json", 4096), object_pairs_hook=_unique)
        if (not isinstance(value, dict) or set(value) != {"version", "owner", "revision", "baseline_revision"}
                or type(value["version"]) is not int or value["version"] != 1 or value["owner"] != self.owner
                or not isinstance(value["revision"], str) or not HASH.fullmatch(value["revision"])
                or value["baseline_revision"] != _tree_revision(dict(BASELINE))):
            raise RepositoryError("repository_boundary")
        return value

    def _files(self, directory, revision, *, name=None):
        name = name or "generation_" + revision
        snapshot = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory)
        try:
            info = os.fstat(snapshot)
            if set(os.listdir(snapshot)) != set(BASELINE):
                raise RepositoryError("repository_boundary")
            files = {path: _read_regular(snapshot, path, MAX_FILE_BYTES).decode() for path in sorted(BASELINE)}
            if (sum(len(source.encode()) for source in files.values()) > MAX_TREE_BYTES
                    or _tree_revision(files) != revision
                    or _identity(info) != _identity(os.stat(name, dir_fd=directory, follow_symlinks=False))):
                raise RepositoryError("repository_boundary")
            return files
        finally:
            os.close(snapshot)

    def read(self):
        with self._locked() as directory:
            state = self._state(directory)
            files = self._files(directory, state["revision"])
            return {"revision": state["revision"], "baseline_revision": state["baseline_revision"],
                    "files": {path: {"source": source, "sha256": _sha(source.encode())} for path, source in files.items()},
                    "requirements": REQUIREMENTS}

    def validate_patch(self, expected_revision, changes):
        if (type(expected_revision) is not str or not HASH.fullmatch(expected_revision)
                or type(changes) is not list or not 1 <= len(changes) <= len(BASELINE)):
            raise RepositoryError("invalid_arguments")
        # Copy JSON objects before validation; callers cannot change a validated
        # path or source while the workspace is being read or locked.
        changes = [dict(change) if type(change) is dict else None for change in changes]
        if not 1 <= len(changes) <= len(BASELINE):
            raise RepositoryError("invalid_arguments")
        paths = set()
        for change in changes:
            if (not isinstance(change, dict) or set(change) != {"path", "expected_sha256", "source"}
                    or type(change["path"]) is not str or change["path"] not in BASELINE or change["path"] in paths
                    or type(change["expected_sha256"]) is not str or not HASH.fullmatch(change["expected_sha256"])
                    or type(change["source"]) is not str or not change["source"]):
                raise RepositoryError("invalid_arguments")
            try:
                if len(change["source"].encode()) > MAX_FILE_BYTES:
                    raise RepositoryError("invalid_arguments")
            except UnicodeError as error:
                raise RepositoryError("invalid_arguments") from error
            paths.add(change["path"])
        snapshot = self.read()
        if snapshot["revision"] != expected_revision:
            raise RepositoryError("repository_revision_mismatch")
        files = {path: value["source"] for path, value in snapshot["files"].items()}
        for change in changes:
            if snapshot["files"][change["path"]]["sha256"] != change["expected_sha256"]:
                raise RepositoryError("repository_file_revision_mismatch")
            files[change["path"]] = change["source"]
        if sum(len(source.encode()) for source in files.values()) > MAX_TREE_BYTES:
            raise RepositoryError("invalid_arguments")
        return files

    def preview_patch(self, expected_revision, changes):
        files = self.validate_patch(expected_revision, changes)
        snapshot = self.read()
        if snapshot["revision"] != expected_revision:
            raise RepositoryError("repository_revision_mismatch")
        return {"revision": _tree_revision(files),
                "changed_files": sorted(path for path in files if files[path] != snapshot["files"][path]["source"])}

    def _write_generation(self, directory, revision, files):
        if set(files) != set(BASELINE) or _tree_revision(files) != revision:
            raise RepositoryError("repository_boundary")
        name = "generation_" + revision
        try:
            os.stat(name, dir_fd=directory, follow_symlinks=False)
        except FileNotFoundError:
            pass
        else:
            if self._files(directory, revision) != files:
                raise RepositoryError("repository_boundary")
            os.fsync(directory)
            return
        # A crash may leave this private staging directory, but cannot poison the
        # content-addressed name used by a retry of the same requested revision.
        temporary = ".generation_" + secrets.token_hex(16)
        os.mkdir(temporary, mode=0o700, dir_fd=directory)
        snapshot = os.open(temporary, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory)
        try:
            for path, source in sorted(files.items()):
                descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600, dir_fd=snapshot)
                with os.fdopen(descriptor, "wb") as stream:
                    stream.write(source.encode())
                    stream.flush()
                    os.fsync(stream.fileno())
            os.fsync(snapshot)
        finally:
            os.close(snapshot)
        if self._files(directory, revision, name=temporary) != files:
            raise RepositoryError("repository_boundary")
        os.rename(temporary, name, src_dir_fd=directory, dst_dir_fd=directory)
        os.fsync(directory)

    def _publish(self, directory, revision, *, authorize=None):
        value = {"version": 1, "owner": self.owner, "revision": revision,
                 "baseline_revision": _tree_revision(dict(BASELINE))}
        temporary = ".state_" + secrets.token_hex(16)
        descriptor = os.open(temporary, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600, dir_fd=directory)
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(_canonical(value))
            stream.flush()
            os.fsync(stream.fileno())
        # This is the authorization decision for this commit. Permission updates
        # are external to this filesystem transaction, not a shared atomic lock.
        if authorize is not None:
            authorize()
        os.replace(temporary, "state.json", src_dir_fd=directory, dst_dir_fd=directory)
        try:
            os.fsync(directory)
        except OSError as error:
            raise RepositoryCommitUncertain(revision) from error

    def patch(self, expected_revision, changes, *, authorize=None):
        files = self.validate_patch(expected_revision, changes)
        with self._locked() as directory:
            state = self._state(directory)
            if state["revision"] != expected_revision:
                raise RepositoryError("repository_revision_mismatch")
            before = self._files(directory, state["revision"])
            revision = _tree_revision(files)
            changed = sorted(path for path in files if before[path] != files[path])
            if changed:
                if authorize is not None:
                    authorize()
                self._write_generation(directory, revision, files)
                try:
                    self._publish(directory, revision, authorize=authorize)
                except RepositoryCommitUncertain as error:
                    raise RepositoryCommitUncertain(revision, changed) from error
            return {"revision": revision, "changed_files": changed}


def repository_tools():
    return [
        {"name": "repository_read", "description": "Read the owned four-module ledger API requirements, source files and exact tree/file revisions.",
         "inputSchema": {"type": "object", "properties": {}, "additionalProperties": False},
         "annotations": {"readOnlyHint": True, "destructiveHint": False}},
        {"name": "repository_patch", "description": "Atomically update allowed repository source files. Bind the current tree revision and each edited file revision; all edits publish together.",
         "inputSchema": {"type": "object", "properties": {
             "expected_revision": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
             "changes": {"type": "array", "minItems": 1, "maxItems": len(BASELINE), "items": {
                 "type": "object", "properties": {"path": {"type": "string", "enum": sorted(BASELINE)},
                     "expected_sha256": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
                     "source": {"type": "string", "minLength": 1, "maxLength": MAX_FILE_BYTES}},
                 "required": ["path", "expected_sha256", "source"], "additionalProperties": False}}},
             "required": ["expected_revision", "changes"], "additionalProperties": False},
         "annotations": {"readOnlyHint": False, "destructiveHint": False}},
    ]
