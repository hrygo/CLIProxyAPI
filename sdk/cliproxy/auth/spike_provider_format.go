package auth

import (
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"strings"
)

func providerDefaultFormat(provider string) sdktranslator.Format {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex":
		return sdktranslator.FormatCodex
	case "xai":
		return sdktranslator.FormatCodex
	case "claude":
		return sdktranslator.FormatClaude
	case "gemini", "vertex", "aistudio":
		return sdktranslator.FormatGemini
	case "kimi", "kimi-ai", "kimi.ai", "kimi.com":
		return sdktranslator.FormatOpenAI
	case "meta":
		return sdktranslator.FormatCodex
	case "antigravity":
		return sdktranslator.FormatAntigravity
	case "devin":
		return sdktranslator.FormatInteractions
	default:
		return sdktranslator.FormatOpenAI
	}
}
