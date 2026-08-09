package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/guzus/birdy/internal/claude"
)

const (
	ProviderOpenCodeGo   = "opencode-go"
	ModelDeepSeekV4Flash = "opencode-go/deepseek-v4-flash"
	apiKeyEnv            = "OPENCODE_API_KEY"
	maxOutputBytes       = 128 * 1024
	maxEventBytes        = maxOutputBytes + 32*1024
)

var executablePath = "opencode"

type streamEvent struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
	SessionID string `json:"sessionID"`
	Part      struct {
		ID        string    `json:"id"`
		SessionID string    `json:"sessionID"`
		MessageID string    `json:"messageID"`
		Type      string    `json:"type"`
		Text      string    `json:"text"`
		Time      *partTime `json:"time"`
	} `json:"part"`
	Error json.RawMessage `json:"error"`
}

type partTime struct {
	Start int64  `json:"start"`
	End   *int64 `json:"end"`
}

type config struct {
	AutoUpdate       bool                       `json:"autoupdate"`
	Share            string                     `json:"share"`
	EnabledProviders []string                   `json:"enabled_providers"`
	Model            string                     `json:"model"`
	SmallModel       string                     `json:"small_model"`
	DefaultAgent     string                     `json:"default_agent"`
	Agent            map[string]agentConfig     `json:"agent"`
	Provider         map[string]providerConfig  `json:"provider"`
	Permission       map[string]string          `json:"permission"`
	MCP              map[string]json.RawMessage `json:"mcp"`
	Instructions     []string                   `json:"instructions"`
}

type agentConfig struct {
	Description string            `json:"description"`
	Mode        string            `json:"mode"`
	Model       string            `json:"model"`
	Prompt      string            `json:"prompt"`
	Steps       int               `json:"steps"`
	Permission  map[string]string `json:"permission"`
}

type providerConfig struct {
	Models map[string]json.RawMessage `json:"models"`
}

// BuildNoToolsArgs returns the only OpenCode invocation used for harness
// traffic. The named agent and its model are also pinned in the isolated
// config; passing both here makes CLI/config drift fail visibly.
func BuildNoToolsArgs() []string {
	return []string{
		"run",
		"--pure",
		"--model", ModelDeepSeekV4Flash,
		"--agent", "birdy-harness",
		"--format", "json",
	}
}

func buildConfig(systemPrompt string) (string, error) {
	denyAll := map[string]string{"*": "deny"}
	value := config{
		AutoUpdate:       false,
		Share:            "disabled",
		EnabledProviders: []string{ProviderOpenCodeGo},
		Model:            ModelDeepSeekV4Flash,
		SmallModel:       ModelDeepSeekV4Flash,
		DefaultAgent:     "birdy-harness",
		Agent: map[string]agentConfig{
			"birdy-harness": {
				Description: "Single-turn Birdy harness model with no tools",
				Mode:        "primary",
				Model:       ModelDeepSeekV4Flash,
				Prompt:      systemPrompt,
				Steps:       1,
				Permission:  denyAll,
			},
		},
		Provider: map[string]providerConfig{
			ProviderOpenCodeGo: {Models: map[string]json.RawMessage{"deepseek-v4-flash": json.RawMessage(`{}`)}},
		},
		Permission:   denyAll,
		MCP:          map[string]json.RawMessage{},
		Instructions: []string{},
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode isolated OpenCode config: %w", err)
	}
	return string(raw), nil
}

// StreamNoTools runs the exact OpenCode Go / DeepSeek V4 Flash route in a
// disposable HOME. It deliberately does not consult persistent OpenCode auth,
// plugins, MCP servers, project instructions, or any other model provider.
func StreamNoTools(ctx context.Context, prompt, systemPrompt string, emit func(claude.Event)) {
	if strings.TrimSpace(os.Getenv(apiKeyEnv)) == "" {
		emit(claude.Event{Type: claude.EventError, Error: apiKeyEnv + " is required for the OpenCode harness provider"})
		emit(claude.Event{Type: claude.EventDone})
		return
	}

	runtimeDir, err := os.MkdirTemp("", "birdy-harness-opencode-")
	if err != nil {
		emit(claude.Event{Type: claude.EventError, Error: "failed to create isolated OpenCode runtime"})
		emit(claude.Event{Type: claude.EventDone})
		return
	}
	defer os.RemoveAll(runtimeDir)

	configContent, err := buildConfig(systemPrompt)
	if err != nil {
		emit(claude.Event{Type: claude.EventError, Error: "failed to configure isolated OpenCode runtime"})
		emit(claude.Event{Type: claude.EventDone})
		return
	}
	configPath := filepath.Join(runtimeDir, "opencode.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		emit(claude.Event{Type: claude.EventError, Error: "failed to write isolated OpenCode config"})
		emit(claude.Event{Type: claude.EventDone})
		return
	}

	cmd := exec.CommandContext(ctx, executablePath, BuildNoToolsArgs()...)
	cmd.Dir = runtimeDir
	cmd.Env = isolatedEnvironment(os.Environ(), runtimeDir, configPath)
	cmd.Stdin = strings.NewReader(prompt)
	streamCommand(ctx, cmd, emit)
}

func isolatedEnvironment(env []string, runtimeDir, configPath string) []string {
	allowed := map[string]struct{}{
		"PATH": {}, "LANG": {}, "LC_ALL": {}, "SSL_CERT_FILE": {},
		"SSL_CERT_DIR": {}, "NODE_EXTRA_CA_CERTS": {}, apiKeyEnv: {},
	}
	result := make([]string, 0, len(allowed)+9)
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, keep := allowed[name]; keep {
			result = append(result, entry)
		}
	}
	result = append(result,
		"HOME="+runtimeDir,
		"XDG_CONFIG_HOME="+filepath.Join(runtimeDir, "config"),
		"XDG_DATA_HOME="+filepath.Join(runtimeDir, "data"),
		"XDG_CACHE_HOME="+filepath.Join(runtimeDir, "cache"),
		"XDG_STATE_HOME="+filepath.Join(runtimeDir, "state"),
		"TMPDIR="+runtimeDir,
		"PWD="+runtimeDir,
		"OPENCODE_CONFIG_DIR="+filepath.Join(runtimeDir, "config", "opencode"),
		"OPENCODE_CONFIG="+configPath,
		"OPENCODE_DISABLE_AUTOUPDATE=1",
		"NO_COLOR=1",
	)
	return result
}

// streamCommand parses the v1.18.3 `opencode run --format json` JSONL
// contract. That CLI emits step_start, completed text parts, tool_use,
// step_finish, and error records. Any other or malformed record fails closed.
func streamCommand(ctx context.Context, cmd *exec.Cmd, emit func(claude.Event)) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		emit(claude.Event{Type: claude.EventError, Error: "failed to create OpenCode output pipe"})
		emit(claude.Event{Type: claude.EventDone})
		return
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		emit(claude.Event{Type: claude.EventError, Error: "failed to start OpenCode"})
		emit(claude.Event{Type: claude.EventDone})
		return
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxEventBytes)
	started := false
	finished := false
	failed := false
	outputBytes := 0
	sessionID := ""
	messageID := ""
	var response strings.Builder
	seenPartIDs := make(map[string]struct{})

	fail := func(message string) {
		if failed {
			return
		}
		failed = true
		emit(claude.Event{Type: claude.EventError, Error: message})
	}
	for scanner.Scan() {
		if failed {
			continue
		}
		var event streamEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			fail("invalid OpenCode stream event")
			continue
		}
		if event.Timestamp <= 0 || strings.TrimSpace(event.SessionID) == "" {
			fail("invalid OpenCode stream identity")
			continue
		}
		if sessionID == "" {
			sessionID = event.SessionID
		} else if event.SessionID != sessionID {
			fail("mixed OpenCode stream sessions")
			continue
		}
		if event.Type != "error" {
			if strings.TrimSpace(event.Part.ID) == "" || strings.TrimSpace(event.Part.MessageID) == "" || event.Part.SessionID != event.SessionID {
				fail("invalid OpenCode part identity")
				continue
			}
			if _, duplicate := seenPartIDs[event.Part.ID]; duplicate {
				fail("duplicate OpenCode stream part")
				continue
			}
			seenPartIDs[event.Part.ID] = struct{}{}
		}
		switch event.Type {
		case "step_start":
			if started || finished || event.Part.Type != "step-start" {
				fail("invalid OpenCode step_start event")
				continue
			}
			messageID = event.Part.MessageID
			started = true
		case "text":
			if !started || finished || event.Part.MessageID != messageID || event.Part.Type != "text" || strings.TrimSpace(event.Part.Text) == "" || !completedPart(event.Part.Time) {
				fail("invalid OpenCode text event")
				continue
			}
			outputBytes += len(event.Part.Text)
			if outputBytes > maxOutputBytes {
				fail("OpenCode response exceeded its output limit")
				continue
			}
			// OpenCode emits completed text parts rather than deltas. Accumulate
			// distinct parts and expose Birdy's cumulative snapshot semantics.
			response.WriteString(event.Part.Text)
			emit(claude.Event{Type: claude.EventSnapshot, Text: response.String()})
		case "step_finish":
			if !started || finished || event.Part.MessageID != messageID || event.Part.Type != "step-finish" {
				fail("invalid OpenCode step_finish event")
				continue
			}
			finished = true
		case "reasoning":
			if !started || finished || event.Part.MessageID != messageID || event.Part.Type != "reasoning" || !completedPart(event.Part.Time) {
				fail("invalid OpenCode reasoning event")
			}
		case "tool_use":
			failed = true
			emit(claude.Event{Type: claude.EventToolUse, Command: "disabled OpenCode tool"})
		case "error":
			if len(event.Error) == 0 || string(event.Error) == "null" {
				fail("invalid OpenCode error event")
				continue
			}
			fail("OpenCode model request failed")
		default:
			fail("unsupported OpenCode stream event")
		}
	}
	if err := scanner.Err(); err != nil {
		fail("failed to read OpenCode stream")
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		emit(claude.Event{Type: claude.EventDone})
		return
	}
	if waitErr != nil {
		fail("OpenCode process failed")
	}
	if !failed && (!started || !finished || outputBytes == 0) {
		fail("incomplete OpenCode stream")
	}
	emit(claude.Event{Type: claude.EventDone})
}

func completedPart(value *partTime) bool {
	return value != nil && value.Start > 0 && value.End != nil && *value.End >= value.Start
}
