package helps

import "testing"

func TestHasResponsesTerminalEvent(t *testing.T) {
	for _, tc := range []struct {
		name, record string
		want         bool
	}{
		{"completed", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", true},
		{"incomplete", "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\"}}\n\n", true},
		{"multiple_records", "data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", true},
		{"multiline_crlf", "data: {\"type\":\"response.completed\",\r\ndata: \"response\":{\"status\":\"completed\"}}\r\n\r\n", true},
		{"content_mentions_completion", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"response.completed\"}\n\n", false},
		{"header_only", "event: response.completed\n\n", false},
		{"wrong_status", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"in_progress\"}}\n\n", false},
		{"missing_status", "data: {\"type\":\"response.completed\",\"response\":{}}\n\n", false},
		{"partial_json", "data: {\"type\":\"response.completed\",\"response\":\n\n", false},
		{"transport_done", "data: [DONE]\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasResponsesTerminalEvent([]byte(tc.record)); got != tc.want {
				t.Fatalf("HasResponsesTerminalEvent = %v, want %v", got, tc.want)
			}
		})
	}
}
