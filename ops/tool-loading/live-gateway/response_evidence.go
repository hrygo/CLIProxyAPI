package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
)

const maxResponseRequests = 256

type responseRecord struct {
	RequestSequence int `json:"request_sequence"`
	responsestools.ResponseFailure
}
type responseSnapshot struct {
	Version        int              `json:"version"`
	RequestsSeen   int              `json:"requests_seen"`
	ActiveRequests int              `json:"active_requests"`
	Sealed         bool             `json:"sealed"`
	Failed         bool             `json:"failed"`
	Failures       []responseRecord `json:"failures"`
}
type responseEvidence struct {
	mu       sync.Mutex
	snapshot responseSnapshot
	active   map[int]bool
	reported map[int]bool
}

func newResponseEvidence() *responseEvidence {
	return &responseEvidence{snapshot: responseSnapshot{Version: 1, Failures: []responseRecord{}}, active: map[int]bool{}, reported: map[int]bool{}}
}
func (journal *responseEvidence) begin(ctx context.Context) (context.Context, func(), error) {
	journal.mu.Lock()
	if journal.snapshot.Sealed || journal.snapshot.RequestsSeen >= maxResponseRequests {
		journal.snapshot.Failed = true
		journal.mu.Unlock()
		return ctx, nil, errors.New("response evidence request budget unavailable")
	}
	journal.snapshot.RequestsSeen++
	id := journal.snapshot.RequestsSeen
	journal.active[id] = true
	journal.snapshot.ActiveRequests++
	journal.mu.Unlock()
	// Finishing a request joins its synchronous callbacks before marking it
	// inactive. Later reports are outside this owner's diagnostic lifetime.
	var scopeMu sync.Mutex
	scopeFinished := false
	ctx = responsestools.WithResponseFailureObserver(ctx, func(event responsestools.ResponseFailure) {
		scopeMu.Lock()
		defer scopeMu.Unlock()
		if scopeFinished {
			return
		}
		journal.mu.Lock()
		defer journal.mu.Unlock()
		if journal.snapshot.Sealed || !journal.active[id] || journal.reported[id] || len(journal.snapshot.Failures) >= maxResponseRequests {
			journal.snapshot.Failed = true
			return
		}
		journal.reported[id] = true
		journal.snapshot.Failures = append(journal.snapshot.Failures, responseRecord{RequestSequence: id, ResponseFailure: event})
	})
	return ctx, func() {
		scopeMu.Lock()
		defer scopeMu.Unlock()
		journal.mu.Lock()
		defer journal.mu.Unlock()
		scopeFinished = true
		if journal.active[id] {
			delete(journal.active, id)
			journal.snapshot.ActiveRequests--
		}
	}, nil
}
func (journal *responseEvidence) close(directory string) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.snapshot.Sealed {
		return errors.New("response evidence already sealed")
	}
	journal.snapshot.Sealed = true
	journal.snapshot.Failed = journal.snapshot.Failed || journal.snapshot.ActiveRequests != 0
	file, err := os.OpenFile(filepath.Join(directory, "response-failures.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	errEncode := json.NewEncoder(file).Encode(journal.snapshot)
	var errIncomplete error
	if journal.snapshot.Failed {
		errIncomplete = errors.New("response evidence is incomplete")
	}
	return errors.Join(errIncomplete, errEncode, file.Sync(), file.Close())
}
