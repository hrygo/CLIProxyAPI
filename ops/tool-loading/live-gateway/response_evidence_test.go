package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
)

func TestResponseEvidenceSeparatePrivateSealedAndBound(t *testing.T) {
	journal := newResponseEvidence()
	ctx, finish, err := journal.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	responsestools.ReportResponseFailure(ctx, "stream_feed", &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract, Err: errors.New("private receipt")})
	finish()
	finish()
	root := t.TempDir()
	if err := journal.close(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "response-failures.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value responseSnapshot
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if !value.Sealed || value.Failed || value.ActiveRequests != 0 || value.RequestsSeen != 1 || len(value.Failures) != 1 || value.Failures[0].RequestSequence != 1 || value.Failures[0].Phase != "stream_feed" || info.Mode().Perm() != 0600 || strings.Contains(string(raw), "private receipt") {
		t.Fatalf("invalid response evidence: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(root, "preparation.json")); !os.IsNotExist(err) {
		t.Fatal("response evidence reused preparation artifact")
	}
	if journal.close(root) == nil {
		t.Fatal("overwrote prior evidence")
	}
}

func TestResponseEvidenceDuplicateAndUnfinishedCallbacksFail(t *testing.T) {
	for _, scenario := range []string{"duplicate", "unfinished", "budget"} {
		t.Run(scenario, func(t *testing.T) {
			journal := newResponseEvidence()
			ctx, finish, err := journal.begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			event := &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract}
			switch scenario {
			case "duplicate":
				responsestools.ReportResponseFailure(ctx, "stream_feed", event)
				responsestools.ReportResponseFailure(ctx, "stream_feed", event)
				finish()
			case "unfinished":
			case "budget":
				finish()
				for i := 1; i < maxResponseRequests; i++ {
					_, done, err := journal.begin(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					done()
				}
				if _, _, err := journal.begin(context.Background()); err == nil {
					t.Fatal("unbounded requests")
				}
			}
			if journal.close(t.TempDir()) == nil {
				t.Fatal("incomplete or duplicate response evidence accepted")
			}
		})
	}
}

func TestResponseEvidenceFinishedScopeCannotMutateSealedSnapshot(t *testing.T) {
	journal := newResponseEvidence()
	ctx, finish, err := journal.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	finish()
	failure := &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract}
	responsestools.ReportResponseFailure(ctx, "stream_feed", failure)
	root := t.TempDir()
	if err := journal.close(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "response-failures.json")
	before, _ := os.ReadFile(path)
	responsestools.ReportResponseFailure(ctx, "stream_finish", failure)
	after, _ := os.ReadFile(path)
	if journal.snapshot.Failed || len(journal.snapshot.Failures) != 0 || string(before) != string(after) {
		t.Fatal("late report changed the finished owner's evidence")
	}
}

func TestResponseEvidenceConcurrentRequestIdentity(t *testing.T) {
	journal := newResponseEvidence()
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			ctx, finish, err := journal.begin(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			responsestools.ReportResponseFailure(ctx, "stream_finish", &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract})
			finish()
		})
	}
	workers.Wait()
	if err := journal.close(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(journal.snapshot.Failures) != 32 || journal.snapshot.RequestsSeen != 32 {
		t.Fatal("concurrent records lost")
	}
	seen := map[int]bool{}
	for _, event := range journal.snapshot.Failures {
		if seen[event.RequestSequence] {
			t.Fatal("request identity duplicated")
		}
		seen[event.RequestSequence] = true
	}
}
