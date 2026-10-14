"""Version-pinned, isolated native clients for tool-loading acceptance.

Requested configuration is not proof of effective mode. Inspect the outbound
request and MCP events before accepting an eager/lazy comparison.
"""

from __future__ import annotations

from copy import deepcopy
from dataclasses import dataclass
import json
import os
from pathlib import Path
import selectors
import signal
import subprocess
import time
from urllib.parse import urlsplit

from jsonschema import Draft202012Validator
from jsonschema.exceptions import SchemaError

from catalog_fixture import build_catalog, digest
from codex_code_mode import requested_codex_mode, metadata_admits_mode

VERSIONS = {"codex": "0.160.0"}
CLAUDE_VERSION = "2.1.289"
# Official installed binaries only; no client source modifications.
ADAPTER_VERSIONS = {**VERSIONS, "claude": CLAUDE_VERSION}
ENV_KEYS = ("PATH", "TMPDIR", "LANG", "LC_ALL", "SYSTEMROOT", "WINDIR")


def validate_session_title(value):
    if (not isinstance(value, str) or not value.strip() or len(value) > 128
            or any(ord(character) < 32 or ord(character) == 127 for character in value)):
        raise ValueError("invalid explicit session title")
    return value


def validate_output_schema(value):
    try:
        if (not isinstance(value, dict) or value.get("type") != "object"
                or value.get("additionalProperties") is not False or not isinstance(value.get("properties"), dict)
                or not isinstance(value.get("required"), list)
                or any(not isinstance(key, str) for key in value["required"])
                or len(set(value["required"])) != len(value["required"])
                or set(value["required"]) != set(value["properties"])):
            raise ValueError("a strict object output schema is required")
        encoded = json.dumps(value, allow_nan=False).encode()
        if len(encoded) > 16384:
            raise ValueError("output schema exceeds the local budget")
        stack = [value]
        while stack:
            node = stack.pop()
            if isinstance(node, dict):
                if "$ref" in node or "$dynamicRef" in node:
                    raise ValueError("output schemas must be self-contained")
                stack.extend(node.values())
            elif isinstance(node, list):
                stack.extend(node)
        Draft202012Validator.check_schema(value)
    except (SchemaError, TypeError, RecursionError) as error:
        raise ValueError("invalid output schema") from error
    return value


def synthetic_codex_model(model: str) -> dict:
    """Minimal metadata for local protocol probes, never a real-model claim."""
    return {"slug": model, "display_name": model, "description": "Local acceptance fixture",
            "base_instructions": "You are a local protocol acceptance agent. Use declared tools and preserve tool results.",
            "supported_reasoning_levels": [], "shell_type": "unified_exec",
            "visibility": "list", "supported_in_api": True, "priority": 1,
            "support_verbosity": False, "truncation_policy": {"mode": "tokens", "limit": 10000},
            "experimental_supported_tools": [], "context_window": 128000,
            "tool_mode": "direct", "supports_search_tool": False}


def write_private(path: Path, content: str) -> None:
    with path.open("x", encoding="utf-8") as handle:
        os.chmod(path, 0o600)
        handle.write(content)


@dataclass
class PreparedClient:
    client_id: str
    client_version: str
    expected_mode: str
    command: list[str]
    env: dict[str, str]
    run_dir: Path
    config_path: Path
    catalog_path: Path
    events_path: Path
    permission_hash: str
    catalog_hash: str
    output_schema: dict | None = None
    session_title: str | None = None

    def collect_boundary_events(self) -> list[dict]:
        if not self.events_path.exists():
            return []
        raw = self.events_path.read_text(encoding="utf-8")
        if raw and not raw.endswith("\n"):
            raise ValueError("incomplete MCP event journal")
        events = [json.loads(line) for line in raw.splitlines()]
        for index, event in enumerate(events):
            if not isinstance(event, dict) or type(event.get("sequence")) is not int or event["sequence"] != index:
                raise ValueError("invalid MCP event sequence")
        return events

    def run_task(self, case_spec: dict, *, cancel_event=None) -> dict:
        """Run only the isolated process group; process exit is not task success."""
        seconds, limit = case_spec["max_seconds"], case_spec["max_output_bytes"]
        if (type(seconds) not in {int, float} or not 0 < seconds < float("inf")
                or type(limit) is not int or limit < 1 or not isinstance(case_spec["prompt"], str)):
            raise ValueError("invalid process limits or prompt")
        if os.name != "posix":
            raise ValueError("process-group isolation currently requires POSIX")
        output_paths = [self.run_dir / "stdout.jsonl", self.run_dir / "stderr.log"]
        handles = []
        process = None
        started = time.monotonic()
        termination, stored, observed = "exited", 0, 0
        selector = selectors.DefaultSelector()
        interrupt_sent = forced_cleanup = False

        def pump(wait_seconds):
            nonlocal stored, observed
            exceeded = False
            for key, _ in selector.select(wait_seconds):
                try:
                    chunk = os.read(key.fileobj.fileno(), 65536)
                except BlockingIOError:
                    continue
                if not chunk:
                    selector.unregister(key.fileobj)
                    continue
                observed += len(chunk)
                accepted = chunk[:max(0, limit - stored)]
                key.data.write(accepted)
                stored += len(accepted)
                exceeded = exceeded or observed > limit
            return exceeded
        try:
            for path in output_paths:
                handle = path.open("xb")
                os.chmod(path, 0o600)
                handles.append(handle)
            process = subprocess.Popen(self.command + [case_spec["prompt"]], cwd=self.run_dir / "workspace" if (self.run_dir / "workspace").exists() else self.run_dir,
                                       env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                       stdin=subprocess.DEVNULL, start_new_session=True)
            for stream, handle in zip((process.stdout, process.stderr), handles):
                os.set_blocking(stream.fileno(), False)
                selector.register(stream, selectors.EVENT_READ, handle)
            while selector.get_map() or process.poll() is None:
                if cancel_event is not None and cancel_event.is_set():
                    termination = "cancelled"
                    break
                if time.monotonic() - started >= seconds:
                    termination = "process_budget"
                    break
                if pump(min(0.05, seconds)):
                    termination = "output_budget"
                if termination != "exited":
                    break
        finally:
            if process is not None:
                # Interrupt the CLI first so it can cancel pending MCP requests
                # and emit release events. Keep draining within the same cap.
                if termination != "exited" and process.poll() is None:
                    try:
                        os.kill(process.pid, signal.SIGINT)
                        interrupt_sent = True
                    except ProcessLookupError:
                        pass
                    graceful_deadline = time.monotonic() + 2
                    while (selector.get_map() or process.poll() is None) and time.monotonic() < graceful_deadline:
                        pump(0.05)
                    forced_cleanup = process.poll() is None
                # Kill descendants even if the main CLI already exited: an MCP
                # child may still own a pipe or execution sandbox.
                try:
                    os.killpg(process.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
                try:
                    process.wait(timeout=1)
                except subprocess.TimeoutExpired:
                    pass
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                process.wait()
                drain_deadline = time.monotonic() + 0.5
                while selector.get_map() and time.monotonic() < drain_deadline:
                    pump(0.01)
                process.stdout.close()
                process.stderr.close()
            selector.close()
            for handle in handles:
                handle.close()
        return {"exit_code": process.returncode, "termination": termination,
                "elapsed_seconds": time.monotonic() - started, "stored_output_bytes": stored,
                "observed_output_bytes": observed, "stdout_path": str(output_paths[0]),
                "stderr_path": str(output_paths[1]), "graceful_interrupt_sent": interrupt_sent,
                "forced_process_cleanup": forced_cleanup, "output_truncated": observed > limit,
                "task_success_verified": False}


def prepare_isolated_client(client_id: str, mode: str, catalog: dict, permissions: dict) -> PreparedClient:
    if client_id not in ADAPTER_VERSIONS or mode not in {"eager", "lazy"}:
        raise ValueError("unsupported client or mode")
    if catalog.get("client_version") != ADAPTER_VERSIONS[client_id]:
        raise ValueError("unsupported client version")
    allowed = {"run_dir", "client_version", "executable", "model", "base_url", "fixture_python",
               "model_metadata", "nonce", "max_dispatches", "max_result_bytes"}
    if set(catalog) - {"output_schema", "coding_task", "codex_tool_mode"} != allowed:
        raise ValueError("missing or unknown adapter configuration keys")
    if "codex_tool_mode" in catalog and not isinstance(catalog["codex_tool_mode"], str):
        raise ValueError("explicit Codex tool profile must be a string")
    codex_mode = requested_codex_mode(client_id, catalog.get("codex_tool_mode"))
    coding_task = catalog.get("coding_task")
    if "coding_task" in catalog:
        from coding_tasks import TASKS
        if type(coding_task) is not str or coding_task not in TASKS or catalog["max_result_bytes"] < 32768:
            raise ValueError("invalid explicit coding profile")
    if "output_schema" in catalog:
        validate_output_schema(catalog["output_schema"])
    url = urlsplit(catalog["base_url"])
    if (url.scheme != "http" or url.hostname not in {"127.0.0.1", "localhost", "::1"}
            or url.username or url.password or url.query or url.fragment or url.path.rstrip("/") != "/v1"):
        raise ValueError("an isolated loopback /v1 endpoint is required")
    fixture_catalog = build_catalog(coding_task)
    names = {tool["name"] for tool in fixture_catalog}
    if set(permissions) != {"client_denied", "service_denied"}:
        raise ValueError("unknown permission keys")
    for values in permissions.values():
        if not isinstance(values, list) or any(not isinstance(name, str) or name not in names for name in values):
            raise ValueError("unknown denied tool")
    for key, minimum in (("max_dispatches", 1), ("max_result_bytes", 256)):
        if type(catalog[key]) is not int or catalog[key] < minimum:
            raise ValueError("invalid fixture budget")
    if not isinstance(catalog["nonce"], str) or not catalog["nonce"]:
        raise ValueError("a fresh nonce is required")
    metadata = deepcopy(catalog["model_metadata"])
    if client_id == "codex" and (metadata.get("slug") != catalog["model"] or not metadata_admits_mode(metadata, codex_mode)):
        raise ValueError("selected model metadata must match the explicit tool profile")
    env = {key: os.environ[key] for key in ENV_KEYS if key in os.environ}
    checked = subprocess.run([str(catalog["executable"]), "--version"], env=env,
                             capture_output=True, text=True, check=True)
    expected = {"codex": "codex-cli " + VERSIONS["codex"],
                "claude": CLAUDE_VERSION + " (Claude Code)"}[client_id]
    if checked.stdout.strip() != expected:
        raise ValueError("installed client version differs from the declared scope")
    root = Path(catalog["run_dir"]).resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    env["PYTHONPYCACHEPREFIX"] = str(root / "pycache")
    workspace = root / "workspace"
    workspace.mkdir(mode=0o700)
    # A private repository bounds instruction discovery to the synthetic task.
    subprocess.run(["git", "-c", "init.defaultBranch=main", "init", "--quiet", "--template=", str(workspace)],
                   env={**env, "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull},
                   capture_output=True, check=True)
    if coding_task is not None:
        from coding_tasks import CodingProject
        CodingProject.create(workspace, coding_task)
    events = root / "mcp-events.jsonl"
    policy = root / "permissions.json"
    normalized_permissions = {key: sorted(set(value)) for key, value in permissions.items()}
    write_private(policy, json.dumps({"generation": 1, "denied": normalized_permissions["service_denied"]}))
    catalog_path = root / "catalog.json"
    metadata["supports_search_tool"] = mode == "lazy"
    if codex_mode == "direct":
        metadata["tool_mode"] = "direct"
    write_private(catalog_path, json.dumps({"models": [metadata]}, ensure_ascii=False))
    fixture_command = [str(catalog["fixture_python"]), str(Path(__file__).with_name("catalog_fixture.py").resolve()),
                       "--nonce", catalog["nonce"], "--events", str(events), "--permissions", str(policy),
                       "--max-dispatches", str(catalog["max_dispatches"]), "--max-result-bytes", str(catalog["max_result_bytes"])]
    if coding_task is not None:
        # Built-in shell remains read-only/denied. Only the explicit MCP profile
        # grants a scoped write to the synthetic task's single source file.
        fixture_command += ["--coding-task", coding_task, "--coding-workspace", str(workspace)]
    fixture_env = {}
    # The endpoint is an explicitly supplied local fixture/gateway. Credentials
    # are not inherited from the operator's shell or stored in configuration.
    env["TOOL_LOADING_GATEWAY_KEY"] = "local-acceptance-placeholder"
    command = [str(catalog["executable"])]
    if client_id == "codex":
        home = root / "codex-home"
        home.mkdir(mode=0o700)
        env["CODEX_HOME"] = str(home)
        values = {
            "model_provider": "acceptance", "model_providers.acceptance.name": "Local acceptance gateway",
            "model_providers.acceptance.base_url": catalog["base_url"], "model_providers.acceptance.wire_api": "responses",
            "model_providers.acceptance.env_key": "TOOL_LOADING_GATEWAY_KEY",
            "model_providers.acceptance.requires_openai_auth": False,
            "model_providers.acceptance.supports_websockets": False,
            "model_providers.acceptance.request_max_retries": 0, "model_providers.acceptance.stream_max_retries": 0,
            "model_catalog_json": str(catalog_path), "approval_policy": "never", "project_doc_max_bytes": 0,
            "features.apps": False, "features.plugins": False, "features.hooks": False,
            "features.multi_agent": False, "features.enable_request_compression": False,
            "features.code_mode": codex_mode == "code_mode_only", "features.code_mode_only": codex_mode == "code_mode_only",
            "features.code_mode_host": codex_mode == "code_mode_only",
            "mcp_servers.fixture.command": fixture_command[0], "mcp_servers.fixture.args": fixture_command[1:],
            "mcp_servers.fixture.default_tools_approval_mode": "approve",
            "mcp_servers.fixture.tool_input_schema_max_bytes": 16000,
            "mcp_servers.fixture.disabled_tools": normalized_permissions["client_denied"],
        }
        if fixture_env:
            values["mcp_servers.fixture.env"] = fixture_env
        config_path = root / "overrides.toml"
        overrides = [f"{key}={json.dumps(value, ensure_ascii=False)}" for key, value in values.items()]
        write_private(config_path, "\n".join(overrides) + "\n")
        command += ["exec", "--strict-config", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check",
                    "--ephemeral", "--json", "--color", "never", "-s", "read-only", "-C", str(workspace), "-m", catalog["model"]]
        if "output_schema" in catalog:
            schema_path = root / "output-schema.json"
            write_private(schema_path, json.dumps(catalog["output_schema"], allow_nan=False))
            command += ["--output-schema", str(schema_path)]
        for override in overrides:
            command += ["-c", override]
        expected_mode = "code-mode-only-"+mode if codex_mode == "code_mode_only" else "tool-search" if mode == "lazy" else "direct"
    else:
        home = root / "claude-home"
        home.mkdir(mode=0o700)
        config_dir = root / "claude-config"
        config_dir.mkdir(mode=0o700)
        env.update({"HOME": str(home), "CLAUDE_CONFIG_DIR": str(config_dir),
                    "ANTHROPIC_BASE_URL": catalog["base_url"].rstrip("/")[:-3],
                    "ANTHROPIC_API_KEY": "local-acceptance-placeholder",
                    "ENABLE_TOOL_SEARCH": "true" if mode == "lazy" else "false",
                    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "DISABLE_UPDATES": "1",
                    "CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1", "ENABLE_CLAUDEAI_MCP_SERVERS": "false"})
        config_path = root / "mcp.json"
        fixture_settings = {"command": fixture_command[0], "args": fixture_command[1:], "alwaysLoad": False}
        if fixture_env:
            fixture_settings["env"] = fixture_env
        write_private(config_path, json.dumps({"mcpServers": {"fixture": fixture_settings}}))
        settings_path = root / "settings.json"
        write_private(settings_path, json.dumps({"disableAllHooks": True}))
        allowed_tools = sorted("mcp__fixture__" + name for name in names
                               if name not in normalized_permissions["client_denied"])
        denied_tools = sorted("mcp__fixture__" + name for name in normalized_permissions["client_denied"])
        # Bare/simple mode suppresses ToolSearch even with explicit opt-in on
        # 2.1.289. Isolate settings, auth, MCP and built-ins independently.
        command += ["--print", "--verbose", "--output-format", "stream-json", "--setting-sources", "",
                    "--settings", str(settings_path), "--strict-mcp-config", "--mcp-config", str(config_path),
                    "--tools", "ToolSearch" if mode == "lazy" else "", "--disable-slash-commands",
                    "--no-session-persistence", "--permission-mode", "dontAsk", "--permission-prompts", "none",
                    "--allowedTools", ",".join(allowed_tools)]
        if denied_tools:
            command += ["--disallowedTools", ",".join(denied_tools)]
        # Terminate variadic tool options before appending a positional prompt.
        command += ["--model", catalog["model"], "--prompt-suggestions", "false"]
        if "output_schema" in catalog:
            command += ["--json-schema", json.dumps(catalog["output_schema"], allow_nan=False)]
        expected_mode = "tool-search" if mode == "lazy" else "direct"
    permission_material = profile_permissions(normalized_permissions, structured_output="output_schema" in catalog,
                                             coding_task=coding_task)
    return PreparedClient(client_id, ADAPTER_VERSIONS[client_id], expected_mode, command, env, root,
                          config_path, catalog_path, events, digest(permission_material), digest(fixture_catalog),
                          deepcopy(catalog.get("output_schema")), None)


def profile_permissions(permissions, *, structured_output=False, coding_task=None):
    material = {"denials": permissions, "native_structured_output": True} if structured_output else permissions
    if coding_task is not None:
        material = {"base": material, "coding_task": coding_task, "writable_files": ["module.py"],
                    "execution_profile": "bounded-pure-python-v1"}
    return material
