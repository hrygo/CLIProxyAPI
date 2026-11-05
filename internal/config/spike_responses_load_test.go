package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpikeLoadRejectsInvalidResponsesPolicy(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("config-version: 8\nserver:\n  host: 127.0.0.1\n  port: 8317\nrequests:\n  responses-tools:\n    routes:\n      - match:\n          provider: codex\n          auth-kind: oauth\n          upstream-model: m\n          upstream-format: codex\n        client-search: sometimes\n")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("invalid responses-tools policy was accepted")
	}
}
