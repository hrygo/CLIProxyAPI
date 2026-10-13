package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	"gopkg.in/yaml.v3"
)

func TestResponsesToolsCustomOutputReplayRouteBoundary(t *testing.T) {
	t.Parallel()
	body := []byte(`{"tools":[{"type":"custom","name":"exec"}],"input":[{"type":"custom_tool_call","id":"ctc_first","call_id":"first","name":"exec","input":"notify(\"notice\")"},{"type":"custom_tool_call_output","id":"ctco_notice","call_id":"first","output":"notice"},{"type":"custom_tool_call","id":"ctc_second","call_id":"second","name":"exec","input":"text(\"side\")"},{"type":"custom_tool_call_output","id":"ctco_side","call_id":"second","output":"side"},{"type":"custom_tool_call_output","id":"ctco_final","call_id":"first","output":[{"type":"input_text","text":"final 漢字"}]}]}`)
	for _, test := range []struct {
		name        string
		format      string
		mode        string
		validPolicy bool
		accept      bool
	}{
		{"native multiple", "codex", "multiple", true, true},
		{"Responses multiple", "openai-response", "multiple", true, true},
		{"explicit single", "codex", "single", true, false},
		{"default single", "codex", "", true, false},
		{"unknown mode", "codex", "aggregate", false, false},
		{"Chat rejected", "openai", "multiple", false, false},
		{"Messages rejected", "claude", "multiple", false, false},
		{"unknown format rejected", "future-format", "multiple", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := "routes:\n  - match:\n      provider: codex\n      auth-kind: oauth\n      upstream-model: m\n      upstream-format: " + test.format + "\n    client-search: native\n    custom-tools: function\n"
			if test.mode != "" {
				document += "    custom-output-replay: " + test.mode + "\n"
			}
			cfg := &Config{}
			if err := yaml.Unmarshal([]byte(document), &cfg.ResponsesTools); err != nil {
				t.Fatal(err)
			}
			cfg.NormalizeResponsesToolsConfig()
			policy, err := cfg.ResponsesTools.Compile()
			if !test.validPolicy {
				if err == nil {
					t.Fatal("unsupported replay policy compiled")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			limiter := responsestools.NewLimiter(policy.Limits)
			prepared, err := responsestools.Prepare(body, policy.Routes[0], policy.Limits, limiter)
			if !test.accept {
				var compatibility *responsestools.ToolCompatibilityError
				if !errors.As(err, &compatibility) || compatibility.Reason != responsestools.ReasonUnsupportedProtocol || prepared.Attempt != nil {
					t.Fatalf("default policy did not reject unsupported output replay: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Attempt == nil {
				t.Fatal("missing custom adaptation")
			}
			defer prepared.Attempt.Close()
			var root map[string]json.RawMessage
			if err := json.Unmarshal(prepared.Body, &root); err != nil {
				t.Fatal(err)
			}
			var items []map[string]json.RawMessage
			if err := json.Unmarshal(root["input"], &items); err != nil || len(items) != 5 {
				t.Fatal("multi-output history changed its length")
			}
			for _, expected := range []struct {
				index  int
				id     string
				callID string
				output string
			}{
				{1, `"fco_notice"`, `"first"`, `"notice"`},
				{3, `"fco_side"`, `"second"`, `"side"`},
				{4, `"fco_final"`, `"first"`, `[{"text":"final 漢字","type":"input_text"}]`},
			} {
				item := items[expected.index]
				if string(item["type"]) != `"function_call_output"` || string(item["id"]) != expected.id || string(item["call_id"]) != expected.callID || string(item["output"]) != expected.output {
					t.Fatalf("output content, identity or global order changed at %d: %s", expected.index, root["input"])
				}
			}
			prepared.Attempt.Close()
			if n, b := limiter.Usage(); n != 0 || b != 0 {
				t.Fatalf("replay lease leaked: %d/%d", n, b)
			}
		})
	}
}

func TestResponsesToolsDefaultsFollowConvention(t *testing.T) {
	cfg := &Config{}
	cfg.NormalizeResponsesToolsConfig()
	if !cfg.ResponsesTools.FeatureEnabled() {
		t.Fatalf("convention must stay enabled without configuration")
	}
	limits := cfg.ResponsesTools.Limits
	defaults := responsestools.DefaultLimits()
	if limits.MaxActiveToolBytes != defaults.MaxActiveToolBytes ||
		limits.MaxActiveAttempts != defaults.MaxActiveAttempts ||
		limits.MaxStateBytes != defaults.MaxStateBytes ||
		limits.MaxAttemptBytes != defaults.MaxAttemptBytes ||
		limits.MaxSchemaExpansionBytes != defaults.MaxSchemaExpansionBytes ||
		limits.MaxSchemaExpansionNodes != defaults.MaxSchemaExpansionNodes ||
		limits.MaxDepth != defaults.MaxDepth {
		t.Fatalf("limits not defaulted: %+v", limits)
	}
	if err := cfg.ValidateResponsesToolsConfig(); err != nil {
		t.Fatalf("empty policy must validate: %v", err)
	}
	policy, err := cfg.ResponsesTools.Compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !policy.Enabled || len(policy.Routes) != 0 {
		t.Fatalf("convention policy must be enabled with no explicit routes")
	}
	off := false
	cfg.ResponsesTools.Enabled = &off
	if cfg.ResponsesTools.FeatureEnabled() {
		t.Fatalf("explicit false must disable the emergency gate")
	}
}

func TestResponsesToolsInvalidEnumRejected(t *testing.T) {
	cfg := &Config{}
	cfg.ResponsesTools.Enabled = boolPointer(true)
	cfg.ResponsesTools.Routes = []ResponsesToolsRoute{{
		Match:        ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: "m", UpstreamFormat: "codex"},
		ClientSearch: "sometimes",
		CustomTools:  "inherit",
	}}
	cfg.NormalizeResponsesToolsConfig()
	if err := cfg.ValidateResponsesToolsConfig(); err == nil {
		t.Fatalf("expected enum rejection")
	}
}

func TestResponsesToolsFlattenEnumCompiles(t *testing.T) {
	cfg := &Config{}
	cfg.ResponsesTools.Enabled = boolPointer(true)
	cfg.ResponsesTools.Routes = []ResponsesToolsRoute{{
		Match:  ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: "m", UpstreamFormat: "codex", BaseURL: "https://example.invalid/v1"},
		Schema: ResponsesToolsSchema{LocalRefs: "flatten"},
	}}
	cfg.NormalizeResponsesToolsConfig()
	if err := cfg.ValidateResponsesToolsConfig(); err != nil {
		t.Fatalf("flatten must validate: %v", err)
	}
	policy, err := cfg.ResponsesTools.Compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if policy.Routes[0].Schema.LocalRefs != responsestools.LocalRefsFlatten {
		t.Fatalf("flatten lost in compile: %+v", policy.Routes[0].Schema)
	}
}

func TestResponsesToolsOverlapRejected(t *testing.T) {
	match := ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: "m", UpstreamFormat: "codex"}
	cfg := &Config{}
	cfg.ResponsesTools.Enabled = boolPointer(true)
	cfg.ResponsesTools.Routes = []ResponsesToolsRoute{
		{Match: match, ClientSearch: "bridge", CustomTools: "function"},
		{Match: match, ClientSearch: "bridge", CustomTools: "strip"},
	}
	cfg.NormalizeResponsesToolsConfig()
	if err := cfg.ValidateResponsesToolsConfig(); err == nil {
		t.Fatalf("expected overlap rejection")
	}
}

func TestResponsesToolsYAMLRoundTrip(t *testing.T) {
	document := "responses-tools:\n" +
		"  enabled: true\n" +
		"  limits:\n" +
		"    max-active-tool-bytes: 100\n" +
		"  routes:\n" +
		"    - match:\n" +
		"        provider: codex\n" +
		"        auth-kind: oauth\n" +
		"        upstream-model: m-x\n" +
		"        upstream-format: codex\n" +
		"      client-search: bridge\n" +
		"      custom-tools: function\n" +
		"      custom-output-replay: multiple\n"
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.ResponsesTools.FeatureEnabled() {
		t.Fatalf("enabled lost")
	}
	if len(cfg.ResponsesTools.Routes) != 1 {
		t.Fatalf("routes lost")
	}
	route := cfg.ResponsesTools.Routes[0]
	if route.ClientSearch != "bridge" || route.CustomTools != "function" || route.CustomOutputReplay != "multiple" {
		t.Fatalf("strategies lost: %+v", route)
	}
	if route.CustomGrammar != "" || route.Schema.LocalRefs != "" {
		t.Fatalf("omitted strategies must stay omitted, not defaulted: %+v", route)
	}
	cloned := cfg.CloneForRuntime()
	if len(cloned.ResponsesTools.Routes) != 1 || !cloned.ResponsesTools.FeatureEnabled() {
		t.Fatalf("clone lost responses-tools")
	}
	cloned.ResponsesTools.Routes[0].ClientSearch = "native"
	cloned.ResponsesTools.Routes[0].CustomOutputReplay = "single"
	if cfg.ResponsesTools.Routes[0].ClientSearch != "bridge" || cfg.ResponsesTools.Routes[0].CustomOutputReplay != "multiple" {
		t.Fatalf("clone shares route memory")
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func TestResponsesToolsLegacyRootKeyMigratesUnderV8(t *testing.T) {
	document := "host: \"127.0.0.1\"\n" +
		"port: 8317\n" +
		"responses-tools:\n" +
		"  enabled: true\n" +
		"  limits:\n" +
		"    max-active-tool-bytes: 100\n"

	migrated, _, err := NormalizeConfigLayout([]byte(document), true)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err = ValidateV8Config(migrated); err != nil {
		t.Fatalf("migrated layout is not valid v8: %v", err)
	}

	var doc yaml.Node
	if err = yaml.Unmarshal(migrated, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The legacy spelling must land on the nested path, not be retained as a
	// comment or dropped: v8 rejects the root-level key on write.
	if node := yamlPath(doc.Content[0], "requests.responses-tools.enabled"); node == nil {
		t.Fatalf("requests.responses-tools.enabled missing after migration:\n%s", migrated)
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err = os.WriteFile(path, migrated, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.ResponsesTools.FeatureEnabled() {
		t.Fatalf("enabled lost across migration")
	}
}
