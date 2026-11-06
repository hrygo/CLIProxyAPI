package openai

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gjson "github.com/MoeForge/moego/ojson"
	"github.com/gorilla/websocket"
)

func TestResponsesWebsocketHomeAppendBeforeCreateIsLocalError(t *testing.T) {
	for _, mode := range []string{"native", "bridge"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			frames := make(chan []byte, 1)
			errs := make(chan error, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					errs <- err
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
				_, frame, errRead := conn.ReadMessage()
				if errRead != nil {
					errs <- errRead
					return
				}
				frames <- bytes.Clone(frame)
				if errWrite := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"after-invalid-append","status":"completed","output":[]}}`)); errWrite != nil {
					errs <- errWrite
					return
				}
				_, _, _ = conn.ReadMessage()
			}))
			t.Cleanup(upstream.Close)
			conn, dispatcher, _ := homeToolsTransportClient(t, upstream.URL, mode)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.append","model":"home-client-alias","input":[{"role":"user","content":"DENIED_EARLY_APPEND"}]}`)); err != nil {
				t.Fatal(err)
			}
			_, payload, err := conn.ReadMessage()
			if err != nil || gjson.GetBytes(payload, "type").String() != "error" || gjson.GetBytes(payload, "status").Int() != http.StatusBadRequest {
				t.Fatalf("early append result=%s err=%v, want 400", payload, err)
			}
			if got := dispatcher.calls.Load(); got != 0 {
				t.Fatalf("early append made %d Home selections", got)
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf("early append made %d upstream requests", got)
			}
			if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"home-client-alias","tools":[{"type":"tool_search"}],"input":[{"role":"user","content":"valid-create"}]}`)); err != nil {
				t.Fatal(err)
			}
			_, payload, err = conn.ReadMessage()
			if err != nil || gjson.GetBytes(payload, "type").String() != "response.completed" || gjson.GetBytes(payload, "response.id").String() != "after-invalid-append" {
				t.Fatalf("create after invalid append=%s err=%v", payload, err)
			}
			select {
			case frame := <-frames:
				if bytes.Contains(frame, []byte("DENIED_EARLY_APPEND")) || !bytes.Contains(frame, []byte("valid-create")) {
					t.Fatalf("invalid append polluted the first upstream frame: %s", frame)
				}
			case err := <-errs:
				t.Fatal(err)
			case <-time.After(8 * time.Second):
				t.Fatal("no upstream frame after valid create")
			}
			if got := dispatcher.calls.Load(); got != 1 {
				t.Errorf("Home selections=%d after valid create, want 1", got)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("upstream requests=%d after valid create, want 1", got)
			}
		})
	}
}

// Exercise the dispatcher-selected route and the upstream wire, including a
// denied frame between successful turns. The endpoint is local to the test.
func TestResponsesWebsocketHomeAppendAndOmittedTools(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        string
		resendTools bool
	}{
		{name: "native_omitted", mode: "native"},
		{name: "native_resent", mode: "native", resendTools: true},
		{name: "bridge_omitted", mode: "bridge"},
		{name: "bridge_resent", mode: "bridge", resendTools: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := make(chan []byte, 8)
			errs := make(chan error, 1)
			done := make(chan struct{})
			releaseFirst := make(chan struct{})
			var connections atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if connections.Add(1) != 1 {
					select {
					case errs <- fmt.Errorf("unexpected second upstream connection"):
					default:
					}
					http.Error(w, "unexpected upstream connection", http.StatusConflict)
					return
				}
				defer close(done)
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					errs <- err
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				for turn := 1; ; turn++ {
					_, frame, errRead := conn.ReadMessage()
					if errRead != nil {
						return
					}
					frames <- bytes.Clone(frame)
					created := fmt.Sprintf(`{"type":"response.created","response":{"id":"home-append-%d","output":[]}}`, turn)
					if errWrite := conn.WriteMessage(websocket.TextMessage, []byte(created)); errWrite != nil {
						errs <- errWrite
						return
					}
					if turn == 1 {
						<-releaseFirst
					}
					completed := fmt.Sprintf(`{"type":"response.completed","response":{"id":"home-append-%d","status":"completed","output":[{"type":"message","id":"assistant-%d","role":"assistant","content":[{"type":"output_text","text":"answer-%d","annotations":[]}]}]}}`, turn, turn, turn)
					if errWrite := conn.WriteMessage(websocket.TextMessage, []byte(completed)); errWrite != nil {
						errs <- errWrite
						return
					}
				}
			}))
			t.Cleanup(upstream.Close)
			released := false
			defer func() {
				if !released {
					close(releaseFirst)
				}
			}()
			conn, dispatcher, access := homeToolsTransportClient(t, upstream.URL, tc.mode)
			write := func(payload string) {
				t.Helper()
				if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
					t.Fatalf("send %s: %v", payload, err)
				}
			}
			readType := func(want string) []byte {
				t.Helper()
				_, payload, err := conn.ReadMessage()
				if err != nil || gjson.GetBytes(payload, "type").String() != want {
					t.Fatalf("event=%s err=%v, want %s", payload, err, want)
				}
				return payload
			}
			readForbidden := func() {
				t.Helper()
				payload := readType("error")
				if status := gjson.GetBytes(payload, "status").Int(); status != http.StatusForbidden {
					t.Fatalf("revoked frame status=%d, want 403: %s", status, payload)
				}
			}
			readTurnEvent := func(wantType string, turn int) {
				t.Helper()
				payload := readType(wantType)
				wantID := fmt.Sprintf("home-append-%d", turn)
				if got := gjson.GetBytes(payload, "response.id").String(); got != wantID {
					t.Fatalf("event %s response.id=%q, want %q: %s", wantType, got, wantID, payload)
				}
			}
			write(`{"type":"response.create","model":"home-client-alias","instructions":"remember these instructions","tools":[{"type":"tool_search"},{"type":"function","name":"lookup","parameters":{"type":"object","properties":{}}}],"input":[{"type":"message","id":"first-user","role":"user","content":"first"}]}`)
			readTurnEvent("response.created", 1)
			access.revoked.Store(true)
			write(`{"type":"response.steer","previous_response_id":"home-append-1","input":"DENIED_LIVE_STEER"}`)
			if tc.mode == "native" {
				readForbidden()
			}
			write(`{"type":"response.interrupt","response_id":"home-append-1","event_id":"DENIED_LIVE_INTERRUPT"}`)
			readForbidden()
			close(releaseFirst)
			released = true
			readTurnEvent("response.completed", 1)
			if tc.mode == "bridge" {
				readForbidden()
			}
			for _, denied := range []string{
				`{"type":"response.append","input":[{"type":"message","id":"DENIED_APPEND","role":"user","content":"denied"}]}`,
				`{"type":"response.create","previous_response_id":"home-append-1","input":[{"type":"message","id":"DENIED_CREATE","role":"user","content":"denied"}]}`,
				`{"type":"response.steer","previous_response_id":"home-append-1","input":"DENIED_STEER"}`,
				`{"type":"response.interrupt","response_id":"home-append-1","event_id":"DENIED_INTERRUPT"}`,
			} {
				write(denied)
				readForbidden()
			}
			access.revoked.Store(false)
			toolsField := ""
			if tc.resendTools {
				toolsField = `,"tools":[{"type":"tool_search"},{"type":"function","name":"lookup","parameters":{"type":"object","properties":{}}}]`
			}
			write(`{"type":"response.append","input":[{"type":"message","id":"second-user","role":"user","content":"second"}]` + toolsField + `}`)
			readTurnEvent("response.created", 2)
			readTurnEvent("response.completed", 2)
			write(`{"type":"response.create","previous_response_id":"home-append-2","input":[{"type":"message","id":"third-user","role":"user","content":"third"}]` + toolsField + `}`)
			readTurnEvent("response.created", 3)
			readTurnEvent("response.completed", 3)
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("upstream did not close")
			}
			select {
			case err := <-errs:
				t.Fatal(err)
			default:
			}
			if got := dispatcher.calls.Load(); got != 1 {
				t.Errorf("Home selections=%d, want one retained session selection", got)
			}
			if got := connections.Load(); got != 1 {
				t.Errorf("upstream connections=%d, want 1", got)
			}
			close(frames)
			var received [][]byte
			for frame := range frames {
				received = append(received, frame)
				if bytes.Contains(frame, []byte("DENIED_")) {
					t.Errorf("revoked frame reached upstream: %s", frame)
				}
			}
			if len(received) != 3 {
				t.Fatalf("upstream frames=%d, want 3", len(received))
			}
			for i, frame := range received {
				if got := gjson.GetBytes(frame, "type").String(); got != "response.create" {
					t.Errorf("turn %d upstream event=%q: %s", i+1, got, frame)
				}
				if got := gjson.GetBytes(frame, "model").String(); got != "home-upstream-model" {
					t.Errorf("turn %d upstream model=%q: %s", i+1, got, frame)
				}
				wantSearch := "tool_search"
				if tc.mode == "bridge" {
					wantSearch = "function"
				}
				if i == 0 || tc.resendTools {
					if got := gjson.GetBytes(frame, "tools.0.type").String(); got != wantSearch {
						t.Errorf("turn %d search declaration=%q, want %q: %s", i+1, got, wantSearch, frame)
					}
					if got := gjson.GetBytes(frame, "tools.1.name").String(); got != "lookup" {
						t.Errorf("turn %d lookup declaration=%q: %s", i+1, got, frame)
					}
				} else {
					// An omitted declaration is not implicitly inherited. The executor
					// may still add its own independent tools to the outbound request.
					searchName := gjson.GetBytes(received[0], "tools.0.name").String()
					for _, tool := range gjson.GetBytes(frame, "tools").Array() {
						if tool.Get("type").String() == "tool_search" || tool.Get("name").String() == "lookup" ||
							(searchName != "" && tool.Get("name").String() == searchName) {
							t.Errorf("turn %d unexpectedly inherited a declaration: %s", i+1, frame)
						}
					}
				}
			}
			for _, i := range []int{0, 1} {
				if got := gjson.GetBytes(received[i], "instructions").String(); got != "remember these instructions" {
					t.Errorf("turn %d instructions=%q: %s", i+1, got, received[i])
				}
			}
			wantInputs := [][]string{{"first-user"}, {"second-user"}, {"third-user"}}
			if tc.mode == "bridge" {
				wantInputs[1] = []string{"first-user", "assistant-1", "second-user"}
				if tc.resendTools {
					wantInputs[2] = []string{"first-user", "assistant-1", "second-user", "assistant-2", "third-user"}
				}
			}
			for i := 1; i < len(received); i++ {
				if tc.mode == "native" || (i == 2 && !tc.resendTools) {
					want := fmt.Sprintf("home-append-%d", i)
					if got := gjson.GetBytes(received[i], "previous_response_id").String(); got != want {
						t.Errorf("turn %d parent=%q, want %q: %s", i+1, got, want, received[i])
					}
				} else if gjson.GetBytes(received[i], "previous_response_id").Exists() {
					t.Errorf("turn %d retained an upstream parent in replay mode: %s", i+1, received[i])
				}
			}
			for i, frame := range received {
				items := gjson.GetBytes(frame, "input").Array()
				if len(items) != len(wantInputs[i]) {
					t.Errorf("turn %d input length=%d, want %d: %s", i+1, len(items), len(wantInputs[i]), frame)
					continue
				}
				for j, item := range items {
					if got := strings.TrimPrefix(item.Get("id").String(), "msg_"); got != wantInputs[i][j] {
						t.Errorf("turn %d input[%d].id=%q, want %q: %s", i+1, j, got, wantInputs[i][j], frame)
					}
				}
			}
			access.mu.Lock()
			defer access.mu.Unlock()
			seen := make(map[string]int)
			markers := []string{"first-user", "second-user", "third-user", "DENIED_LIVE_STEER", "DENIED_LIVE_INTERRUPT", "DENIED_APPEND", "DENIED_CREATE", "DENIED_STEER", "DENIED_INTERRUPT"}
			for _, body := range access.seenBodies {
				for _, marker := range markers {
					if bytes.Contains(body, []byte(marker)) {
						seen[marker]++
						if gjson.GetBytes(body, "model").String() != "home-client-alias" {
							t.Errorf("authorization did not use client model for %s: %s", marker, body)
						}
					}
				}
			}
			for _, marker := range markers {
				if seen[marker] != 1 {
					t.Errorf("authorization saw %s %d times, want 1", marker, seen[marker])
				}
			}
		})
	}
}
