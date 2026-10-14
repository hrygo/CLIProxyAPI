package executor

import (
	"slices"
	"testing"
)

func TestNativeWireToolHistoryEventsProtectCallInputs(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		left  string
		right string
		equal bool
	}{
		{"function code mutation", `"arguments":"{\"input\":\"text('original')\"}"`, `"arguments":"{\"input\":\"text('changed')\"}"`, false},
		{"outer JSON escaping", `"arguments":"{\"input\":\"a\"}"`, `"arguments":"{\"input\":\"\u0061\"}"`, true},
		{"blank function normalization", `"arguments":" \n\t"`, `"arguments":"{}"`, true},
		{"null is not blank", `"arguments":null`, `"arguments":"{}"`, false},
		{"missing is not null", `"status":"completed"`, `"arguments":null`, false},
		{"object is not string", `"arguments":{}`, `"arguments":"{}"`, false},
		{"structured argument exact integers", `"arguments":{"value":9007199254740993}`, `"arguments":{"value":9007199254740992}`, false},
		{"status is outside call input", `"arguments":"{}","status":"completed"`, `"arguments":"{}","status":"in_progress"`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			left, err := NativeWireToolHistoryEvents([]byte(`{"input":[{"type":"function_call","call_id":"c1","name":"exec",` + test.left + `}]}`))
			if err != nil {
				t.Fatal(err)
			}
			right, err := NativeWireToolHistoryEvents([]byte(`{"input":[{"type":"function_call","call_id":"c1","name":"exec",` + test.right + `}]}`))
			if err != nil {
				t.Fatal(err)
			}
			if len(left) != 1 || len(right) != 1 || left[0].CallInputSHA256 == "" || left[0].OutputSHA256 != "" {
				t.Fatal("call input was not protected independently of output")
			}
			if equal := slices.Equal(left, right); equal != test.equal {
				t.Fatalf("equivalent call inputs = %v, want %v", equal, test.equal)
			}
		})
	}
}

func TestNativeWireToolHistoryEventsPreserveCustomSource(t *testing.T) {
	t.Parallel()
	var previous []WireToolHistoryEvent
	for _, input := range []string{`""`, `"{}"`, `" "`, `null`, `{"code":"notify('hello')"}`} {
		events, err := NativeWireToolHistoryEvents([]byte(`{"input":[{"type":"custom_tool_call","call_id":"c1","name":"exec","input":` + input + `}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 || events[0].CallInputSHA256 == "" {
			t.Fatal("custom source input is unprotected")
		}
		for _, other := range previous {
			if events[0].CallInputSHA256 == other.CallInputSHA256 {
				t.Fatal("distinct custom source inputs were normalized together")
			}
		}
		previous = append(previous, events[0])
	}
}
