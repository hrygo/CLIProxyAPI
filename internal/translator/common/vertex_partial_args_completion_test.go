package common

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestVertexPartialArgsCompletionReportsUnfinishedValues(t *testing.T) {
	for _, tc := range []struct {
		name       string
		frames     []string
		unfinished bool
	}{
		{"empty final leaves previous value open", []string{`{"partialArgs":[{"jsonPath":"$.a","stringValue":"x","willContinue":true}],"willContinue":true}`, `{}`}, true},
		{"value open on final frame", []string{`{"partialArgs":[{"jsonPath":"$.a","stringValue":"x","willContinue":true}],"willContinue":false}`}, true},
		{"same frame closes value", []string{`{"partialArgs":[{"jsonPath":"$.a","stringValue":"x","willContinue":true},{"jsonPath":"$.a","stringValue":"y"}],"willContinue":false}`}, false},
		{"explicit path end", []string{`{"partialArgs":[{"jsonPath":"$.a","stringValue":"x","willContinue":true}],"willContinue":true}`, `{"partialArgs":[{"jsonPath":"$.a"}],"willContinue":false}`}, false},
		{"equivalent quoted path closes", []string{`{"partialArgs":[{"jsonPath":"$['a']","stringValue":"x","willContinue":true}],"willContinue":true}`, `{"partialArgs":[{"jsonPath":"$.a","stringValue":"y"}],"willContinue":false}`}, false},
		{"interleaved paths leave one open", []string{`{"partialArgs":[{"jsonPath":"$.a","stringValue":"x","willContinue":true},{"jsonPath":"$.b","stringValue":"y","willContinue":true}],"willContinue":true}`, `{"partialArgs":[{"jsonPath":"$.a","stringValue":"z"}],"willContinue":false}`}, true},
		{"interleaved paths both closed", []string{`{"partialArgs":[{"jsonPath":"$.a","stringValue":"x","willContinue":true},{"jsonPath":"$.b","stringValue":"y","willContinue":true}],"willContinue":true}`, `{"partialArgs":[{"jsonPath":"$.a","stringValue":"z"},{"jsonPath":"$.b"}],"willContinue":false}`}, false},
		{"snake case nested array open", []string{`{"partial_args":[{"json_path":"$.items[1].a","string_value":"x","will_continue":true}],"will_continue":true}`, `{}`}, true},
		{"snake case nested array closed", []string{`{"partial_args":[{"json_path":"$.items[1].a","string_value":"x","will_continue":true}],"will_continue":true}`, `{"partial_args":[{"json_path":"$.items[1].a"}],"will_continue":false}`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assembler := NewVertexPartialArgsAssembler()
			for i, frame := range tc.frames {
				_, done := assembler.PushFunctionCall(gjson.Parse(frame))
				if assembler.Err() != nil {
					t.Fatalf("frame %d: %v", i, assembler.Err())
				}
				if done != (i == len(tc.frames)-1) {
					t.Fatalf("frame %d done=%v", i, done)
				}
			}
			if got := assembler.HasUnfinishedValueAtCompletion(); got != tc.unfinished {
				t.Fatalf("unfinished=%v, want %v", got, tc.unfinished)
			}
		})
	}
}
