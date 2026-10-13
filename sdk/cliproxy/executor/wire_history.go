package executor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// WireToolHistoryEvent describes one native history event without its payload.
type WireToolHistoryEvent struct {
	Type         string
	ItemID       string
	CallID       string
	OutputSHA256 string
}

// NativeWireToolHistoryEvents records native tool-event order, item identities
// and canonical output hashes. Exact number tokens survive canonicalization.
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
		if event.Type == "function_call_output" || event.Type == "custom_tool_call_output" {
			{
				raw, err := json.Marshal(item)
				if err != nil {
					return nil, err
				}
				decoder := json.NewDecoder(bytes.NewReader(raw))
				decoder.UseNumber()
				var output any
				if err := decoder.Decode(&output); err != nil {
					return nil, err
				}
				canonical, err := json.Marshal(output)
				if err != nil {
					return nil, err
				}
				digest := sha256.Sum256(canonical)
				event.OutputSHA256 = hex.EncodeToString(digest[:])
			}
		}
		events = append(events, event)
	}
	return events, nil
}
