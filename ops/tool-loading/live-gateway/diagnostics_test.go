package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestCandidateFailureEnvelopeNeverExportsCauses(t *testing.T) {
	secret := errors.New("Bearer private-token /private/account.json https://user:password@example.invalid")
	err := errors.Join(candidateFailure("credentials", "binding_content_mismatch", secret), secret)
	var output bytes.Buffer
	if errWrite := writeCandidateFailure(&output, err); errWrite != nil {
		t.Fatal(errWrite)
	}
	want := `{"version":1,"scope":"isolated-candidate-error","failures":[{"stage":"credentials","reason":"binding_content_mismatch"},{"stage":"unknown","reason":"candidate_failed"}]}`
	if strings.TrimSpace(output.String()) != want {
		t.Fatalf("safe error envelope = %s, want %s", output.String(), want)
	}
	if !errors.Is(err, secret) {
		t.Fatal("internal cause identity was lost")
	}
	output.Reset()
	if errWrite := writeCandidateFailure(&output, candidateFailure("secret-stage", "secret-reason", secret)); errWrite != nil {
		t.Fatal(errWrite)
	}
	if strings.Contains(output.String(), "secret") || strings.Contains(output.String(), "private") {
		t.Fatal("unclassified error fields were exported")
	}
}

func TestCredentialBindingDriftHasSafeIdentity(t *testing.T) {
	cfg := &config.Config{AuthDir: t.TempDir()}
	p := liveProfile{Executor: "codex", AuthFiles: []credentialBinding{{fingerprint([]byte("missing.json")), fingerprint(nil)}}}
	_, err := loadSelectedCredentials(cfg, p)
	var output bytes.Buffer
	if errWrite := writeCandidateFailure(&output, err); errWrite != nil {
		t.Fatal(errWrite)
	}
	if !strings.Contains(output.String(), `"stage":"credentials","reason":"binding_source_missing"`) {
		t.Fatalf("missing bound credential was not diagnosed: %s", output.String())
	}
	raw := []byte(`{"type":"codex","access_token":"synthetic-token"}`)
	if errWrite := os.WriteFile(filepath.Join(cfg.AuthDir, "missing.json"), raw, 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	p.AuthFiles[0].ContentSHA256 = fingerprint(raw)
	if errWrite := os.WriteFile(filepath.Join(cfg.AuthDir, "missing.json"), append(raw, '\n'), 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	_, err = loadSelectedCredentials(cfg, p)
	output.Reset()
	if errWrite := writeCandidateFailure(&output, err); errWrite != nil {
		t.Fatal(errWrite)
	}
	if !strings.Contains(output.String(), `"reason":"binding_content_mismatch"`) || strings.Contains(output.String(), "synthetic-token") {
		t.Fatalf("content drift was not safely diagnosed: %s", output.String())
	}
}
