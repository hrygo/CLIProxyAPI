package executor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// WireToolHistoryEvent describes one native history event without its payload.
type WireToolHistoryEvent struct {
	Type            string
	ItemID          string
	CallID          string
	CallInputSHA256 string
	OutputSHA256    string
}

// NativeWireToolHistoryEvents records native tool-event order, item identities
// and canonical call-input/output hashes. Exact number tokens survive
// canonicalization; raw payloads are never retained in the guard context.
func NativeWireToolHistoryEvents(body []byte) ([]WireToolHistoryEvent, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	input := bytes.TrimSpace(root["input"])
	if len(input) == 0 || input[0] != '[' {
		return nil, nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(input, &items); err != nil {
		return nil, err
	}
	var events []WireToolHistoryEvent
	for _, item := range items {
		var event WireToolHistoryEvent
		if err := json.Unmarshal(item["type"], &event.Type); err != nil {
			continue
		}
		switch event.Type {
		case "function_call", "custom_tool_call", "function_call_output", "custom_tool_call_output":
		default:
			continue
		}
		if raw, exists := item["id"]; exists {
			if err := json.Unmarshal(raw, &event.ItemID); err != nil {
				return nil, err
			}
		}
		if err := json.Unmarshal(item["call_id"], &event.CallID); err != nil {
			return nil, err
		}
		if event.Type == "function_call" || event.Type == "custom_tool_call" {
			field := "arguments"
			if event.Type == "custom_tool_call" {
				field = "input"
			}
			callInput := make(map[string]json.RawMessage, 1)
			if raw, exists := item[field]; exists {
				// The Codex translator legitimately normalizes blank function
				// arguments to "{}". Custom source text is never normalized.
				var arguments *string
				if field == "arguments" && json.Unmarshal(raw, &arguments) == nil && arguments != nil && strings.TrimSpace(*arguments) == "" {
					raw = json.RawMessage(`"{}"`)
				}
				callInput[field] = raw
			}
			var err error
			event.CallInputSHA256, err = canonicalWireHistorySHA256(callInput)
			if err != nil {
				return nil, err
			}
		} else {
			var err error
			event.OutputSHA256, err = canonicalWireHistorySHA256(item)
			if err != nil {
				return nil, err
			}
		}
		events = append(events, event)
	}
	return events, nil
}

func canonicalWireHistorySHA256(item map[string]json.RawMessage) (string, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}
