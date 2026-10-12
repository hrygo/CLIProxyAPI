package helps

import (
	"bytes"

	"github.com/tidwall/gjson"
)

// HasResponsesTerminalEvent checks complete translated SSE records, not words
// in generated content or an event header without its response payload.
func HasResponsesTerminalEvent(chunk []byte) bool {
	var data []byte
	terminal := func() bool {
		if !gjson.ValidBytes(data) {
			return false
		}
		event := gjson.GetBytes(data, "type").String()
		status := gjson.GetBytes(data, "response.status").String()
		return (event == "response.completed" && status == "completed") ||
			(event == "response.incomplete" && status == "incomplete")
	}
	for _, line := range bytes.Split(chunk, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 {
			if terminal() {
				return true
			}
			data = nil
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, bytes.TrimPrefix(line[len("data:"):], []byte(" "))...)
		}
	}
	return terminal()
}
