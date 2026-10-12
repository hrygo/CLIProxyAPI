package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/watcher/synthesizer"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

type credentialBinding struct {
	NameSHA256    string `json:"name_sha256"`
	ContentSHA256 string `json:"content_sha256"`
}

type credentialFlags []string

func (f *credentialFlags) String() string         { return strings.Join(*f, ",") }
func (f *credentialFlags) Set(value string) error { *f = append(*f, value); return nil }

func parseCredentialBindings(values []string) ([]credentialBinding, error) {
	if len(values) > 8 {
		return nil, errors.New("credential selection budget exceeded")
	}
	var bindings []credentialBinding
	seen := map[string]bool{}
	digest := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, value := range values {
		parts := strings.Split(value, ":")
		if len(parts) != 2 || !digest.MatchString(parts[0]) || !digest.MatchString(parts[1]) || seen[parts[0]] {
			return nil, errors.New("invalid credential selection binding")
		}
		seen[parts[0]] = true
		bindings = append(bindings, credentialBinding{parts[0], parts[1]})
	}
	return bindings, nil
}

func hasNativeAPIKey(cfg *config.Config, provider string) bool {
	switch provider {
	case "codex":
		return len(cfg.CodexKey) > 0
	case "gemini":
		return len(cfg.GeminiKey) > 0
	case "claude":
		return len(cfg.ClaudeKey) > 0
	case "meta":
		return len(cfg.MetaKey) > 0
	}
	return false
}

func selectedCredentialPayloads(cfg *config.Config, p liveProfile) (map[string][]byte, string, error) {
	directory, err := util.ResolveAuthDir(cfg.AuthDir)
	if err != nil {
		return nil, "", errors.New("selected credential directory unavailable")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, "", errors.New("selected credential directory unavailable")
	}
	defer func() { _ = root.Close() }()
	listing, err := root.Open(".")
	if err != nil {
		return nil, "", errors.New("selected credential inventory unavailable")
	}
	entries, err := listing.Readdirnames(4097)
	_ = listing.Close()
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > 4096 {
		return nil, "", errors.New("selected credential inventory budget exceeded")
	}
	wanted := map[string]string{}
	for _, binding := range p.AuthFiles {
		wanted[binding.NameSHA256] = binding.ContentSHA256
	}
	payloads := map[string][]byte{}
	for _, name := range entries {
		expected, selected := wanted[fingerprint([]byte(name))]
		if !selected {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			return nil, "", errors.New("selected credential is not a JSON source")
		}
		before, err := root.Lstat(name)
		if err != nil || !before.Mode().IsRegular() || before.Size() > 1<<20 {
			return nil, "", errors.New("selected credential is not a bounded regular source")
		}
		stream, err := root.Open(name)
		if err != nil {
			return nil, "", errors.New("selected credential could not be opened")
		}
		opened, err := stream.Stat()
		if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
			_ = stream.Close()
			return nil, "", errors.New("selected credential identity changed")
		}
		data, errRead := io.ReadAll(io.LimitReader(stream, (1<<20)+1))
		errClose := stream.Close()
		if errRead != nil || errClose != nil || len(data) > 1<<20 || fingerprint(data) != expected {
			return nil, "", errors.New("selected credential content differs from binding")
		}
		payloads[name] = data
	}
	if len(payloads) != len(wanted) {
		return nil, "", errors.New("selected credential source missing")
	}
	return payloads, directory, nil
}

func loadSelectedCredentials(cfg *config.Config, p liveProfile) ([]*coreauth.Auth, error) {
	if len(p.AuthFiles) == 0 {
		if p.Executor == "antigravity" {
			return nil, errors.New("Antigravity profile requires explicit credential bindings")
		}
		if p.Executor == "codex" || p.Executor == "gemini" || p.Executor == "claude" || p.Executor == "meta" {
			if !hasNativeAPIKey(cfg, p.Executor) {
				return nil, errors.New("native OAuth profile requires explicit credential bindings")
			}
		}
		return nil, nil
	}
	payloads, directory, err := selectedCredentialPayloads(cfg, p)
	if err != nil {
		return nil, err
	}
	ctx := &synthesizer.SynthesisContext{Config: cfg, AuthDir: directory, Now: time.Now()}
	var result []*coreauth.Auth
	for name, data := range payloads {
		items, err := synthesizer.SynthesizeAuthFile(ctx, filepath.Join(directory, name), data)
		if err != nil || len(items) != 1 || items[0] == nil || items[0].Provider != p.Executor || items[0].Disabled {
			return nil, errors.New("selected credential cannot serve pinned native profile")
		}
		if p.Executor == "antigravity" {
			token, _ := items[0].Metadata["access_token"].(string)
			project, _ := items[0].Metadata["project_id"].(string)
			expiry, valid := items[0].ExpirationTime()
			// Match the executor's five-minute safety window; never bootstrap or
			// refresh an operator credential in the isolated acceptance process.
			if strings.TrimSpace(token) == "" || strings.TrimSpace(project) == "" || !valid || !expiry.After(ctx.Now.Add(5*time.Minute)) {
				return nil, errors.New("Antigravity snapshot needs a current access token and project")
			}
		}
		result = append(result, items[0])
	}
	return result, nil
}

func verifySelectedCredentialSources(cfg *config.Config, p liveProfile) error {
	if len(p.AuthFiles) == 0 {
		return nil
	}
	_, _, err := selectedCredentialPayloads(cfg, p)
	return err
}

func isolateSelectedAPIKeys(cfg *config.Config, p liveProfile) {
	if len(p.AuthFiles) == 0 {
		return
	}
	// An explicit OAuth selection cannot silently include config API-key accounts.
	switch p.Executor {
	case "codex":
		cfg.CodexKey = nil
	case "gemini":
		cfg.GeminiKey = nil
	case "claude":
		cfg.ClaudeKey = nil
	case "meta":
		cfg.MetaKey = nil
	}
}
