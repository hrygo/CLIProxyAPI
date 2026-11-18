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

func TestPreparationEvidenceIsPrivateSealedAndNeverOverwritten(t *testing.T) {
	journal := newPreparationEvidence()
	ctx, finish, err := journal.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	responsestools.ReportPreparationFailure(ctx, &responsestools.ToolCompatibilityError{
		Reason: responsestools.ReasonDeclarationBudget, Err: errors.New("private receipt")})
	finish()
	finish()
	directory := t.TempDir()
	if err := journal.close(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "preparation.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot preparationSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if snapshot.Version != 1 || !snapshot.Sealed || snapshot.Failed || snapshot.ActiveRequests != 0 || snapshot.RequestsSeen != 1 ||
		len(snapshot.Failures) != 1 || snapshot.Failures[0].RequestSequence != 1 || info.Mode().Perm() != 0600 || strings.Contains(string(data), "private receipt") {
		t.Fatalf("invalid diagnostic snapshot: %s", data)
	}
	if journal.close(directory) == nil {
		t.Fatal("overwrote sealed evidence")
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(data) {
		t.Fatal("replaced prior diagnostic")
	}
}

func TestPreparationEvidenceRejectsUnfinishedAndOverBudgetRequests(t *testing.T) {
	for _, name := range []string{"unfinished", "budget"} {
		t.Run(name, func(t *testing.T) {
			journal := newPreparationEvidence()
			if name == "unfinished" {
				if _, _, err := journal.begin(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				for range maxPreparationRequests {
					_, finish, err := journal.begin(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					finish()
				}
				if _, _, err := journal.begin(context.Background()); err == nil {
					t.Fatal("unbounded diagnostic requests")
				}
			}
			if journal.close(t.TempDir()) == nil {
				t.Fatal("incomplete evidence reported success")
			}
		})
	}
}

func TestPreparationEvidenceConcurrentRequestsReleaseBeforeSealing(t *testing.T) {
	journal := newPreparationEvidence()
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			ctx, finish, err := journal.begin(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			responsestools.ReportPreparationFailure(ctx, &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonJSONSyntax})
			finish()
		})
	}
	workers.Wait()
	if err := journal.close(t.TempDir()); err != nil || journal.snapshot.RequestsSeen != 32 || len(journal.snapshot.Failures) != 32 {
		t.Fatalf("concurrent evidence not released: snapshot=%+v error=%v", journal.snapshot, err)
	}
}

func TestPreparationEvidenceLateCallbackFailsBeforeSealing(t *testing.T) {
	journal := newPreparationEvidence()
	ctx, finish, err := journal.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	finish()
	responsestools.ReportPreparationFailure(ctx, &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonJSONSyntax})
	if journal.close(t.TempDir()) == nil {
		t.Fatal("callback after request release silently accepted")
	}
}
