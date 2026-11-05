package helps

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

type bufferedSSETestReader struct {
	first []byte
	err   error
	reads int
}

type bufferedSSEChunkReader struct {
	remaining []byte
	reads     int
}

func (r *bufferedSSEChunkReader) Read(p []byte) (int, error) {
	r.reads++
	if len(r.remaining) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.remaining)
	r.remaining = r.remaining[n:]
	return n, nil
}

func (r *bufferedSSETestReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads != 1 {
		return 0, fmt.Errorf("unexpected subsequent read")
	}
	if len(p) < len(r.first) {
		return 0, io.ErrShortBuffer
	}
	return copy(p, r.first), r.err
}

func TestBufferedSSEScannerCompleteEventsWithoutAnotherRead(t *testing.T) {
	const wire = "data: {\"finishReason\":\"STOP\"}\r\n\r\n" +
		": ignored comment\r\nevent: message\r\ndata: {\"response\":\r\ndata: {\"usageMetadata\":{\"totalTokenCount\":10}}}\r\n\r\n" +
		"data: {\"thoughtSignature\":\"signed\"}\n\n" +
		"data: {\"truncated\":\n"
	r := &bufferedSSETestReader{first: []byte(wire)}
	scanner := NewBufferedSSEScanner(r, 4096)
	if !scanner.Scan() {
		t.Fatalf("finish line not read: %v", scanner.Err())
	}
	events, err := scanner.BufferedEventsAfterCurrent()
	if err != nil {
		t.Fatalf("buffered events: %v", err)
	}
	if r.reads != 1 || len(events) != 2 {
		t.Fatalf("reads=%d, events=%q; want one read and two complete events", r.reads, events)
	}
	if !gjson.ValidBytes(events[0]) || gjson.GetBytes(events[0], "response.usageMetadata.totalTokenCount").Int() != 10 ||
		gjson.GetBytes(events[1], "thoughtSignature").String() != "signed" {
		t.Fatalf("unexpected events: %q", events)
	}
	if bytes.Contains(events[1], []byte("truncated")) {
		t.Fatalf("partial event was included: %q", events)
	}
}

func TestBufferedSSEScannerRequiresCompleteSeparators(t *testing.T) {
	cases := []struct {
		name, suffix string
		want         int
	}{
		{"current_event_not_closed", ": comment\n\n", 0},
		{"tail_missing_blank_line", "\ndata: {\"tail\":1}\n", 0},
		{"tail_complete", "\ndata: {\"tail\":1}\n\n", 1},
		{"tail_not_yet_read", "\n", 0},
		{"done_stops", "\ndata: {\"first\":1}\n\ndata: [DONE]\n\ndata: {\"later\":2}\n\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &bufferedSSETestReader{first: []byte("data: {\"finishReason\":\"STOP\"}\n" + tc.suffix)}
			scanner := NewBufferedSSEScanner(r, 4096)
			if !scanner.Scan() {
				t.Fatal(scanner.Err())
			}
			events, err := scanner.BufferedEventsAfterCurrent()
			if err != nil || len(events) != tc.want || r.reads != 1 {
				t.Fatalf("events=%q, error=%v, reads=%d", events, err, r.reads)
			}
		})
	}
}

func TestBufferedSSEScannerValidatesCurrentEvent(t *testing.T) {
	const finish = `data: {"finishReason":"STOP"}` + "\n"
	const tail = "\ndata: {\"usage\":1}\n\n"
	for _, tc := range []struct {
		name, wire string
		scans      int
		wantStatus int
		wantEvents int
	}{
		{name: "valid_event", wire: finish + tail, scans: 1, wantEvents: 1},
		{name: "valid_multiline_event", wire: "data: {\"candidates\":[\n" + `data: {"finishReason":"STOP"}]}` + "\n" + tail, scans: 2, wantEvents: 1},
		{name: "invalid_extra_data", wire: finish + `data: {"error":{"code":429}}` + "\n\n", scans: 1, wantStatus: 502},
		{name: "duplicate_current_json", wire: `data: {"finishReason":"STOP","error":null,"\u0065rror":{"code":429}}` + "\n\n", scans: 1, wantStatus: 502},
		{name: "duplicate_in_multiline_current_json", wire: "data: {\"error\":null,\n" + `data: "\u0065rror":{"code":429},"finishReason":"STOP"}` + "\n\n", scans: 2, wantStatus: 502},
		{name: "nested_error_in_current_json", wire: `data: {"finishReason":"STOP","response":{"error":{"code":429}}}` + "\n\n", scans: 1, wantStatus: 429},
		{name: "code_message_in_current_json", wire: `data: {"finishReason":"STOP","code":503,"message":"private"}` + "\n\n", scans: 1, wantStatus: 503},
		{name: "error_before_finish", wire: "event: error\n" + `data: {"finishReason":"STOP","code":429}` + "\n\n", scans: 2, wantStatus: 429},
		{name: "error_after_finish", wire: `data: {"finishReason":"STOP","code":429}` + "\nevent: error\n\n", scans: 1, wantStatus: 429},
		{name: "error_overridden_before_finish", wire: "event: error\nevent: message\n" + finish + tail, scans: 3, wantEvents: 1},
		{name: "message_overridden_before_finish", wire: "event: message\nevent: error\n" + finish + "\n", scans: 3, wantStatus: 502},
		{name: "error_overridden_after_finish", wire: finish + "event: error\nevent: message\n" + tail, scans: 1, wantEvents: 1},
		{name: "message_overridden_after_finish", wire: finish + "event: message\nevent: error\n\n", scans: 1, wantStatus: 502},
		{name: "error_overridden_incomplete", wire: finish + "event: error\nevent: message\n", scans: 1},
		{name: "message_overridden_incomplete", wire: finish + "event: message\nevent: error\n", scans: 1, wantStatus: 502},
		{name: "incomplete_error_before_finish", wire: "event: response.failed\n" + finish, scans: 2, wantStatus: 502},
		{name: "incomplete_error_after_finish", wire: finish + "event: error\n", scans: 1, wantStatus: 502},
		{name: "incomplete_invalid_combination", wire: finish + `data: {"error":{"code":429}}` + "\n", scans: 1},
		{name: "incomplete_error_line", wire: finish + "event: error", scans: 1},
		{name: "crlf_error_prefix", wire: "event: response.error\r\n" + `data: {"finishReason":"STOP","code":403}` + "\r\n\r\n", scans: 2, wantStatus: 403},
		{name: "unknown_event_prefix", wire: "event: custom\n" + finish + tail, scans: 2, wantEvents: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &bufferedSSETestReader{first: []byte(tc.wire)}
			scanner := NewBufferedSSEScanner(r, 4096)
			for range tc.scans {
				if !scanner.Scan() {
					t.Fatalf("scan failed: %v", scanner.Err())
				}
			}
			events, err := scanner.BufferedEventsAfterCurrent()
			if tc.wantStatus == 0 && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantStatus != 0 {
				status, ok := err.(interface{ StatusCode() int })
				if !ok || status.StatusCode() != tc.wantStatus {
					t.Fatalf("error = %v, want status %d", err, tc.wantStatus)
				}
			}
			if len(events) != tc.wantEvents || r.reads != 1 {
				t.Fatalf("events=%q, reads=%d; want %d events after one read", events, r.reads, tc.wantEvents)
			}
		})
	}
}

func TestBufferedSSEScannerCurrentPrefixIsBoundedAndResets(t *testing.T) {
	t.Run("previous_event_does_not_contaminate_current", func(t *testing.T) {
		r := &bufferedSSETestReader{first: []byte("data: {\"previous\":true}\n\ndata: {\"finishReason\":\"STOP\"}\n\n")}
		scanner := NewBufferedSSEScanner(r, 4096)
		for range 3 {
			if !scanner.Scan() {
				t.Fatal(scanner.Err())
			}
		}
		events, err := scanner.BufferedEventsAfterCurrent()
		if err != nil || len(events) != 0 || r.reads != 1 {
			t.Fatalf("events=%q, error=%v, reads=%d", events, err, r.reads)
		}
	})
	for _, tc := range []struct {
		name, suffix string
		wantError    bool
	}{
		{name: "complete", suffix: "\n", wantError: true},
		{name: "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := `data: {"padding":"` + strings.Repeat("a", 30) + "\",\n" +
				`data: "padding2":"` + strings.Repeat("b", 30) + "\",\n" +
				`data: "finishReason":"STOP"}` + "\n" + tc.suffix
			r := &bufferedSSEChunkReader{remaining: []byte(wire)}
			scanner := NewBufferedSSEScanner(r, 64)
			for range 3 {
				if !scanner.Scan() {
					t.Fatal(scanner.Err())
				}
			}
			readsBefore := r.reads
			events, err := scanner.BufferedEventsAfterCurrent()
			if (err != nil) != tc.wantError || len(events) != 0 || r.reads != readsBefore {
				t.Fatalf("events=%q, error=%v, reads=%d before=%d", events, err, r.reads, readsBefore)
			}
		})
	}
}

func TestBufferedSSEScannerRejectsMalformedCompleteEvent(t *testing.T) {
	r := &bufferedSSETestReader{first: []byte("data: {}\n\ndata: {\"bad\":}\n\ndata: {\"after\":true}\n\n")}
	scanner := NewBufferedSSEScanner(r, 4096)
	if !scanner.Scan() {
		t.Fatal(scanner.Err())
	}
	events, err := scanner.BufferedEventsAfterCurrent()
	if err == nil || len(events) != 0 || r.reads != 1 {
		t.Fatalf("events=%q, error=%v, reads=%d", events, err, r.reads)
	}
}

func TestBufferedSSEScannerReturnsValidPrefixBeforeMalformedEvent(t *testing.T) {
	r := &bufferedSSETestReader{first: []byte("data: {}\n\ndata: {\"usage\":1}\n\ndata: {\"bad\":}\n\ndata: {\"after\":true}\n\n")}
	scanner := NewBufferedSSEScanner(r, 4096)
	if !scanner.Scan() {
		t.Fatal(scanner.Err())
	}
	events, err := scanner.BufferedEventsAfterCurrent()
	if err == nil || len(events) != 1 || gjson.GetBytes(events[0], "usage").Int() != 1 || r.reads != 1 {
		t.Fatalf("events=%q, error=%v, reads=%d", events, err, r.reads)
	}
}

func TestBufferedSSEScannerRecognizesErrorEventWithoutPayloadLeak(t *testing.T) {
	for _, name := range []string{"error", "response.error", "response.failed"} {
		for _, data := range []string{"", "data: {\"message\":\"private quota details\"}\n"} {
			t.Run(name+"/"+fmt.Sprint(data != ""), func(t *testing.T) {
				r := &bufferedSSETestReader{first: []byte("data: {}\n\ndata: {\"usage\":1}\n\nevent: " + name + "\n" + data + "\ndata: {\"after\":true}\n\n")}
				scanner := NewBufferedSSEScanner(r, 4096)
				if !scanner.Scan() {
					t.Fatal(scanner.Err())
				}
				events, err := scanner.BufferedEventsAfterCurrent()
				if err == nil || len(events) != 1 || gjson.GetBytes(events[0], "usage").Int() != 1 || r.reads != 1 {
					t.Fatalf("events=%q, error=%v, reads=%d", events, err, r.reads)
				}
				if strings.Contains(err.Error(), "private quota details") {
					t.Fatalf("error exposed event data: %v", err)
				}
			})
		}
	}
}

func TestBufferedSSEScannerRejectsDuplicateJSONKeys(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
	}{
		{"duplicate_error", `{"error":null,"error":{"code":429}}`},
		{"nested_duplicate", `{"response":{"error":null,"error":{"code":429}}}`},
		{"escaped_equivalent", `{"error":null,"\u0065rror":{"code":429}}`},
		{"duplicate_in_array", `{"items":[{"secret":"first","secret":"second"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &bufferedSSETestReader{first: []byte("data: {}\n\ndata: {\"usage\":1}\n\ndata: " + tc.payload + "\n\n")}
			scanner := NewBufferedSSEScanner(r, 4096)
			if !scanner.Scan() {
				t.Fatal(scanner.Err())
			}
			events, err := scanner.BufferedEventsAfterCurrent()
			if err == nil || len(events) != 1 || gjson.GetBytes(events[0], "usage").Int() != 1 || r.reads != 1 {
				t.Fatalf("events=%q, error=%v, reads=%d", events, err, r.reads)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "429") {
				t.Fatalf("error exposed event data: %v", err)
			}
		})
	}
}

func TestBufferedSSEScannerRecognizesHeartbeatEvents(t *testing.T) {
	for _, name := range []string{"ping", "heartbeat", "keepalive"} {
		t.Run(name, func(t *testing.T) {
			wire := "data: {}\n\ndata: {\"usage\":1}\n\n" +
				"event: " + name + "\ndata: ping\n\n" +
				"event: " + name + "\ndata:\n\n" +
				"event: " + name + "\n\n" +
				"event: " + name + "\ndata: {\"error\":{\"code\":429}}\n\n"
			r := &bufferedSSETestReader{first: []byte(wire)}
			scanner := NewBufferedSSEScanner(r, 4096)
			if !scanner.Scan() {
				t.Fatal(scanner.Err())
			}
			events, err := scanner.BufferedEventsAfterCurrent()
			if err != nil || len(events) != 2 || gjson.GetBytes(events[1], "error.code").Int() != 429 || r.reads != 1 {
				t.Fatalf("events=%q, error=%v, reads=%d", events, err, r.reads)
			}
		})
	}
}

func TestBufferedSSEScannerRejectsMalformedAndDuplicateHeartbeatJSON(t *testing.T) {
	for _, payload := range []string{`{"error":`, `{"error":null,"error":{"code":429}}`} {
		r := &bufferedSSETestReader{first: []byte("data: {}\n\ndata: {\"usage\":1}\n\nevent: ping\ndata: " + payload + "\n\n")}
		scanner := NewBufferedSSEScanner(r, 4096)
		if !scanner.Scan() {
			t.Fatal(scanner.Err())
		}
		events, err := scanner.BufferedEventsAfterCurrent()
		if err == nil || len(events) != 1 || gjson.GetBytes(events[0], "usage").Int() != 1 || r.reads != 1 {
			t.Fatalf("events=%q, error=%v, reads=%d", events, err, r.reads)
		}
	}
}

func TestBufferedSSEScannerRejectsUnknownNonJSONEvent(t *testing.T) {
	r := &bufferedSSETestReader{first: []byte("data: {}\n\nevent: custom\ndata: ping\n\n")}
	scanner := NewBufferedSSEScanner(r, 4096)
	if !scanner.Scan() {
		t.Fatal(scanner.Err())
	}
	if _, err := scanner.BufferedEventsAfterCurrent(); err == nil || r.reads != 1 {
		t.Fatalf("error=%v, reads=%d", err, r.reads)
	}
}

func TestBufferedSSEScannerErrorEventPreservesSafeStatus(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       int
	}{
		{"nested_code", `data: {"error":{"code":429,"message":"private quota details"}}` + "\n", 429},
		{"wrapped_code", `data: {"response":{"error":{"code":403}}}` + "\n", 403},
		{"direct_code", `data: {"code":503,"message":"private quota details"}` + "\n", 503},
		{"invalid_code", `data: {"error":{"code":200}}` + "\n", 502},
		{"fractional_code", `data: {"error":{"code":429.5}}` + "\n", 502},
		{"precise_fractional_code", `data: {"error":{"code":429.00000000000000001}}` + "\n", 502},
		{"no_data", "", 502},
		{"malformed", `data: {"error":` + "\n", 502},
		{"duplicate", `data: {"error":null,"error":{"code":429}}` + "\n", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &bufferedSSETestReader{first: []byte("data: {}\n\ndata: {\"usage\":1}\n\nevent: error\n" + tc.data + "\n")}
			scanner := NewBufferedSSEScanner(r, 4096)
			if !scanner.Scan() {
				t.Fatal(scanner.Err())
			}
			events, err := scanner.BufferedEventsAfterCurrent()
			status, ok := err.(interface{ StatusCode() int })
			if err == nil || !ok || status.StatusCode() != tc.want || len(events) != 1 || r.reads != 1 {
				t.Fatalf("events=%q, error=%v, status=%v, reads=%d", events, err, status, r.reads)
			}
			if strings.Contains(err.Error(), "private quota details") {
				t.Fatalf("error exposed event data: %v", err)
			}
		})
	}
}

func TestBufferedSSEScannerErrorRetryAfterIsSafe(t *testing.T) {
	const retry = `"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"3s"}]`
	for _, tc := range []struct {
		name, payload string
		wantRetry     bool
	}{
		{name: "error", payload: `{"error":{"code":429,` + retry + `},"access_token":"synthetic-sensitive-token"}`, wantRetry: true},
		{name: "response_error", payload: `{"response":{"error":{"code":429,` + retry + `}},"debug":"synthetic-sensitive-debug"}`, wantRetry: true},
		{name: "bad_retry", payload: `{"error":{"code":429,"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"bad"}]}}`},
		{name: "other_status", payload: `{"error":{"code":503,` + retry + `}}`},
		{name: "duplicate_keys", payload: `{"error":{"code":429,` + retry + `},"error":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &bufferedSSETestReader{first: []byte("data: {}\n\nevent: error\ndata: " + tc.payload + "\n\n")}
			scanner := NewBufferedSSEScanner(r, 4096)
			if !scanner.Scan() {
				t.Fatal(scanner.Err())
			}
			_, err := scanner.BufferedEventsAfterCurrent()
			retryErr, ok := err.(interface{ RetryAfter() *time.Duration })
			if !ok || (retryErr.RetryAfter() != nil) != tc.wantRetry {
				t.Fatalf("error=%v, retry provider=%v, want retry=%t", err, ok, tc.wantRetry)
			}
			if tc.wantRetry && *retryErr.RetryAfter() != 3*time.Second {
				t.Fatalf("retry delay=%v, want 3s", *retryErr.RetryAfter())
			}
			if strings.Contains(err.Error(), "synthetic-sensitive") || r.reads != 1 {
				t.Fatalf("error=%v, reads=%d", err, r.reads)
			}
		})
	}
}

func TestBufferedSSEScannerExposesAlreadyReturnedReadError(t *testing.T) {
	want := errors.New("upstream read failed")
	r := &bufferedSSETestReader{first: []byte("data: {}\n\ndata: {\"usage\":1}\n\n"), err: want}
	scanner := NewBufferedSSEScanner(r, 4096)
	if !scanner.Scan() || !errors.Is(scanner.Err(), want) || r.reads != 1 {
		t.Fatalf("error=%v, reads=%d", scanner.Err(), r.reads)
	}
	events, err := scanner.BufferedEventsAfterCurrent()
	if err != nil || len(events) != 1 || !strings.Contains(string(events[0]), "usage") || r.reads != 1 {
		t.Fatalf("events=%q, error=%v, reads=%d", events, err, r.reads)
	}
}
