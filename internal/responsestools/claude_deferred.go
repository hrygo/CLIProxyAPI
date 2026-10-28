package responsestools

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// PrepareClaudeDeferredTools bridges client-executed Anthropic ToolSearch
// history before a non-Anthropic translator runs. It never searches a catalog
// or executes a tool. The caller must bypass this helper on native Messages
// routes, where defer_loading and tool_reference retain their native meaning.
func PrepareClaudeDeferredTools(body []byte, limits Limits) ([]byte, error) {
	// JSON escapes can encode either marker and need structural inspection.
	if !bytes.Contains(body, []byte(`"defer_loading"`)) && !bytes.Contains(body, []byte(`"tool_reference"`)) && !bytes.Contains(body, []byte(`\u`)) {
		return body, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, syntaxError(fmt.Errorf("invalid Anthropic request JSON"))
	}
	if !claudeHasDeferredProtocol(body) {
		return body, nil
	}
	if err := validateCustomArgumentUnicode(string(body)); err != nil {
		return nil, syntaxError(fmt.Errorf("invalid Anthropic discovery Unicode: %w", err))
	}
	value, ok := decodeValue(root["tools"])
	tools, toolsOK := value.([]any)
	if !ok || !toolsOK {
		return nil, unprocessableError(ReasonUnsupportedProtocol, fmt.Errorf("deferred search requires tool declarations"))
	}
	hasDeferred := false
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		if flag, present := tool["defer_loading"]; present {
			if _, valid := flag.(bool); !valid {
				return nil, unprocessableError(ReasonUnsupportedProtocol, fmt.Errorf("invalid Anthropic deferral flag"))
			}
		}
		if tool["defer_loading"] == true {
			hasDeferred = true
		}
	}
	// Protocol detection is structural: an explicit false flag qualifies, and
	// there is nothing deferred to bridge in that turn.
	if err := ValidateLimits(limits); err != nil {
		return nil, err
	}
	if err := validateClaudeDiscoveryJSON(body, limits.MaxDepth); err != nil {
		return nil, err
	}
	if !hasDeferred {
		replay, err := claudeHistoryNeedsSearch(root["messages"])
		if err != nil {
			return nil, err
		}
		if !replay {
			return body, nil
		}
	}
	declared := make(map[string]map[string]any, len(tools))
	for _, item := range tools {
		tool, valid := item.(map[string]any)
		name, _ := tool["name"].(string)
		if !valid || name == "" || declared[name] != nil {
			return nil, unprocessableError(ReasonAmbiguousIdentity, fmt.Errorf("invalid or duplicate Anthropic tool identity"))
		}
		declared[name] = tool
	}
	search := declared["ToolSearch"]
	active := make(map[string]bool)
	changedHistory := false
	if search == nil || search["defer_loading"] == true {
		// A client may keep the reserved placeholder after it stops declaring a
		// search function, for example once every tool is loaded eagerly. There
		// is nothing to bridge then, so the deferred declarations are dropped
		// instead of failing the turn. A discovery round still in history
		// cannot be replayed without its search function: the upstream would
		// receive a call that no declaration admits.
		replay, err := claudeHistoryNeedsSearch(root["messages"])
		if err != nil {
			return nil, err
		}
		if replay {
			return nil, unprocessableError(ReasonUnsupportedProtocol, fmt.Errorf("deferred history requires an active client ToolSearch declaration"))
		}
		var choice struct{ Type, Name string }
		_ = json.Unmarshal(root["tool_choice"], &choice)
		if choice.Type == "tool" {
			forced := declared[choice.Name]
			if forced == nil || choice.Name == "DeferredToolPlaceholder" || forced["defer_loading"] == true {
				return nil, unprocessableError(ReasonUnsupportedProtocol, fmt.Errorf("forced tool is not active after discovery"))
			}
		}
	} else {
		var messages []map[string]json.RawMessage
		if err := json.Unmarshal(root["messages"], &messages); err != nil {
			return nil, unprocessableError(ReasonHistoryLink, fmt.Errorf("invalid Anthropic discovery history"))
		}
		pending := make(map[string]string)
		seen := make(map[string]bool)
		for _, message := range messages {
			var role string
			_ = json.Unmarshal(message["role"], &role)
			var blocks []map[string]json.RawMessage
			if json.Unmarshal(message["content"], &blocks) != nil {
				continue
			}
			changedMessage := false
			for _, block := range blocks {
				var kind, id, name string
				_ = json.Unmarshal(block["type"], &kind)
				if kind == "tool_use" && role == "assistant" {
					_ = json.Unmarshal(block["id"], &id)
					_ = json.Unmarshal(block["name"], &name)
					if id == "" || seen[id] {
						return nil, unprocessableError(ReasonHistoryLink, fmt.Errorf("ambiguous Anthropic tool call identity"))
					}
					seen[id], pending[id] = true, name
					continue
				}
				if kind == "tool_reference" {
					return nil, unprocessableError(ReasonHistoryLink, fmt.Errorf("tool reference must belong to a client search result"))
				}
				if kind != "tool_result" {
					continue
				}
				_ = json.Unmarshal(block["tool_use_id"], &id)
				callName := pending[id]
				delete(pending, id)
				var parts []map[string]json.RawMessage
				if json.Unmarshal(block["content"], &parts) != nil {
					continue
				}
				var names []map[string]string
				for _, part := range parts {
					var partType string
					_ = json.Unmarshal(part["type"], &partType)
					if partType != "tool_reference" {
						continue
					}
					var failed bool
					invalidErrorFlag := false
					if raw, present := block["is_error"]; present {
						invalidErrorFlag = json.Unmarshal(raw, &failed) != nil || string(raw) == "null"
					}
					if role != "user" || callName != "ToolSearch" || failed || invalidErrorFlag {
						return nil, unprocessableError(ReasonHistoryLink, fmt.Errorf("unbound or failed client discovery result"))
					}
					_ = json.Unmarshal(part["tool_name"], &name)
					tool := declared[name]
					schema, validSchema := tool["input_schema"].(map[string]any)
					if tool == nil || name == "DeferredToolPlaceholder" || !validSchema || schema == nil {
						return nil, unprocessableError(ReasonUnsupportedProtocol, fmt.Errorf("discovery reference lacks an effective tool schema"))
					}
					active[name] = true
					names = append(names, map[string]string{"name": name})
				}
				if len(names) == 0 {
					continue
				}
				// Mixed reference/text blocks need an explicit contract rather
				// than silently dropping an error, image, or other component.
				if len(names) != len(parts) {
					return nil, unprocessableError(ReasonUnsupportedProtocol, fmt.Errorf("mixed discovery result blocks are unsupported"))
				}
				manifest, _ := json.Marshal(map[string]any{"loaded_tools": names})
				block["content"], _ = json.Marshal(string(manifest))
				changedMessage, changedHistory = true, true
			}
			if changedMessage {
				message["content"], _ = json.Marshal(blocks)
			}
		}
		var choice struct{ Type, Name string }
		_ = json.Unmarshal(root["tool_choice"], &choice)
		if choice.Type == "tool" && (declared[choice.Name] == nil || choice.Name == "DeferredToolPlaceholder" ||
			(declared[choice.Name]["defer_loading"] == true && !active[choice.Name])) {
			return nil, unprocessableError(ReasonUnsupportedProtocol, fmt.Errorf("forced tool is not active after discovery"))
		}
		if changedHistory {
			root["messages"], _ = json.Marshal(messages)
		}
	}
	root["tools"], _ = json.Marshal(filterClaudeDeferredTools(tools, active))
	if len(root["tools"]) > limits.MaxActiveToolBytes {
		return nil, budgetError(ReasonDeclarationBudget, fmt.Errorf("active Anthropic declarations exceed the tool byte budget"))
	}
	return encodeClaudeBridge(root)
}

// claudeHistoryNeedsSearch reports whether the explicit history depends on a
// client search function, either through a search call or through a discovery
// reference in a result. Only block structure counts: an argument or text that
// happens to contain the same words is not discovery.
func claudeHistoryNeedsSearch(raw json.RawMessage) (bool, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || string(trimmed) == "null" {
		return false, nil
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return false, unprocessableError(ReasonHistoryLink, fmt.Errorf("invalid Anthropic discovery history"))
	}
	for _, message := range messages {
		var role string
		_ = json.Unmarshal(message["role"], &role)
		var blocks []map[string]json.RawMessage
		if json.Unmarshal(message["content"], &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			var kind, name string
			_ = json.Unmarshal(block["type"], &kind)
			if kind == "tool_use" && role == "assistant" {
				_ = json.Unmarshal(block["name"], &name)
				if name == "ToolSearch" {
					return true, nil
				}
				continue
			}
			if kind == "tool_reference" {
				return true, nil
			}
			if kind == "tool_result" && claudeResultCarriesReference(block["content"]) {
				return true, nil
			}
		}
	}
	return false, nil
}

// claudeResultCarriesReference reports whether one tool result embeds a
// discovery reference block. A plain string result never carries one, however
// it is worded.
func claudeResultCarriesReference(content json.RawMessage) bool {
	var parts []json.RawMessage
	if json.Unmarshal(content, &parts) != nil {
		return false
	}
	for _, raw := range parts {
		var part map[string]json.RawMessage
		if json.Unmarshal(raw, &part) != nil {
			continue
		}
		var kind string
		_ = json.Unmarshal(part["type"], &kind)
		if kind == "tool_reference" {
			return true
		}
	}
	return false
}

// ClaudeDeclaresSearch reports whether an Anthropic request declares the
// client-executed ToolSearch function. A disabled client-search policy rejects
// exactly those requests; a request that only carries deferred declarations or
// the reserved placeholder has no search entry point to reject.
func ClaudeDeclaresSearch(body []byte) bool {
	var root struct {
		Tools []struct {
			Name         string          `json:"name"`
			DeferLoading json.RawMessage `json:"defer_loading"`
		} `json:"tools"`
	}
	if json.Unmarshal(body, &root) != nil {
		return false
	}
	for _, tool := range root.Tools {
		if tool.Name == "ToolSearch" && string(bytes.TrimSpace(tool.DeferLoading)) != "true" {
			return true
		}
	}
	return false
}

// filterClaudeDeferredTools drops the reserved placeholder together with the
// deferred declarations that no discovery result activated, then removes the
// native deferral flag so the upstream sees ordinary functions only. Every
// element must already be a validated declaration object with a non-empty
// name, as enforced before both call sites.
func filterClaudeDeferredTools(tools []any, active map[string]bool) []any {
	filtered := make([]any, 0, len(tools))
	for _, item := range tools {
		tool := item.(map[string]any)
		name := tool["name"].(string)
		if name == "DeferredToolPlaceholder" || (tool["defer_loading"] == true && !active[name]) {
			continue
		}
		delete(tool, "defer_loading")
		filtered = append(filtered, tool)
	}
	return filtered
}

func encodeClaudeBridge(root map[string]json.RawMessage) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(root); err != nil {
		return nil, syntaxError(fmt.Errorf("cannot encode Anthropic discovery bridge"))
	}
	return bytes.TrimSuffix(output.Bytes(), []byte("\n")), nil
}

// Inspect protocol positions, never text, schemas, tool names, or arguments.
// Retain repeated members during detection so last-member decoding cannot
// hide discovery state from the duplicate-member validation below.
func claudeHasDeferredProtocol(body []byte) bool {
	for _, raw := range claudeJSONMemberValues(body, "tools") {
		tools, _ := rawItemArray(raw)
		for _, tool := range tools {
			for _, flag := range claudeJSONMemberValues(tool, "defer_loading") {
				var deferred bool
				if json.Unmarshal(flag, &deferred) != nil || string(flag) == "null" || deferred {
					return true
				}
			}
		}
	}
	for _, raw := range claudeJSONMemberValues(body, "messages") {
		messages, _ := rawItemArray(raw)
		for _, message := range messages {
			for _, content := range claudeJSONMemberValues(message, "content") {
				blocks, _ := rawItemArray(content)
				for _, block := range blocks {
					if claudeBlockHasType(block, "tool_reference") {
						return true
					}
					if !claudeBlockHasType(block, "tool_result") {
						continue
					}
					for _, result := range claudeJSONMemberValues(block, "content") {
						parts, _ := rawItemArray(result)
						for _, part := range parts {
							if claudeBlockHasType(part, "tool_reference") {
								return true
							}
						}
					}
				}
			}
		}
	}
	return false
}

func claudeBlockHasType(raw json.RawMessage, kind string) bool {
	for _, value := range claudeJSONMemberValues(raw, "type") {
		var blockType string
		if json.Unmarshal(value, &blockType) == nil && blockType == kind {
			return true
		}
	}
	return false
}

func claudeJSONMemberValues(raw json.RawMessage, member string) []json.RawMessage {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil
	}
	var values []json.RawMessage
	for decoder.More() {
		key, errKey := decoder.Token()
		var value json.RawMessage
		if errKey != nil || decoder.Decode(&value) != nil {
			return nil
		}
		if key == member {
			values = append(values, value)
		}
	}
	return values
}

// Refuse duplicate JSON members before history interpretation. Otherwise a
// translator using first-member semantics can disagree with the bridge's
// last-member decoding about the name or id that authorized a reference.
func validateClaudeDiscoveryJSON(body []byte, maximum int) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > maximum {
			return schemaError("schema_depth_exceeded")
		}
		token, err := decoder.Token()
		if err != nil {
			return syntaxError(fmt.Errorf("invalid Anthropic discovery JSON"))
		}
		delimiter, container := token.(json.Delim)
		if !container {
			return nil
		}
		switch delimiter {
		case '{':
			members := make(map[string]bool)
			for decoder.More() {
				key, err := decoder.Token()
				name, valid := key.(string)
				if err != nil || !valid {
					return syntaxError(fmt.Errorf("invalid Anthropic discovery member"))
				}
				if members[name] {
					return unprocessableError(ReasonAmbiguousIdentity, fmt.Errorf("duplicate Anthropic discovery JSON member"))
				}
				members[name] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return syntaxError(fmt.Errorf("invalid Anthropic discovery container"))
		}
		if _, err := decoder.Token(); err != nil {
			return syntaxError(fmt.Errorf("incomplete Anthropic discovery container"))
		}
		return nil
	}
	return walk(0)
}
