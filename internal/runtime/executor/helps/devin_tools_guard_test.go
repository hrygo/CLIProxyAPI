package helps

import (
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"google.golang.org/protobuf/encoding/protowire"
)

func devinGuardBytes(num protowire.Number, value []byte) []byte {
	out := protowire.AppendTag(nil, num, protowire.BytesType)
	return protowire.AppendBytes(out, value)
}

func devinGuardVarint(num protowire.Number, value uint64) []byte {
	out := protowire.AppendTag(nil, num, protowire.VarintType)
	return protowire.AppendVarint(out, value)
}

func devinGuardFrame(tool, call, result []byte) []byte {
	wire := devinGuardBytes(10, tool)
	wire = append(wire, devinGuardBytes(3, call)...)
	wire = append(wire, devinGuardBytes(3, result)...)
	return WrapConnectEnvelope(wire)
}

func TestDevinGuardValidatesConnectProtobufAndHistory(t *testing.T) {
	tool := append(devinGuardBytes(1, []byte("lookup")), devinGuardBytes(3, []byte(`{"type":"object"}`))...)
	call := append(devinGuardBytes(1, []byte("native:call")), devinGuardBytes(2, []byte("lookup"))...)
	call = append(devinGuardBytes(3, []byte(`{"q":"test"}`)), call...)
	assistant := append(devinGuardVarint(2, 2), devinGuardBytes(6, call)...)
	result := append(devinGuardVarint(2, 4), devinGuardBytes(7, []byte("native:call"))...)
	valid := devinGuardFrame(tool, assistant, result)
	ctx := cliproxyexecutor.WithWireContract(context.Background(), &cliproxyexecutor.WireContract{
		CustomAliases: []string{"lookup"}, RequiredFunctionNames: []string{"lookup"}, ExactFunctionNames: true,
		HistoryCalls: []cliproxyexecutor.WireToolHistoryReference{{Name: "lookup", CallID: EncodeDevinToolCallID("native:call")}},
	})
	if err := ValidateDevinOutboundToolContract(ctx, valid); err != nil {
		t.Fatalf("valid Connect frame rejected: %v", err)
	}
	malformedTool := append(append([]byte(nil), tool...), devinGuardBytes(1, []byte("other"))...)
	unknownTool := append(append([]byte(nil), tool...), devinGuardVarint(4, 1)...)
	unknownCall := append(append([]byte(nil), call...), devinGuardVarint(4, 1)...)
	malformedAssistant := append(devinGuardVarint(2, 2), devinGuardBytes(6, unknownCall)...)
	malformedLength := append(devinGuardVarint(2, 2), protowire.AppendTag(nil, 6, protowire.BytesType)...)
	malformedLength = append(malformedLength, 0x05, 0x0a, 0x01)
	badFlag := append([]byte(nil), valid...)
	badFlag[0] = ConnectFlagCompressed
	badLength := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(badLength[1:5], uint32(len(badLength)))
	wrongSource := append(devinGuardVarint(2, 1), devinGuardBytes(6, call)...)
	wrongWireType := append(devinGuardVarint(1, 1), devinGuardBytes(3, []byte(`{}`))...)
	invalidJSON := append(devinGuardBytes(1, []byte("lookup")), devinGuardBytes(3, []byte(`{`))...)
	whitespaceName := devinGuardBytes(1, []byte(" lookup "))
	resultFirst := devinGuardFrame(tool, result, assistant)
	duplicateResult := append(append([]byte(nil), valid[5:]...), devinGuardBytes(3, result)...)
	unknownRoot := append(append([]byte(nil), valid[5:]...), devinGuardVarint(22, 1)...)
	for _, tc := range []struct {
		name  string
		frame []byte
	}{
		{"truncated envelope", valid[:4]},
		{"wrong frame flag", badFlag},
		{"wrong frame length", badLength},
		{"extra frame bytes", append(append([]byte(nil), valid...), 0)},
		{"invalid protobuf tag", WrapConnectEnvelope([]byte{0})},
		{"unknown root field", WrapConnectEnvelope(unknownRoot)},
		{"duplicate singular tool field", devinGuardFrame(malformedTool, assistant, result)},
		{"unknown tool field", devinGuardFrame(unknownTool, assistant, result)},
		{"unknown call field", devinGuardFrame(tool, malformedAssistant, result)},
		{"malformed nested length", devinGuardFrame(tool, malformedLength, result)},
		{"wrong tool field wire type", devinGuardFrame(wrongWireType, assistant, result)},
		{"invalid tool schema JSON", devinGuardFrame(invalidJSON, assistant, result)},
		{"whitespace tool name", devinGuardFrame(whitespaceName, assistant, result)},
		{"wrong call source", devinGuardFrame(tool, wrongSource, result)},
		{"result before call", resultFirst},
		{"duplicate result", WrapConnectEnvelope(duplicateResult)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDevinOutboundToolContract(ctx, tc.frame)
			var status interface {
				StatusCode() int
				IsRequestScoped() bool
				AvailabilityNeutral() bool
			}
			if !errors.As(err, &status) || status.StatusCode() != http.StatusUnprocessableEntity || !status.IsRequestScoped() || !status.AvailabilityNeutral() {
				t.Fatalf("error = %v, want request-scoped availability-neutral 422", err)
			}
		})
	}
	if err := ValidateDevinOutboundToolContract(context.Background(), []byte("not a frame")); err != nil {
		t.Fatalf("inactive contract must not alter the request: %v", err)
	}
}

func TestDevinGuardRejectsLossyNativeHistoryAndIDCollisions(t *testing.T) {
	tool := devinGuardBytes(1, []byte("lookup"))
	call := append(devinGuardBytes(1, []byte("native:call")), devinGuardBytes(2, []byte("lookup"))...)
	assistant := append(devinGuardVarint(2, 2), devinGuardBytes(6, call)...)
	result := append(devinGuardVarint(2, 4), devinGuardBytes(7, []byte("native:call"))...)
	frame := devinGuardFrame(tool, assistant, result)
	for _, contract := range []*cliproxyexecutor.WireContract{
		{MultipleCustomOutputs: true, NativeHistoryEvents: []cliproxyexecutor.WireToolHistoryEvent{{Type: "custom_tool_call"}}},
		{MultipleCustomOutputs: true},
		{CustomAliases: []string{"lookup"}, HistoryCalls: []cliproxyexecutor.WireToolHistoryReference{
			{Name: "lookup", CallID: "native:call"}, {Name: "lookup", CallID: EncodeDevinToolCallID("native:call")},
		}},
	} {
		ctx := cliproxyexecutor.WithWireContract(context.Background(), contract)
		if err := ValidateDevinOutboundToolContract(ctx, frame); err == nil {
			t.Fatal("lossy history or duplicate decoded call ID was accepted")
		}
	}
}

func TestDevinGuardPreservesWhitespaceInNativeCallIDs(t *testing.T) {
	tool := devinGuardBytes(1, []byte("lookup"))
	ctx := cliproxyexecutor.WithWireContract(context.Background(), &cliproxyexecutor.WireContract{
		CustomAliases: []string{"lookup"}, HistoryCalls: []cliproxyexecutor.WireToolHistoryReference{{Name: "lookup", CallID: EncodeDevinToolCallID(" call ")}},
	})
	for _, id := range []string{" call ", "call"} {
		call := append(devinGuardBytes(1, []byte(id)), devinGuardBytes(2, []byte("lookup"))...)
		assistant := append(devinGuardVarint(2, 2), devinGuardBytes(6, call)...)
		result := append(devinGuardVarint(2, 4), devinGuardBytes(7, []byte(id))...)
		err := ValidateDevinOutboundToolContract(ctx, devinGuardFrame(tool, assistant, result))
		if (err != nil) != (id == "call") {
			t.Fatalf("identity %q: %v", id, err)
		}
	}
}
