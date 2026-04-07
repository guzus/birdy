package tui

import (
	"os"
	"strings"
)

type chatBackend string

const (
	backendClaude chatBackend = "claude"
	backendCodex  chatBackend = "codex"
)

func normalizeModelSelection(selection string) string {
	switch strings.ToLower(strings.TrimSpace(selection)) {
	case "sonnet":
		return "sonnet"
	case "opus":
		return "opus"
	case "haiku":
		return "haiku"
	case "codex", "gpt-5.4", "gpt-5.4-mini":
		return "codex"
	default:
		return "sonnet"
	}
}

func nextModelSelection(selection string) string {
	switch normalizeModelSelection(selection) {
	case "sonnet":
		return "opus"
	case "opus":
		return "haiku"
	case "haiku":
		return "codex"
	default:
		return "sonnet"
	}
}

func modelDisplayLabel(selection string) string {
	switch normalizeModelSelection(selection) {
	case "codex":
		return "CODEX"
	default:
		return strings.ToUpper(normalizeModelSelection(selection))
	}
}

func modelBackend(selection string) chatBackend {
	switch normalizeModelSelection(selection) {
	case "codex":
		return backendCodex
	default:
		return backendClaude
	}
}

func modelCLIName(selection string) string {
	switch normalizeModelSelection(selection) {
	case "codex":
		if configured := strings.TrimSpace(os.Getenv("BIRDY_TUI_CODEX_MODEL")); configured != "" {
			return configured
		}
		return "gpt-5.4-mini"
	default:
		return normalizeModelSelection(selection)
	}
}
