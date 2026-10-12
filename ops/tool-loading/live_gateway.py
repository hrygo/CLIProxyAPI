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
import subprocess
import time
from urllib.parse import urlsplit

from client_adapters import ENV_KEYS, write_private


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
            or type(ready["isolation_version"]) is not int or ready["isolation_version"] != 3 or ready["credential_refresh_disabled"] is not True
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
    if set(profile) != {"client_model", "upstream_model", "executor", "contract"} or any(not isinstance(value, str) or not value for value in profile.values()):
        raise ValueError("invalid pinned profile")
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
                raise ValueError("candidate cleanup or source snapshot verification failed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gateway-binary", type=Path, required=True)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    for field in ("client-model", "upstream-model", "executor", "contract"):
        parser.add_argument("--" + field, required=True)
    args = parser.parse_args()
    root = args.output_dir.resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    profile = {key: getattr(args, key) for key in ("client_model", "upstream_model", "executor", "contract")}
    with candidate_process(args.gateway_binary, args.config, root, profile) as (ready, capture):
        write_private(root / "bootstrap.summary.json", json.dumps({"gate_status": "unverified", "startup_verified": True,
                      "provider_inference_performed": False, "ready": ready, "capture_dir": str(capture)}, indent=2))
        print("Isolated candidate startup verified; no Provider inference or certification performed.")


if __name__ == "__main__":
    main()
