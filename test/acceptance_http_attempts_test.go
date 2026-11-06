package test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/acceptance"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
)

// This is real gateway HTTP with a deterministic local upstream, not a live
// Provider sample. The optional export is private and never overwrites a run.
func TestAcceptanceHTTPJournalThroughResponsesHandler(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "buffered"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			turn := strings.Replace(responsesToolsSSECompletedMessage("fixture answer"), `"status":"completed","output":`, `"status":"completed","usage":{"input_tokens":31,"output_tokens":2},"output":`, 1)
			h, _ := newResponsesToolsIntegrationHarness(t, "acceptance-fixture-model", []string{turn})
			dir := filepath.Join(t.TempDir(), "http-evidence")
			if export := os.Getenv("TOOL_LOADING_TEST_HTTP_EVIDENCE_DIR"); export != "" && stream {
				dir = export
			}
			journal, err := acceptance.NewHTTPAttemptJournal(dir, "openai-responses-v1", 1<<20, 4)
			if err != nil {
				t.Fatal(err)
			}
			body := responsesToolsDeferredBody(h.model)
			if stream {
				body = append(body[:len(body)-1], []byte(`,"stream":true}`)...)
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			c.Request = c.Request.WithContext(helps.WithHTTPAttemptObserver(context.Background(), journal.Observe))
			c.Request.Header.Set("Content-Type", "application/json")
			h.handler.Responses(c)
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "fixture answer") {
				t.Fatalf("gateway response = %d, %s", recorder.Code, recorder.Body.String())
			}
			if stream && !strings.Contains(recorder.Body.String(), "response.completed") {
				t.Fatal("missing actual protocol completion")
			}
			if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(filepath.Join(dir, "attempts.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(file)
			var events []map[string]any
			for scanner.Scan() {
				var event map[string]any
				if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
					t.Fatal(err)
				}
				events = append(events, event)
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if len(events) != 2 || events[1]["outcome"] != "eof" {
				t.Fatalf("gateway attempts not captured: %+v", events)
			}
			request, err := os.ReadFile(filepath.Join(dir, "request-000001.body"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(request), strings.Repeat("x", 2048)) || strings.Contains(string(request), `"type":"tool_search"`) {
				t.Fatal("collector did not capture the translated deferred outbound request")
			}
			response, err := os.ReadFile(filepath.Join(dir, "response-000001.body"))
			if err != nil || !strings.Contains(string(response), `"input_tokens":31`) {
				t.Fatal("raw upstream usage was not captured")
			}
		})
	}
}
