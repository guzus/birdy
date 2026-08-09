package chatmodel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/guzus/birdy/internal/birdbox"
	"github.com/guzus/birdy/internal/claude"
	"github.com/guzus/birdy/internal/codex"
	"github.com/guzus/birdy/internal/opencode"
)

type Backend string

const (
	BackendClaudeCode Backend = "claude-code"
	BackendCodex      Backend = "codex"
	BackendOpenCode   Backend = "opencode"

	DefaultClientModelID  = "sonnet"
	CodexClientModelID    = "codex"
	DeepSeekClientModelID = "deepseek-flash"
)

type Mode uint8

const (
	ModeGeneral Mode = iota
	ModeNoTools
)

type Selection struct {
	ID            string
	Backend       Backend
	Provider      string
	DisplayModel  string
	RuntimeModel  string
	SupportsTools bool
}

type Request struct {
	Mode         Mode
	Prompt       string
	SystemPrompt string
	BirdyCommand string
}

type StreamFunc func(context.Context, Selection, Request, func(claude.Event))

var serverClaudeModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,79}$`)

func ClientModels() []Selection {
	return []Selection{
		{
			ID: DefaultClientModelID, Backend: BackendClaudeCode,
			Provider: "Claude Code", DisplayModel: "Sonnet", RuntimeModel: "sonnet", SupportsTools: true,
		},
		{
			ID: CodexClientModelID, Backend: BackendCodex,
			Provider: "OpenAI Codex", DisplayModel: codex.ResolveModel(CodexClientModelID), RuntimeModel: CodexClientModelID, SupportsTools: true,
		},
		{
			ID: DeepSeekClientModelID, Backend: BackendOpenCode,
			Provider: "OpenCode Go", DisplayModel: "DeepSeek V4 Flash", RuntimeModel: opencode.ModelDeepSeekV4Flash, SupportsTools: true,
		},
	}
}

func ResolveClient(value string) (Selection, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		normalized = DefaultClientModelID
	}
	for _, model := range ClientModels() {
		if normalized == model.ID {
			return model, nil
		}
	}

	// Preserve the explicit model aliases accepted by the pre-registry API.
	switch normalized {
	case "opus", "haiku":
		return Selection{
			ID: normalized, Backend: BackendClaudeCode, Provider: "Claude Code",
			DisplayModel: strings.ToUpper(normalized[:1]) + normalized[1:], RuntimeModel: normalized, SupportsTools: true,
		}, nil
	case "gpt-5.4", "gpt-5.4-mini":
		return Selection{
			ID: normalized, Backend: BackendCodex, Provider: "OpenAI Codex",
			DisplayModel: normalized, RuntimeModel: normalized, SupportsTools: true,
		}, nil
	case opencode.ModelDeepSeekV4Flash:
		for _, model := range ClientModels() {
			if model.ID == DeepSeekClientModelID {
				return model, nil
			}
		}
		panic("DeepSeek client model is not registered")
	default:
		return Selection{}, fmt.Errorf("unsupported chat model")
	}
}

func ResolveServer(backend, model string) (Selection, error) {
	backend = strings.TrimSpace(backend)
	model = strings.TrimSpace(model)
	switch Backend(backend) {
	case BackendClaudeCode:
		if model == "" {
			model = DefaultClientModelID
		}
		if !serverClaudeModelPattern.MatchString(model) {
			return Selection{}, fmt.Errorf("Claude model identifier must be at most 80 characters")
		}
		return Selection{
			ID: model, Backend: BackendClaudeCode, Provider: "Claude Code",
			DisplayModel: model, RuntimeModel: model, SupportsTools: true,
		}, nil
	case BackendOpenCode:
		if model == "" {
			model = opencode.ModelDeepSeekV4Flash
		}
		if model != opencode.ModelDeepSeekV4Flash {
			return Selection{}, fmt.Errorf("OpenCode supports only %s", opencode.ModelDeepSeekV4Flash)
		}
		return Selection{
			ID: model, Backend: BackendOpenCode, Provider: "OpenCode Go",
			DisplayModel: "DeepSeek V4 Flash", RuntimeModel: model,
		}, nil
	default:
		return Selection{}, fmt.Errorf("backend must be one of %s or %s", BackendClaudeCode, BackendOpenCode)
	}
}

func Available(selection Selection) bool {
	return availableFor(selection, ModeGeneral)
}

func AvailableNoTools(selection Selection) bool {
	return availableFor(selection, ModeNoTools)
}

func availableFor(selection Selection, mode Mode) bool {
	if mode == ModeNoTools {
		switch selection.Backend {
		case BackendClaudeCode:
			return true
		case BackendOpenCode:
			return strings.TrimSpace(os.Getenv("OPENCODE_API_KEY")) != ""
		default:
			return false
		}
	}
	switch selection.Backend {
	case BackendClaudeCode:
		if birdbox.Enabled() {
			return true
		}
		_, err := exec.LookPath("claude")
		return err == nil
	case BackendCodex:
		_, err := exec.LookPath("codex")
		return err == nil
	case BackendOpenCode:
		return opencode.BirdyToolsAvailable()
	default:
		return false
	}
}

func UnavailableReason(selection Selection) string {
	if selection.Backend == BackendOpenCode && !Available(selection) {
		return "Host has not configured OpenCode Go"
	}
	return "Model is unavailable"
}

func Stream(ctx context.Context, selection Selection, request Request, emit func(claude.Event)) {
	if !availableFor(selection, request.Mode) {
		emitFailure(emit, "selected model is unavailable")
		return
	}

	switch selection.Backend {
	case BackendClaudeCode:
		if request.Mode == ModeNoTools {
			if birdbox.Enabled() {
				birdbox.StreamNoTools(ctx, request.Prompt, selection.RuntimeModel, request.SystemPrompt, emit)
				return
			}
			claude.StreamNoTools(ctx, request.Prompt, selection.RuntimeModel, request.SystemPrompt, emit)
			return
		}
		if birdbox.Enabled() {
			birdbox.Stream(ctx, request.Prompt, selection.RuntimeModel, emit)
			return
		}
		claude.Stream(ctx, request.Prompt, selection.RuntimeModel, request.BirdyCommand, emit)
	case BackendCodex:
		if request.Mode != ModeGeneral {
			emitFailure(emit, "selected model does not support this chat mode")
			return
		}
		codex.Stream(ctx, request.Prompt, selection.RuntimeModel, request.BirdyCommand, emit)
	case BackendOpenCode:
		if request.Mode == ModeNoTools {
			opencode.StreamNoTools(ctx, request.Prompt, request.SystemPrompt, emit)
			return
		}
		opencode.StreamWithBirdy(ctx, request.Prompt, request.SystemPrompt, request.BirdyCommand, emit)
	default:
		emitFailure(emit, "unsupported chat model provider")
	}
}

func emitFailure(emit func(claude.Event), message string) {
	emit(claude.Event{Type: claude.EventError, Error: message})
	emit(claude.Event{Type: claude.EventDone})
}
