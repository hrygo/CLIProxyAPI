"""Code Mode must be explicit and cannot reuse direct-profile evidence."""
from copy import deepcopy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))


class CodexCodeModeProfileTests(unittest.TestCase):
    def test_code_mode_entry_retains_actual_declaration_namespace(self):
        from code_mode_wire import EXEC_ALIAS, normalize_code_mode_wire
        native={"type":"custom","name":"exec","description":"Run JavaScript code; ALL_TOOLS",
                "format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"}}
        bridge={"type":"function","name":EXEC_ALIAS,"description":"Run JavaScript code; ALL_TOOLS",
                "parameters":{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":False}}
        def body(tool, namespace):
            return {"tools":[tool] if namespace is None else [{"type":"namespace","name":namespace,"tools":[tool]}],"input":[]}
        for namespace in (None,"functions"):
            self.assertTrue(normalize_code_mode_wire([body(native,namespace)],"openai-responses-v1")["integrity"])
        self.assertTrue(normalize_code_mode_wire([body(bridge,None)],"openai-responses-v1")["integrity"])
        for tool, namespace in ((native,"other"),(bridge,"functions"),(bridge,"other"),({**bridge,"namespace":"other"},None)):
            with self.subTest(namespace=namespace,name=tool["name"]):
                self.assertFalse(normalize_code_mode_wire([body(tool,namespace)],"openai-responses-v1")["integrity"])
        self.assertFalse(normalize_code_mode_wire([body(bridge,None),body({**bridge,"parameters":{}},None)],"openai-responses-v1")["integrity"])

    def test_bridge_observation_keeps_grammar_loss_explicit(self):
        from codex_code_mode import observe_code_mode
        from catalog_fixture import build_catalog
        description="Run JavaScript code; ALL_TOOLS\n"+"\n".join("declare const tools: { mcp__fixture__"+t["name"]+"(args: {project: string;}): Promise<unknown>; };" for t in build_catalog() if t["name"]!="fixture_mutation")
        tool={"type":"function","name":"ccb_af56ab8ec19e8955daac9192a46dce815acb276fab7bb6ac","description":description+'\nsyntax: "lark"\ntype: "grammar"',
              "parameters":{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":False}}
        result=observe_code_mode([tool],"eager",service_denied=["fixture_mutation"],wire_contract="openai-responses-v1")
        self.assertTrue(result["effective"])
        self.assertFalse(result["exec_grammar_observed"])
        self.assertTrue(result["exec_bridge_observed"])
        self.assertEqual(result["grammar_fidelity"],"described")
        self.assertFalse(observe_code_mode([tool],"eager",service_denied=["fixture_mutation"])["effective"])
        for invalid in ({**tool,"name":"ccb_other"},{**tool,"parameters":{"type":"object"}}):
            self.assertFalse(observe_code_mode([invalid],"eager",wire_contract="openai-responses-v1")["effective"])

    def test_bridge_history_binds_declared_identity_and_exact_input_wrapper(self):
        from code_mode_wire import normalize_code_mode_wire
        from codex_code_mode import observe_code_discovery
        alias="ccb_af56ab8ec19e8955daac9192a46dce815acb276fab7bb6ac"
        tool={"type":"function","name":alias,"description":"Run JavaScript code; ALL_TOOLS; syntax: lark; type: grammar",
              "parameters":{"type":"object","properties":{"input":{"type":"string"}},"required":["input"],"additionalProperties":False}}
        initial={"tools":[tool],"input":[]}
        call={"type":"function_call","name":alias,"call_id":"search","arguments":json.dumps({"input":"text(ALL_TOOLS);"})}
        schema="declare const tools: { mcp__fixture__ledger_budget_lookup(args: {project: string;}): Promise<unknown>; };"
        output={"type":"function_call_output","call_id":"search","output":schema}
        bodies=[initial,{"input":[call,output]}]
        frozen=deepcopy(bodies)
        view=normalize_code_mode_wire(bodies,"openai-responses-v1")
        self.assertTrue(view["integrity"])
        self.assertEqual(bodies,frozen)
        self.assertEqual(observe_code_discovery(view["bodies"])["loaded_fixture_schema_tools"],["ledger_budget_lookup"])
        for arguments in ('{"input":"text(ALL_TOOLS);","input":"fake"}', '{"input":null}', '{"input":"text(ALL_TOOLS);","other":1}'):
            bad={"input":[{**call,"arguments":arguments},output]}
            self.assertFalse(normalize_code_mode_wire([initial,bad],"openai-responses-v1")["integrity"])
        unknown=normalize_code_mode_wire([initial,{"input":[{**call,"name":"other"},output]}],"openai-responses-v1")
        self.assertEqual(observe_code_discovery(unknown["bodies"])["loaded_fixture_schema_tools"],[])
        self.assertFalse(normalize_code_mode_wire([initial,{"input":[{**call,"namespace":"other"},output]}],"openai-responses-v1")["integrity"])

    def test_chat_exec_view_requires_declared_string_bridge_and_matching_tool_result(self):
        from code_mode_wire import normalize_code_mode_wire
        from codex_code_mode import observe_code_discovery
        function={"name":"exec","description":"Run JavaScript code; ALL_TOOLS",
                  "parameters":{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}}
        initial={"tools":[{"type":"function","function":function}],"messages":[]}
        history={"messages":[{"role":"assistant","tool_calls":[{"id":"search","type":"function","function":{"name":"exec","arguments":json.dumps({"input":"text(ALL_TOOLS);"})}}]},
                             {"role":"tool","tool_call_id":"search","content":"[]"}]}
        view=normalize_code_mode_wire([initial,history],"openai-chat-v1")
        self.assertTrue(view["integrity"])
        self.assertTrue(observe_code_discovery(view["bodies"])["discovery_result_observed"])
        self.assertNotIn("messages",view["bodies"][1])
        invalid=deepcopy(history); invalid["messages"][1]["tool_call_id"]="different"
        self.assertFalse(observe_code_discovery(normalize_code_mode_wire([initial,invalid],"openai-chat-v1")["bodies"])["discovery_result_observed"])

    def test_scripted_code_mode_preserves_raw_input_and_receipt_identity(self):
        from scripted_clients import ScriptedModel, response_events, tool_receipt
        model = ScriptedModel("codex", "eager", "allow", codex_tool_mode="code_mode_only")
        item = model.next({})
        self.assertEqual(item["type"], "custom_tool_call")
        self.assertEqual(item["namespace"], "functions")
        self.assertEqual(item["name"], "exec")
        self.assertIn("await tools.mcp__fixture__ledger_budget_lookup", item["input"])
        events = response_events(item, "resp_fixture")
        deltas = [x for x in events if x["type"] == "response.custom_tool_call_input.delta"]
        self.assertEqual(deltas[0]["delta"], item["input"])
        self.assertEqual(tool_receipt("codex", {"input":[{"type":"custom_tool_call_output","call_id":"invoke",
                         "output":'{"remaining_budget":424242,"verification_code":"fresh"}'}]})["verification_code"], "fresh")

    def test_actual_catalog_requires_explicit_matching_mode(self):
        from live_clients import CatalogFailure, select_model_metadata
        model = {"slug": "fixture", "context_window": 1000,
                 "tool_mode": "code_mode_only", "supports_search_tool": True}
        with self.assertRaises(CatalogFailure):
            select_model_metadata({"models": [model]}, "fixture", "codex", "lazy")
        self.assertEqual(select_model_metadata({"models": [model]}, "fixture", "codex", "lazy",
                         codex_tool_mode="code_mode_only"), model)
        with self.assertRaises(CatalogFailure):
            select_model_metadata({"models": [{**model, "tool_mode": "direct"}]}, "fixture", "codex", "lazy",
                                  codex_tool_mode="code_mode_only")

    def test_adapter_preserves_model_mode_and_bottom_level_denials(self):
        from client_adapters import prepare_isolated_client, synthetic_codex_model
        for mode in ("eager", "lazy"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                metadata = {**synthetic_codex_model("fixture"), "tool_mode": "code_mode_only", "supports_search_tool": True}
                catalog = {"run_dir": root/"run", "client_version": "0.160.0", "executable": sys.executable,
                           "model": "fixture", "base_url": "http://127.0.0.1:12345/v1", "fixture_python": sys.executable,
                           "model_metadata": metadata, "nonce": "fresh", "max_dispatches": 8, "max_result_bytes": 65536,
                           "codex_tool_mode": "code_mode_only"}
                with patch("client_adapters.subprocess.run", return_value=subprocess.CompletedProcess([],0,"codex-cli 0.160.0\n","")):
                    client = prepare_isolated_client("codex", mode, catalog,
                             {"client_denied": ["ledger_budget_lookup"], "service_denied": ["fixture_mutation"]})
                frozen = json.loads(client.catalog_path.read_text())["models"][0]
                self.assertEqual(frozen["tool_mode"], "code_mode_only")
                self.assertEqual(frozen["supports_search_tool"], mode == "lazy")
                overrides = client.config_path.read_text()
                for flag in ("features.code_mode", "features.code_mode_only", "features.code_mode_host"):
                    self.assertIn(flag+"=true", overrides)
                self.assertIn('mcp_servers.fixture.disabled_tools=["ledger_budget_lookup"]', overrides)
                self.assertEqual(json.loads((client.run_dir/"permissions.json").read_text())["denied"], ["fixture_mutation"])
                self.assertEqual(client.expected_mode, "code-mode-only-"+mode)
                self.assertEqual(metadata["supports_search_tool"], True)

    def test_profile_identity_cannot_collide_with_direct_profile(self):
        from live_clients import make_case_spec
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config, executable = root/"config", root/"client"
            config.write_text("synthetic-config")
            executable.write_text("synthetic-client")
            profile = {"client_model":"fixture", "upstream_model":"fixture", "executor":"codex", "contract":"openai-responses-v1"}
            with patch("live_clients.shutil.which", return_value=str(executable)):
                direct = make_case_spec(config, profile, "codex", "lazy", "a"*40, "ep_"+"b"*32,90)
                code = make_case_spec(config, profile, "codex", "lazy", "a"*40, "ep_"+"b"*32,90, codex_tool_mode="code_mode_only")
                eager = make_case_spec(config, profile, "codex", "eager", "a"*40, "ep_"+"b"*32,90, codex_tool_mode="code_mode_only")
            self.assertNotEqual(direct.case_id, code.case_id)
            self.assertNotEqual(direct.scope["fixture_hash"], code.scope["fixture_hash"])
            self.assertEqual(code.pair_id, eager.pair_id)
            self.assertEqual(code.scope["client_mode"], "code-mode-only-lazy")

    def test_new_matrix_schema_keeps_old_declarations_closed(self):
        from live_matrix import validate_declaration
        value = {"schema_version":2, "task_seconds":90, "max_requests":16,"max_dispatches":16,"schedule_seed":"new-profile",
                 "units":[{"profile":{"client_model":"fixture","upstream_model":"fixture","executor":"codex","contract":"openai-responses-v1"},
                           "endpoint_sha256":"b"*64,"client":"codex","structured_output":True,"opencode_title":None,
                           "scenarios":["lookup"],"codex_tool_mode":"code_mode_only"}]}
        validate_declaration(value)
        for version, client, mode in ((1,"codex","code_mode_only"),(2,"opencode","code_mode_only"),
                                      (2,"codex",True),(2,"codex","code_mode"),(2,"codex",None)):
            invalid=deepcopy(value); invalid["schema_version"]=version
            invalid["units"][0].update(client=client,codex_tool_mode=mode)
            with self.subTest(version=version,client=client,mode=mode), self.assertRaises(ValueError):
                validate_declaration(invalid)

    def test_effective_mode_requires_real_nested_signatures(self):
        from codex_code_mode import observe_code_mode
        from catalog_fixture import build_catalog
        def signature(name):
            full="mcp__fixture__"+name
            return "### `"+full+"`\nexec tool declaration:\n```ts\ndeclare const tools: { "+full+"(args: {\n  project: string;\n}): Promise<unknown>; };\n```"
        complete="\n\n".join(signature(t["name"]) for t in build_catalog())
        tool={"type":"custom","name":"exec","description":complete,
              "format":{"type":"grammar","syntax":"lark","definition":"synthetic"}}
        eager=observe_code_mode([tool],"eager")
        self.assertTrue(eager["effective"])
        self.assertEqual(eager["nested_fixture_signatures"],len(build_catalog()))
        self.assertFalse(observe_code_mode([tool],"lazy")["effective"])
        sparse={**tool,"description":"Run JavaScript; discover with ALL_TOOLS\n"+signature("tool_search")}
        self.assertTrue(observe_code_mode([sparse],"lazy")["effective"])
        self.assertFalse(observe_code_mode([{**tool,"description":complete.replace("declare const tools:","example:")}],"eager")["effective"])
        self.assertFalse(observe_code_mode([{**sparse,"type":"function"}],"lazy")["effective"])

    def test_eager_catalog_honors_both_denial_layers_without_hiding_leaks(self):
        from codex_code_mode import observe_code_mode
        from catalog_fixture import build_catalog
        def tool(names):
            return {"type":"custom", "name":"exec", "format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"},
                    "description":"ALL_TOOLS\n"+"\n".join("declare const tools: { mcp__fixture__"+name+"(args: {project: string;}): Promise<unknown>; };" for name in names)}
        exposed={t["name"] for t in build_catalog()}-{"fixture_mutation","ledger_budget_lookup"}
        self.assertTrue(observe_code_mode([tool(exposed)], "eager", client_denied=["ledger_budget_lookup"], service_denied=["fixture_mutation"])["effective"])
        self.assertFalse(observe_code_mode([tool(exposed|{"fixture_mutation"})], "eager", client_denied=["ledger_budget_lookup"], service_denied=["fixture_mutation"])["effective"])
        self.assertFalse(observe_code_mode([tool(["fixture_mutation"])], "lazy", service_denied=["fixture_mutation"])["effective"])
        self.assertFalse(observe_code_mode([{**tool([]),"description":"Run JavaScript"}], "lazy")["effective"])

    def test_discovery_requires_identity_typed_schema_and_precedes_invocation(self):
        from codex_code_mode import observe_code_discovery
        search={"type":"custom_tool_call","call_id":"search","name":"exec","namespace":"functions","input":"text(ALL_TOOLS);"}
        schema="declare const tools: { mcp__fixture__ledger_budget_lookup(args: {\nproject: string;\n}): Promise<unknown>; };"
        output={"type":"custom_tool_call_output","call_id":"search","output":[{"type":"input_text","text":json.dumps([{"name":"mcp__fixture__ledger_budget_lookup","description":schema}])}]}
        invoke={"type":"custom_tool_call","call_id":"invoke","name":"exec","namespace":"functions","input":"text(await tools.mcp__fixture__ledger_budget_lookup({project:'alpha'}));"}
        observed=observe_code_discovery([{"input":[search,output,invoke]}])
        self.assertEqual(observed["loaded_fixture_schema_tools"],["ledger_budget_lookup"])
        self.assertEqual(observed["invocations_before_schema"],[])
        self.assertEqual(observed["observed_fixture_invocations"],["ledger_budget_lookup"])
        late=observe_code_discovery([{"input":[invoke,search,output]},{"input":[invoke,search,output]}])
        self.assertEqual(late["invocations_before_schema"],["ledger_budget_lookup"])
        for wrong in ({**output,"call_id":"different"},{**output,"output":"ledger_budget_lookup"}):
            result=observe_code_discovery([{"input":[search,wrong,invoke]}])
            self.assertEqual(result["loaded_fixture_schema_tools"],[])
        self.assertEqual(observe_code_discovery([{"input":[{**search,"namespace":"other"},output]}])["loaded_fixture_schema_tools"],[])
        self.assertEqual(observe_code_discovery([{"input":None}])["loaded_fixture_schema_tools"],[])

    def test_service_denial_is_bound_to_actual_exec_call_and_error(self):
        from codex_code_mode import observe_hidden_tool_rejection
        call={"type":"custom_tool_call","call_id":"invoke","name":"exec","namespace":"functions","input":"text(await tools.mcp__fixture__fixture_mutation({project:'alpha'}));"}
        output={"type":"custom_tool_call_output","call_id":"invoke","output":[{"type":"input_text","text":"Script error:\nTypeError: tools.mcp__fixture__fixture_mutation is not a function\n    at exec_main.mjs:1:18"}]}
        self.assertTrue(observe_hidden_tool_rejection([{"input":[call,output]}],"fixture_mutation"))
        for items in ([output], [call,{**output,"call_id":"other"}], [call,{**output,"output":"permission denied"}], [{**call,"namespace":"other"},output]):
            self.assertFalse(observe_hidden_tool_rejection([{"input":items}],"fixture_mutation"))

    def test_unqualified_exec_requires_unique_native_declaration(self):
        from codex_code_mode import observe_code_discovery
        exec_tool={"type":"custom","name":"exec","format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"}}
        search={"type":"custom_tool_call","name":"exec","call_id":"search","input":"text(ALL_TOOLS);"}
        output={"type":"custom_tool_call_output","call_id":"search","output":"[]"}
        initial={"input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"functions","tools":[exec_tool]}]}]}
        self.assertTrue(observe_code_discovery([initial,{"input":[search,output]}])["discovery_result_observed"])
        self.assertFalse(observe_code_discovery([{"input":[search,output]}])["discovery_result_observed"])
        ambiguous=deepcopy(initial)
        ambiguous["input"][0]["tools"].append({"type":"namespace","name":"other","tools":[exec_tool]})
        self.assertFalse(observe_code_discovery([ambiguous,{"input":[search,output]}])["discovery_result_observed"])
        self.assertFalse(observe_code_discovery([initial,{"input":[{**search,"namespace":"other"},output]}])["discovery_result_observed"])

    def test_custom_multitool_replay_reads_only_matching_exec_results(self):
        from task_scenarios import intermediate_receipt_replayed
        from catalog_fixture import confirmation_code
        def body(target, value, *, namespace="functions", result_id="invoke"):
            return {"input":[{"type":"custom_tool_call","name":"exec","namespace":namespace,"call_id":"invoke",
                              "input":"text(await tools.mcp__fixture__"+target+"({project:'alpha'}));"},
                             {"type":"custom_tool_call_output","call_id":result_id,"output":[{"type":"input_text","text":json.dumps(value)}]}]}
        first=body("ledger_budget_lookup",{"verification_code":"fresh"})
        second=body("ledger_budget_confirm",{"confirmation_code":confirmation_code("fresh")})
        self.assertTrue(intermediate_receipt_replayed([first,second],"fresh"))
        self.assertFalse(intermediate_receipt_replayed([second,first],"fresh"))
        for bad in (body("ledger_budget_lookup",{"verification_code":"fresh"},result_id="other"),
                    body("ledger_budget_lookup",{"verification_code":"fresh"},namespace="other"),
                    body("collection_00_read",{"verification_code":"fresh"}),
                    {"input":[first["input"][1]]},
                    {"input":[{**first["input"][0],"input":json.dumps({"verification_code":"fresh"})}]}):
            self.assertFalse(intermediate_receipt_replayed([bad,second],"fresh"))


if __name__ == "__main__":
    unittest.main()
