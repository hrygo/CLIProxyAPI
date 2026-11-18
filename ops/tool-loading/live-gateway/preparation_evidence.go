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

const maxPreparationRequests = 256

type preparationRecord struct {
	RequestSequence int `json:"request_sequence"`
	responsestools.PreparationFailure
}

type preparationSnapshot struct {
	Version        int                 `json:"version"`
	RequestsSeen   int                 `json:"requests_seen"`
	ActiveRequests int                 `json:"active_requests"`
	Sealed         bool                `json:"sealed"`
	Failed         bool                `json:"failed"`
	Failures       []preparationRecord `json:"failures"`
}

type preparationEvidence struct {
	mu       sync.Mutex
	snapshot preparationSnapshot
	active   map[int]bool
}

func newPreparationEvidence() *preparationEvidence {
	return &preparationEvidence{snapshot: preparationSnapshot{Version: 1, Failures: []preparationRecord{}}, active: make(map[int]bool)}
}

func (journal *preparationEvidence) begin(ctx context.Context) (context.Context, func(), error) {
	journal.mu.Lock()
	if journal.snapshot.Sealed || journal.snapshot.RequestsSeen >= maxPreparationRequests {
		journal.snapshot.Failed = true
		journal.mu.Unlock()
		return ctx, nil, errors.New("preparation evidence request budget unavailable")
	}
	journal.snapshot.RequestsSeen++
	id := journal.snapshot.RequestsSeen
	journal.active[id] = true
	journal.snapshot.ActiveRequests++
	journal.mu.Unlock()
	ctx = responsestools.WithPreparationFailureObserver(ctx, func(event responsestools.PreparationFailure) {
		journal.mu.Lock()
		defer journal.mu.Unlock()
		if journal.snapshot.Sealed || !journal.active[id] || len(journal.snapshot.Failures) >= maxPreparationRequests {
			journal.snapshot.Failed = true
			return
		}
		journal.snapshot.Failures = append(journal.snapshot.Failures, preparationRecord{RequestSequence: id, PreparationFailure: event})
	})
	return ctx, func() {
		journal.mu.Lock()
		defer journal.mu.Unlock()
		if journal.active[id] {
			delete(journal.active, id)
			journal.snapshot.ActiveRequests--
		}
	}, nil
}

func (journal *preparationEvidence) close(directory string) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.snapshot.Sealed {
		return errors.New("preparation evidence already sealed")
	}
	journal.snapshot.Sealed = true
	journal.snapshot.Failed = journal.snapshot.Failed || journal.snapshot.ActiveRequests != 0
	file, err := os.OpenFile(filepath.Join(directory, "preparation.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	errEncode := json.NewEncoder(file).Encode(journal.snapshot)
	var errIncomplete error
	if journal.snapshot.Failed {
		errIncomplete = errors.New("preparation evidence is incomplete")
	}
	return errors.Join(errIncomplete, errEncode, file.Sync(), file.Close())
}
