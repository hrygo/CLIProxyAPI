"""Read-only isolated candidate lifecycle; never claims live certification."""

import argparse
from contextlib import contextmanager
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import select
import signal
import socket
import stat
import subprocess
import time
from urllib.parse import urlsplit

from client_adapters import ENV_KEYS, write_private


FAILURE_REASONS = {
    "arguments": {"invalid_arguments", "invalid_profile", "credential_binding_invalid", "unsafe_environment"},
    "configuration": {"configuration_unavailable", "configuration_invalid", "policy_invalid", "configuration_changed"},
    "credentials": {"binding_source_unavailable", "binding_source_invalid", "binding_source_changed", "binding_source_missing",
                    "binding_content_mismatch", "binding_required", "profile_mismatch", "snapshot_unavailable"},
    "startup": {"startup_failed"}, "evidence": {"evidence_failed"}, "unknown": {"candidate_failed"},
}


def _valid_failures(value):
    return (isinstance(value, list) and 1 <= len(value) <= 8 and all(
        isinstance(item, dict) and set(item) == {"stage", "reason"} and isinstance(item["stage"], str)
        and isinstance(item["reason"], str) and item["reason"] in FAILURE_REASONS.get(item["stage"], set()) for item in value))


class CandidateFailure(ValueError):
    def __init__(self, diagnostics):
        if not _valid_failures(diagnostics):
            raise ValueError("invalid candidate failure diagnostics")
        self.diagnostics = [dict(item) for item in diagnostics]
        super().__init__("isolated candidate failed; evidence remains unverified")


def read_candidate_failure(path):
    from http_attempts import _json
    try:
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
        with os.fdopen(descriptor, "rb") as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or not 0 < info.st_size <= 65536:
                return None
            raw = stream.read(65537)
            after = os.fstat(stream.fileno())
        identity = lambda value: (value.st_dev, value.st_ino, value.st_size, value.st_mtime_ns, value.st_ctime_ns)
        if (len(raw) != info.st_size or identity(info) != identity(after)
                or identity(after) != identity(os.stat(path, follow_symlinks=False))):
            return None
        envelope = _json(raw)
        if (not isinstance(envelope, dict) or set(envelope) != {"version", "scope", "failures"}
                or type(envelope["version"]) is not int or envelope["version"] != 1
                or envelope["scope"] != "isolated-candidate-error" or not _valid_failures(envelope["failures"])):
            return None
        return envelope["failures"]
    except (OSError, ValueError):
        return None


def verify_gateway_revision(binary, revision):
    """Attest the clean Go build before any evidence claims its revision."""
    from batch_plan import fingerprint_materials
    if not isinstance(revision, str) or re.fullmatch(r"(?:[0-9a-f]{40}|[0-9a-f]{64})", revision) is None:
        raise ValueError("complete gateway revision required")
    binary = Path(binary).resolve()
    before = fingerprint_materials({"gateway-binary": binary})["gateway-binary"]
    env = {key: os.environ[key] for key in ENV_KEYS if key in os.environ}
    # Inspect an existing executable only; never select/download a toolchain or
    # read the user's persistent Go environment to satisfy a live preflight.
    env.update(GOTOOLCHAIN="local", GOENV="off", GOWORK="off")
    try:
        result = subprocess.run(["go", "version", "-m", str(binary)], env=env, stdin=subprocess.DEVNULL,
                                capture_output=True, text=True, timeout=10, check=False)
    except (OSError, subprocess.SubprocessError):
        raise ValueError("gateway build metadata unavailable") from None
    if result.returncode != 0 or len(result.stdout) > 1048576:
        raise ValueError("gateway build metadata unavailable")
    settings = {}
    for line in result.stdout.splitlines():
        columns = line.split("\t")
        if len(columns) == 3 and columns[1] == "build" and columns[2].startswith("vcs"):
            key, separator, value = columns[2].partition("=")
            if not separator or key in settings:
                raise ValueError("ambiguous gateway build metadata")
            settings[key] = value
    if (settings.get("vcs") != "git" or settings.get("vcs.revision") != revision
            or settings.get("vcs.modified") != "false"
            or fingerprint_materials({"gateway-binary": binary})["gateway-binary"] != before):
        raise ValueError("gateway binary is dirty or differs from the declared revision")
    return before


def parse_credential_bindings(values, executor):
    if not isinstance(values, list) or len(values) > 8:
        raise ValueError("credential selection budget exceeded")
    bindings = []
    for value in values:
        if not isinstance(value, str) or re.fullmatch(r"[0-9a-f]{64}:[0-9a-f]{64}", value) is None:
            raise ValueError("invalid credential binding")
        name_hash, content_hash = value.split(":")
        bindings.append({"name_sha256": name_hash, "content_sha256": content_hash})
    if bindings and (executor not in {"codex", "meta", "gemini", "claude", "antigravity"}
                     or len({binding["name_sha256"] for binding in bindings}) != len(bindings)):
        raise ValueError("invalid native credential selection")
    return bindings


def validate_profile(profile):
    if (not isinstance(profile, dict) or set(profile) - {"auth_files"} != {"client_model", "upstream_model", "executor", "contract"}
            or any(not isinstance(value, str) or not value for key, value in profile.items() if key != "auth_files")):
        raise ValueError("invalid pinned profile")
    bindings = profile.get("auth_files", [])
    if (not isinstance(bindings, list) or "auth_files" in profile and not bindings
            or any(not isinstance(value, dict) or set(value) != {"name_sha256", "content_sha256"}
                   or any(not isinstance(digest, str) for digest in value.values()) for value in bindings)):
        raise ValueError("invalid explicit credential bindings")
    parse_credential_bindings([binding["name_sha256"] + ":" + binding["content_sha256"] for binding in bindings], profile["executor"])
    if profile["executor"] == "antigravity" and (not bindings or profile["contract"] != "antigravity-generate-v1"):
        raise ValueError("Antigravity requires a bound credential and wrapped wire contract")


def validate_ready(ready, pid, port, profile, config_hash):
    keys = {"version", "scope", "pid", "base_url", "ready_nonce", "profile", "source_config_sha256",
            "source_policy_sha256", "isolation_version", "selected_auth_ids_sha256", "selected_auth_count", "credential_refresh_disabled", "selected_auth_ids"}
    if not isinstance(ready, dict) or set(ready) != keys:
        raise ValueError("invalid live candidate readiness shape")
    address = urlsplit(ready["base_url"])
    ids = ready["selected_auth_ids"]
    if (not isinstance(ids, list) or not ids or any(not isinstance(item, str) or not re.fullmatch(r"[0-9a-f]{64}", item) for item in ids)
            or ids != sorted(set(ids)) or len(ids) != ready["selected_auth_count"]
            or hashlib.sha256(json.dumps(ids, separators=(",", ":")).encode()).hexdigest() != ready["selected_auth_ids_sha256"]):
        raise ValueError("selected credential scope fingerprint invalid")
    if (type(ready["version"]) is not int or ready["version"] != 1 or ready["scope"] != "isolated-live-candidate"
            or type(ready["pid"]) is not int or ready["pid"] != pid or ready["profile"] != profile
            or ready["source_config_sha256"] != config_hash
            or type(ready["isolation_version"]) is not int or ready["isolation_version"] != 6 or ready["credential_refresh_disabled"] is not True
            or type(ready["selected_auth_count"]) is not int or ready["selected_auth_count"] < 1
            or not isinstance(ready["ready_nonce"], str) or not 1 <= len(ready["ready_nonce"]) <= 128
            or address.scheme != "http" or address.hostname != "127.0.0.1" or address.port != port
            or address.username or address.password or address.query or address.fragment or address.path != "/v1"
            or any(not isinstance(ready[key], str) or not re.fullmatch(r"[0-9a-f]{64}", ready[key])
                   for key in ("source_config_sha256", "source_policy_sha256", "selected_auth_ids_sha256"))):
        raise ValueError("live candidate differs from actual child/config/profile")


def _ready_http(ready):
    address = urlsplit(ready["base_url"])
    # This is a bounded local startup probe, not an upstream inference timeout.
    connection = http.client.HTTPConnection(address.hostname, address.port, timeout=0.5)
    try:
        connection.request("GET", "/acceptance/ready", headers={"Authorization": "Bearer local-acceptance-placeholder"})
        response = connection.getresponse()
        body = response.read(8193)
        if response.status != 200 or len(body) > 8192:
            return False
        return json.loads(body) == {"pid": ready["pid"], "ready_nonce": ready["ready_nonce"]}
    except (OSError, ValueError, http.client.HTTPException):
        return False
    finally:
        connection.close()


@contextmanager
def candidate_process(binary, config, root, profile, *, max_attempts=16):
    binary, config, root = Path(binary).resolve(), Path(config).resolve(), Path(root).resolve()
    if not binary.is_file() or not config.is_file() or not root.is_dir() or type(max_attempts) is not int or not 1 <= max_attempts <= 32:
        raise ValueError("invalid candidate inputs or attempt budget")
    validate_profile(profile)
    bindings = profile.get("auth_files", [])
    config_hash = hashlib.sha256(config.read_bytes()).hexdigest()
    write_private(root / "gateway-binary-sha256.txt", hashlib.sha256(binary.read_bytes()).hexdigest() + "\n")
    # The SDK binds its own listener. A reservation is released before launch;
    # collision is fail-closed, and nonce probing proves endpoint ownership.
    with socket.socket() as reservation:
        reservation.bind(("127.0.0.1", 0))
        port = reservation.getsockname()[1]
    command = [str(binary), "--config", str(config), "--state-dir", str(root / "gateway-state"), "--port", str(port),
               "--client-model", profile["client_model"], "--upstream-model", profile["upstream_model"],
               "--executor", profile["executor"], "--contract", profile["contract"], "--max-attempts", str(max_attempts)]
    for binding in bindings:
        command += ["--auth-file-binding", binding["name_sha256"]+":"+binding["content_sha256"]]
    env = {key: os.environ[key] for key in ENV_KEYS if key in os.environ}
    # Preserve the original owner home only for resolving the explicitly
    # requested operator auth directory. Native clients use separate homes.
    if "HOME" in os.environ:
        env["HOME"] = os.environ["HOME"]
    stdout_path, stderr_path = root / "gateway.stdout.log", root / "gateway.stderr.log"
    with stdout_path.open("xb") as stdout_log, stderr_path.open("xb") as stderr:
        os.chmod(stdout_path, 0o600)
        os.chmod(stderr_path, 0o600)
        process = subprocess.Popen(command, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                   stderr=stderr, start_new_session=True)
        ready, buffer, observed = None, b"", 0
        try:
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline and process.poll() is None:
                if ready is not None and _ready_http(ready):
                    break
                if select.select([process.stdout], [], [], 0.05)[0]:
                    chunk = os.read(process.stdout.fileno(), 8192)
                    if not chunk:
                        break
                    observed += len(chunk)
                    if observed > 65536:
                        raise ValueError("candidate startup output budget exhausted")
                    stdout_log.write(chunk)
                    stdout_log.flush()
                    buffer += chunk
                    while b"\n" in buffer:
                        line, buffer = buffer.split(b"\n", 1)
                        if line.startswith(b"{"):
                            if ready is not None:
                                raise ValueError("duplicated candidate readiness")
                            ready = json.loads(line)
                            validate_ready(ready, process.pid, port, profile, config_hash)
            else:
                raise ValueError("candidate startup budget exhausted or child exited")
            if ready is None or process.poll() is not None or not _ready_http(ready):
                raise ValueError("candidate endpoint ownership not verified")
            write_private(root / "gateway.ready.json", json.dumps(ready, indent=2))
            yield ready, root / "gateway-state" / "capture"
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=35)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            process.stdout.close()
            unchanged = hashlib.sha256(config.read_bytes()).hexdigest() == config_hash
            write_private(root / "gateway.exit.json", json.dumps({"exit_code": process.returncode, "source_config_unchanged": unchanged}))
            if process.returncode != 0 or not unchanged:
                diagnostics = (read_candidate_failure(stderr_path) if process.returncode != 0 else None) or []
                if not unchanged:
                    diagnostics = diagnostics[:7] + [{"stage": "configuration", "reason": "configuration_changed"}]
                if not diagnostics:
                    diagnostics = [{"stage": "unknown", "reason": "candidate_failed"}]
                write_private(root / "gateway.failure.json", json.dumps({"version": 1,
                    "scope": "isolated-candidate-error", "failures": diagnostics}, indent=2))
                raise CandidateFailure(diagnostics)
            from http_attempts import read_auxiliary_work_diagnostics
            if read_auxiliary_work_diagnostics(root / "gateway-state" / "capture") is None:
                raise ValueError("candidate auxiliary work settlement is unverified")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gateway-binary", type=Path, required=True)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    for field in ("client-model", "upstream-model", "executor", "contract"):
        parser.add_argument("--" + field, required=True)
    parser.add_argument("--auth-file-binding", action="append", default=[], help="explicit native filename SHA-256:content SHA-256")
    args = parser.parse_args()
    profile = {key: getattr(args, key) for key in ("client_model", "upstream_model", "executor", "contract")}
    try:
        bindings = parse_credential_bindings(args.auth_file_binding, args.executor)
        if bindings:
            profile["auth_files"] = bindings
        validate_profile(profile)
    except ValueError:
        parser.error("invalid pinned profile or native credential selection")
    root = args.output_dir.resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    with candidate_process(args.gateway_binary, args.config, root, profile) as (ready, capture):
        write_private(root / "bootstrap.summary.json", json.dumps({"gate_status": "unverified", "startup_verified": True,
                      "provider_inference_performed": False, "ready": ready, "capture_dir": str(capture)}, indent=2))
        print("Isolated candidate startup verified; no Provider inference or certification performed.")


if __name__ == "__main__":
    main()
