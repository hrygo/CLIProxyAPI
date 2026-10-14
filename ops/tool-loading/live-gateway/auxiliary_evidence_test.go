package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
)

func TestAuxiliaryWorkEvidenceSealsOnlyAfterOwnerJoins(t *testing.T) {
	directory := t.TempDir()
	scope := helps.NewHTTPAuxiliaryWorkScope()
	work, release, err := helps.BeginHTTPAuxiliaryWork(helps.WithHTTPAuxiliaryWorkScope(context.Background(), scope))
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- closeAuxiliaryWork(context.Background(), scope, directory) }()
	<-work.Done()
	path := filepath.Join(directory, "auxiliary-work.json")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("work evidence written before join")
	}
	release()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state helps.HTTPAuxiliaryWorkSnapshot
	if err := json.Unmarshal(data, &state); err != nil || !state.Sealed || state.Active != 0 || state.Completed != 1 {
		t.Fatalf("invalid settled evidence: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("work evidence is not private")
	}
	if err := closeAuxiliaryWork(context.Background(), scope, directory); err == nil {
		t.Fatal("work evidence was overwritten")
	}
}

func TestAuxiliaryWorkEvidenceRejectsUnsettledAndRejectedWork(t *testing.T) {
	for _, unfinished := range []bool{true, false} {
		t.Run(map[bool]string{true: "unfinished", false: "rejected"}[unfinished], func(t *testing.T) {
			scope := helps.NewHTTPAuxiliaryWorkScope()
			ctx := helps.WithHTTPAuxiliaryWorkScope(context.Background(), scope)
			_, release, err := helps.BeginHTTPAuxiliaryWork(ctx)
			if err != nil {
				t.Fatal(err)
			}
			join, cancel := context.WithCancel(context.Background())
			cancel()
			if !unfinished {
				release()
				if err := scope.CloseContext(context.Background()); err != nil {
					t.Fatal(err)
				}
				if _, _, err := helps.BeginHTTPAuxiliaryWork(ctx); err == nil {
					t.Fatal("closed work admitted")
				}
			}
			if err := closeAuxiliaryWork(join, scope, t.TempDir()); err == nil {
				t.Fatal("invalid cleanup certified")
			}
			release()
		})
	}
}
