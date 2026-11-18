package helps

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"google.golang.org/protobuf/encoding/protowire"
)

type devinGuardField struct {
	kind     protowire.Type
	repeated bool
	text     bool
	json     bool
	children map[protowire.Number]devinGuardField
}

// These are the fields emitted by BuildDevinGetChatMessageRequest. Unknown
// fields fail closed while a tool contract is active: their meaning cannot be
// determined from the wire alone, in particular for tool-related messages.
var devinGuardFields = map[protowire.Number]devinGuardField{
	1: {kind: protowire.BytesType, children: map[protowire.Number]devinGuardField{
		1: {kind: protowire.BytesType, text: true}, 2: {kind: protowire.BytesType, text: true},
		3: {kind: protowire.BytesType, text: true}, 4: {kind: protowire.BytesType, text: true},
		5: {kind: protowire.BytesType, text: true}, 7: {kind: protowire.BytesType, text: true},
		12: {kind: protowire.BytesType, text: true}, 31: {kind: protowire.BytesType, text: true},
	}},
	2: {kind: protowire.BytesType, text: true},
	3: {kind: protowire.BytesType, repeated: true, children: map[protowire.Number]devinGuardField{
		1: {kind: protowire.BytesType, text: true}, 2: {kind: protowire.VarintType}, 3: {kind: protowire.BytesType, text: true},
		6: {kind: protowire.BytesType, repeated: true, children: map[protowire.Number]devinGuardField{
			1: {kind: protowire.BytesType, text: true}, 2: {kind: protowire.BytesType, text: true}, 3: {kind: protowire.BytesType, text: true},
		}},
		7: {kind: protowire.BytesType, text: true},
		10: {kind: protowire.BytesType, repeated: true, children: map[protowire.Number]devinGuardField{
			1: {kind: protowire.BytesType, text: true}, 2: {kind: protowire.BytesType, text: true},
		}},
		11: {kind: protowire.BytesType, text: true}, 12: {kind: protowire.BytesType}, 18: {kind: protowire.BytesType, text: true},
	}},
	7: {kind: protowire.VarintType},
	8: {kind: protowire.BytesType, children: map[protowire.Number]devinGuardField{
		1: {kind: protowire.VarintType}, 2: {kind: protowire.VarintType}, 3: {kind: protowire.VarintType},
		5: {kind: protowire.Fixed64Type}, 7: {kind: protowire.VarintType}, 8: {kind: protowire.Fixed64Type},
	}},
	10: {kind: protowire.BytesType, repeated: true, children: map[protowire.Number]devinGuardField{
		1: {kind: protowire.BytesType, text: true}, 2: {kind: protowire.BytesType, text: true}, 3: {kind: protowire.BytesType, json: true},
	}},
	15: {kind: protowire.BytesType, children: map[protowire.Number]devinGuardField{
		1: {kind: protowire.BytesType, text: true}, 2: {kind: protowire.VarintType},
		3: {kind: protowire.VarintType}, 4: {kind: protowire.VarintType},
	}},
	16: {kind: protowire.BytesType, text: true}, 20: {kind: protowire.VarintType}, 21: {kind: protowire.BytesType, text: true},
}

func validateDevinGuardMessage(data []byte, fields map[protowire.Number]devinGuardField) error {
	seen := make(map[protowire.Number]bool)
	for len(data) > 0 {
		num, kind, tagLen := protowire.ConsumeTag(data)
		if tagLen < 0 {
			return fmt.Errorf("invalid protobuf tag")
		}
		data = data[tagLen:]
		field, known := fields[num]
		if !known || kind != field.kind || (!field.repeated && seen[num]) {
			return fmt.Errorf("unexpected or repeated protobuf field %d", num)
		}
		seen[num] = true
		n := protowire.ConsumeFieldValue(num, kind, data)
		if n < 0 {
			return fmt.Errorf("invalid protobuf field %d", num)
		}
		if kind == protowire.BytesType {
			value, size := protowire.ConsumeBytes(data)
			if size < 0 {
				return fmt.Errorf("invalid protobuf bytes field %d", num)
			}
			if field.text && !utf8.Valid(value) {
				return fmt.Errorf("invalid protobuf text field %d", num)
			}
			if field.json && !json.Valid(value) {
				return fmt.Errorf("invalid protobuf JSON field %d", num)
			}
			if field.children != nil {
				if err := validateDevinGuardMessage(value, field.children); err != nil {
					return err
				}
			}
		}
		data = data[n:]
	}
	return nil
}

// ValidateDevinOutboundToolContract decodes the exact Connect frame that will
// be sent, then validates its tool declarations and history against the shared
// contract. It never records the payload, credentials, arguments, or results.
func ValidateDevinOutboundToolContract(ctx context.Context, frame []byte) error {
	wire := WireContractFromContext(ctx)
	if wire == nil {
		return nil
	}
	invalid := func(err error) error {
		return wireError(http.StatusUnprocessableEntity, "cannot validate Devin Connect tool contract: %v", err)
	}
	if len(frame) < 5 || frame[0] != ConnectFlagData || uint64(binary.BigEndian.Uint32(frame[1:5])) != uint64(len(frame)-5) {
		return invalid(fmt.Errorf("invalid Connect request envelope"))
	}
	data := frame[5:]
	if err := validateDevinGuardMessage(data, devinGuardFields); err != nil {
		return invalid(err)
	}
	_, native, err := FinalizeDevinPayload(data, func(body []byte) []byte { return body })
	if err != nil {
		return invalid(fmt.Errorf("invalid protobuf payload"))
	}
	var view struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"tools"`
		Prompts []struct {
			Source    uint64 `json:"source"`
			ToolCalls []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"prompts"`
	}
	if err := json.Unmarshal(native, &view); err != nil {
		return invalid(fmt.Errorf("invalid decoded protobuf payload"))
	}
	if wire.MultipleCustomOutputs && len(wire.NativeHistoryEvents) > 0 {
		return invalid(fmt.Errorf("native tool history cannot be recovered losslessly from Devin protobuf"))
	}
	tools := make([]map[string]any, 0, len(view.Tools))
	for _, tool := range view.Tools {
		if tool.Name == "" || tool.Name != strings.TrimSpace(tool.Name) {
			return invalid(fmt.Errorf("invalid tool identity"))
		}
		declaration := map[string]any{"type": "function", "name": tool.Name}
		if tool.Description != "" {
			declaration["description"] = tool.Description
		}
		if len(tool.Parameters) > 0 {
			declaration["parameters"] = tool.Parameters
		}
		tools = append(tools, declaration)
	}
	// Encodings preserve even native IDs with whitespace and avoid collisions
	// when the shared JSON guard normalizes string fields.
	idToken := func(id string) string {
		if id == "" {
			return ""
		}
		return "devin_" + base64.RawURLEncoding.EncodeToString([]byte(id))
	}
	adapted := *wire
	adapted.HistoryCalls = append([]cliproxyexecutor.WireToolHistoryReference(nil), wire.HistoryCalls...)
	expectedIDs := make(map[string]struct{}, len(adapted.HistoryCalls))
	for i := range adapted.HistoryCalls {
		id := DecodeDevinToolCallID(adapted.HistoryCalls[i].CallID)
		adapted.HistoryCalls[i].CallID = idToken(id)
		expectedIDs[adapted.HistoryCalls[i].CallID] = struct{}{}
	}
	seenCalls := make(map[string]bool)
	var messages []map[string]any
	for _, prompt := range view.Prompts {
		if len(prompt.ToolCalls) > 0 {
			if prompt.Source != 2 || prompt.ToolCallID != "" || wire.MultipleCustomOutputs {
				return invalid(fmt.Errorf("invalid or unsupported tool call history"))
			}
			var calls []map[string]any
			for _, call := range prompt.ToolCalls {
				if call.ID == "" || call.Name == "" || call.Name != strings.TrimSpace(call.Name) {
					return invalid(fmt.Errorf("invalid tool call identity"))
				}
				id := idToken(call.ID)
				seenCalls[id] = true
				calls = append(calls, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": call.Name}})
			}
			messages = append(messages, map[string]any{"role": "assistant", "tool_calls": calls})
		}
		if prompt.ToolCallID != "" || prompt.Source == 4 {
			if prompt.Source != 4 || prompt.ToolCallID == "" || wire.MultipleCustomOutputs {
				return invalid(fmt.Errorf("invalid or unsupported tool result history"))
			}
			id := idToken(prompt.ToolCallID)
			if _, expected := expectedIDs[id]; expected && !seenCalls[id] {
				return invalid(fmt.Errorf("tool result precedes its call"))
			}
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": id})
		}
	}
	canonical, err := json.Marshal(map[string]any{"tools": tools, "messages": messages})
	if err != nil {
		return invalid(fmt.Errorf("cannot represent protobuf tool fields"))
	}
	guardCtx := cliproxyexecutor.WithWireContract(ctx, &adapted)
	return ValidateOutboundToolContract(guardCtx, bytes.TrimSpace(canonical), adapted.MaxActiveToolBytes)
}

// ValidateDevinOutboundToolContractRequest inspects the body the transport
// will actually read, rather than trusting a possibly unrelated GetBody clone.
func ValidateDevinOutboundToolContractRequest(ctx context.Context, request *http.Request) error {
	if WireContractFromContext(ctx) == nil {
		return nil
	}
	if request == nil || request.Body == nil {
		return wireError(http.StatusUnprocessableEntity, "cannot inspect Devin Connect request body")
	}
	body, err := io.ReadAll(request.Body)
	errClose := request.Body.Close()
	if err != nil || errClose != nil {
		return wireError(http.StatusUnprocessableEntity, "cannot read Devin Connect request body")
	}
	if err := ValidateDevinOutboundToolContract(ctx, body); err != nil {
		return err
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return nil
}
