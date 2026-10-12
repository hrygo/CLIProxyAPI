// Package acceptance provides opt-in evidence capture for isolated fixture runs.
// It is never enabled by the production server or configuration loader.
package acceptance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

type bodyArtifact struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Bytes    int64  `json:"bytes"`
	Complete bool   `json:"complete"`
}

// HTTPAttemptJournal records starts durably before dispatch, preserving one
// artifact per physical HTTP attempt. Only synthetic request/response bodies
// may be captured: headers, URLs and error messages are deliberately excluded.
// EOF means the body was read, not that a protocol's legal terminal was present.
type HTTPAttemptJournal struct {
	mu          sync.Mutex
	dir         string
	contract    string
	maxBytes    int64
	maxAttempts int
	attempts    int
	active      int
	sequence    int
	file        *os.File
	closed      bool
	err         error
}

// NewHTTPAttemptJournal requires a new absolute directory and explicit budgets.
// The caller must pin the actual outbound protocol for this scoped run.
func NewHTTPAttemptJournal(dir, contract string, maxBytes int64, maxAttempts int) (*HTTPAttemptJournal, error) {
	switch contract {
	case "openai-responses-v1", "openai-chat-v1", "anthropic-messages-v1", "gemini-generate-v1":
	default:
		return nil, errors.New("unsupported HTTP evidence contract")
	}
	if !filepath.IsAbs(dir) || maxBytes < 1 || maxBytes > 64<<20 || maxAttempts < 1 || maxAttempts > 10000 {
		return nil, errors.New("invalid HTTP evidence directory or budget")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, fmt.Errorf("create HTTP evidence directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, "attempts.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("create HTTP evidence journal: %w", err)
	}
	return &HTTPAttemptJournal{dir: dir, contract: contract, maxBytes: maxBytes, maxAttempts: maxAttempts, file: file}, nil
}

func (j *HTTPAttemptJournal) appendLocked(event map[string]any) error {
	j.sequence++
	event["sequence"] = j.sequence
	event["version"] = 1
	data, err := json.Marshal(event)
	if err == nil {
		_, err = j.file.Write(append(data, '\n'))
	}
	if err == nil {
		err = j.file.Sync()
	}
	if err != nil {
		j.err = errors.Join(j.err, err)
	}
	return err
}

// Observe is compatible with helps.HTTPAttemptObserver. Capture setup failures
// fail before dispatch. Later capture errors are available through Close, and
// never silently become complete usage evidence.
func (j *HTTPAttemptJournal) Observe(req *http.Request, next http.RoundTripper) (*http.Response, error) {
	if req == nil || next == nil {
		return nil, errors.New("missing HTTP evidence request or transport")
	}
	var request []byte
	if req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			return nil, errors.New("HTTP evidence requires a replayable request body")
		}
		body, err := req.GetBody()
		if err != nil || body == nil {
			return nil, errors.New("HTTP evidence could not copy request body")
		}
		request, err = io.ReadAll(io.LimitReader(body, j.maxBytes+1))
		errClose := body.Close()
		if err != nil || errClose != nil || int64(len(request)) > j.maxBytes {
			return nil, errors.New("HTTP evidence request capture failed or exceeded budget")
		}
	}
	j.mu.Lock()
	if j.closed || j.err != nil || j.attempts >= j.maxAttempts {
		j.mu.Unlock()
		return nil, errors.New("HTTP evidence journal closed, failed or exhausted")
	}
	j.attempts++
	id := fmt.Sprintf("request-%06d", j.attempts)
	requestPath := id + ".body"
	err := j.writeRequestLocked(requestPath, request)
	responsePath := fmt.Sprintf("response-%06d.body", j.attempts)
	var file *os.File
	if err == nil {
		file, err = os.OpenFile(filepath.Join(j.dir, responsePath), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	}
	if err == nil {
		digest := sha256.Sum256(request)
		err = j.appendLocked(map[string]any{"event": "request-started", "request_id": id, "contract": j.contract,
			"request": bodyArtifact{requestPath, hex.EncodeToString(digest[:]), int64(len(request)), true}})
	}
	if err != nil {
		j.err = errors.Join(j.err, err)
		j.mu.Unlock()
		if file != nil {
			err = errors.Join(err, file.Close())
		}
		return nil, fmt.Errorf("prepare HTTP evidence: %w", err)
	}
	j.active++
	j.mu.Unlock()
	capture := &attemptBody{journal: j, id: id, path: responsePath, file: file, digest: sha256.New()}
	resp, err := next.RoundTrip(req)
	if err != nil {
		capture.finish("transport_error", false)
		return resp, err
	}
	if resp == nil {
		capture.finish("transport_error", false)
		return nil, errors.New("upstream returned no HTTP response")
	}
	capture.status = resp.StatusCode
	if resp.Body == nil || resp.Body == http.NoBody {
		capture.finish("no_body", true)
		return resp, nil
	}
	capture.source = resp.Body
	resp.Body = capture
	return resp, nil
}

func (j *HTTPAttemptJournal) writeRequestLocked(name string, body []byte) error {
	file, err := os.OpenFile(filepath.Join(j.dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(body)
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}

// Close refuses to certify a run with bodies still being read. Abrupt process
// loss leaves durable starts without finishes; replay must retain them as unknown.
func (j *HTTPAttemptJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.active != 0 {
		return errors.New("HTTP evidence still has active response bodies")
	}
	if !j.closed {
		j.closed = true
		j.err = errors.Join(j.err, j.file.Close())
	}
	return j.err
}

type attemptBody struct {
	mu      sync.Mutex
	journal *HTTPAttemptJournal
	id      string
	path    string
	file    *os.File
	digest  hash.Hash
	source  io.ReadCloser
	status  int
	bytes   int64
	seen    int64
	done    bool
	err     error
}

func (b *attemptBody) Read(p []byte) (int, error) {
	n, err := b.source.Read(p)
	b.mu.Lock()
	if !b.done {
		b.seen += int64(n)
		retained := min(int64(n), b.journal.maxBytes-b.bytes)
		if retained > 0 && b.err == nil {
			written, errWrite := b.file.Write(p[:retained])
			_, _ = b.digest.Write(p[:written])
			b.bytes += int64(written)
			b.err = errWrite
		}
	}
	b.mu.Unlock()
	if err == io.EOF {
		b.finish("eof", true)
	} else if err != nil {
		b.finish("read_error", false)
	}
	return n, err
}

func (b *attemptBody) Close() error {
	err := b.source.Close()
	if err != nil {
		b.finish("close_error", false)
	} else {
		b.finish("closed", false)
	}
	return err
}

func (b *attemptBody) finish(outcome string, complete bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	b.done = true
	b.err = errors.Join(b.err, b.file.Sync(), b.file.Close())
	if b.err != nil {
		outcome, complete = "capture_error", false
	}
	j := b.journal
	j.mu.Lock()
	defer j.mu.Unlock()
	j.active--
	j.err = errors.Join(j.err, b.err)
	_ = j.appendLocked(map[string]any{"event": "request-finished", "request_id": b.id, "contract": j.contract,
		"outcome": outcome, "status_code": b.status, "observed_bytes": b.seen,
		"response": bodyArtifact{b.path, hex.EncodeToString(b.digest.Sum(nil)), b.bytes, complete && b.seen == b.bytes}})
}
