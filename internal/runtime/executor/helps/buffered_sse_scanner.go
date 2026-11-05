package helps

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	translatorcommon "github.com/router-for-me/CLIProxyAPI/v8/internal/translator/common"
	"github.com/tidwall/gjson"
)

// BufferedSSEError reports a safe status without exposing upstream event data.
type BufferedSSEError struct {
	statusCode int
	reason     string
	retryAfter *time.Duration
}

func (e *BufferedSSEError) Error() string              { return e.reason }
func (e *BufferedSSEError) StatusCode() int            { return e.statusCode }
func (e *BufferedSSEError) RetryAfter() *time.Duration { return e.retryAfter }

// BufferedSSEScanner exposes unread bytes that Scanner has already obtained.
// It has the same single-goroutine usage requirements as bufio.Scanner.
type BufferedSSEScanner struct {
	*bufio.Scanner
	remaining   []byte
	current     bufferedSSEEvent
	currentOpen bool
	maxSize     int
}

func NewBufferedSSEScanner(reader io.Reader, maxTokenSize int) *BufferedSSEScanner {
	s := &BufferedSSEScanner{Scanner: bufio.NewScanner(reader), maxSize: maxTokenSize}
	s.Buffer(nil, maxTokenSize)
	s.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		advance, token, err := bufio.ScanLines(data, atEOF)
		if err == nil && token != nil {
			s.remaining = data[advance:]
			if len(token) == 0 {
				s.current = bufferedSSEEvent{}
				s.currentOpen = false
			} else {
				s.currentOpen = true
				s.current.addLine(token, s.maxSize)
			}
		}
		return advance, token, err
	})
	return s
}

// BufferedEventsAfterCurrent returns complete JSON SSE events following the
// current line's event, without reading from the underlying reader. The current
// event is validated if its separator is already buffered; an event whose last
// observed type is an error is rejected even before its separator arrives. The
// caller must invoke this before the next Scan. Other incomplete events are
// ignored. Malformed or error events return preceding valid events and an error;
// [DONE] ends the scan.
func (s *BufferedSSEScanner) BufferedEventsAfterCurrent() ([][]byte, error) {
	if s == nil {
		return nil, nil
	}
	remaining := s.remaining
	currentEvent := s.currentOpen
	event := s.current
	// Prevent appended snapshot data from modifying the scanner's tracked prefix.
	event.payload = event.payload[:len(event.payload):len(event.payload)]
	var events [][]byte
	for {
		end := bytes.IndexByte(remaining, '\n')
		if end < 0 {
			if event.eventType == bufferedSSEErrorEvent {
				return events, &BufferedSSEError{statusCode: http.StatusBadGateway, reason: "buffered SSE error event"}
			}
			return events, nil
		}
		line := bytes.TrimSuffix(remaining[:end], []byte("\r"))
		remaining = remaining[end+1:]
		if len(line) == 0 {
			if event.eventType == bufferedSSEErrorEvent {
				return events, event.error("buffered SSE error event")
			}
			if event.hasData {
				trimmed := bytes.TrimSpace(event.payload)
				if !event.overflow && bytes.Equal(trimmed, []byte("[DONE]")) {
					return events, nil
				}
				heartbeat := event.eventType == bufferedSSEHeartbeat
				ignoredHeartbeat := !event.overflow && heartbeat && (len(trimmed) == 0 || (!json.Valid(event.payload) && !looksLikeBufferedJSON(trimmed)))
				if !ignoredHeartbeat {
					if event.overflow || !validUniqueBufferedJSON(event.payload) {
						return events, &BufferedSSEError{statusCode: http.StatusBadGateway, reason: "invalid buffered SSE JSON event"}
					}
					if currentEvent && bufferedSSEPayloadIsError(event.payload) {
						return events, event.error("buffered SSE error event")
					}
					if !currentEvent {
						events = append(events, bytes.Clone(event.payload))
					}
				}
			}
			currentEvent = false
			event = bufferedSSEEvent{}
			continue
		}
		event.addLine(line, s.maxSize)
	}
}

type bufferedSSEEventType uint8

const (
	bufferedSSEOther bufferedSSEEventType = iota
	bufferedSSEErrorEvent
	bufferedSSEHeartbeat
)

type bufferedSSEEvent struct {
	payload   []byte
	eventType bufferedSSEEventType
	hasData   bool
	overflow  bool
}

func (e *bufferedSSEEvent) addLine(line []byte, limit int) {
	if len(line) == 0 || line[0] == ':' {
		return
	}
	field, value, _ := bytes.Cut(line, []byte(":"))
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	switch string(field) {
	case "event":
		switch string(value) {
		case "error", "response.error", "response.failed":
			e.eventType = bufferedSSEErrorEvent
		case "ping", "heartbeat", "keepalive":
			e.eventType = bufferedSSEHeartbeat
		default:
			e.eventType = bufferedSSEOther
		}
	case "data":
		separator := 0
		if e.hasData {
			separator = 1
		}
		e.hasData = true
		if e.overflow || (limit > 0 && len(value)+separator > limit-len(e.payload)) {
			e.overflow = true
			e.payload = nil
			return
		}
		if separator != 0 {
			e.payload = append(e.payload, '\n')
		}
		e.payload = append(e.payload, value...)
	}
}

func (e bufferedSSEEvent) error(reason string) *BufferedSSEError {
	result := &BufferedSSEError{statusCode: http.StatusBadGateway, reason: reason}
	if e.hasData && !e.overflow && validUniqueBufferedJSON(e.payload) {
		result.statusCode = bufferedSSEStatusCode(e.payload)
		if result.statusCode == http.StatusTooManyRequests {
			result.retryAfter = bufferedSSERetryAfter(e.payload)
		}
	}
	return result
}

func bufferedSSEPayloadIsError(payload []byte) bool {
	root := gjson.ParseBytes(payload)
	for _, path := range []string{"error", "response.error"} {
		if value := root.Get(path); value.Exists() && value.Type != gjson.Null {
			return true
		}
	}
	for _, prefix := range []string{"", "response."} {
		if (root.Get(prefix+"code").Exists() && root.Get(prefix+"message").Exists()) || root.Get(prefix+"type").String() == "error" {
			return true
		}
	}
	return false
}

func bufferedSSERetryAfter(payload []byte) *time.Duration {
	if delay, err := ParseRetryDelay(payload); err == nil && delay != nil {
		return delay
	}
	if nested := gjson.GetBytes(payload, "response.error"); nested.IsObject() {
		wrapped := append([]byte(`{"error":`), []byte(nested.Raw)...)
		wrapped = append(wrapped, '}')
		if delay, err := ParseRetryDelay(wrapped); err == nil {
			return delay
		}
	}
	return nil
}

func looksLikeBufferedJSON(data []byte) bool {
	return len(data) > 0 && bytes.ContainsRune([]byte(`{["-0123456789tfn`), rune(data[0]))
}

// JSON validation and duplicate-key detection share one interpretation of
// escaped keys. This prevents an earlier null from hiding a later error object.
func validUniqueBufferedJSON(data []byte) bool {
	if !json.Valid(data) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return uniqueBufferedJSONValue(decoder)
}

func uniqueBufferedJSONValue(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, errKey := decoder.Token()
			key, keyOK := keyToken.(string)
			if errKey != nil || !keyOK {
				return false
			}
			if _, duplicate := seen[key]; duplicate {
				return false
			}
			seen[key] = struct{}{}
			if !uniqueBufferedJSONValue(decoder) {
				return false
			}
		}
		end, errEnd := decoder.Token()
		return errEnd == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !uniqueBufferedJSONValue(decoder) {
				return false
			}
		}
		end, errEnd := decoder.Token()
		return errEnd == nil && end == json.Delim(']')
	default:
		return false
	}
}

func bufferedSSEStatusCode(payload []byte) int {
	root := gjson.ParseBytes(payload)
	for _, path := range []string{"error.code", "response.error.code", "code", "response.code"} {
		value := root.Get(path)
		if !value.Exists() {
			continue
		}
		if code, ok := translatorcommon.ParseGeminiUsageCount(value); ok && code >= http.StatusBadRequest && code <= 599 {
			return int(code)
		}
		return http.StatusBadGateway
	}
	return http.StatusBadGateway
}
