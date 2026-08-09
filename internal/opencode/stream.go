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
	"time"

	"github.com/guzus/birdy/internal/birdtool"
	"github.com/guzus/birdy/internal/claude"
	"github.com/guzus/birdy/internal/store"
)

const (
	ProviderOpenCodeGo   = "opencode-go"
	ModelDeepSeekV4Flash = "opencode-go/deepseek-v4-flash"
	apiKeyEnv            = "OPENCODE_API_KEY"
	maxOutputBytes       = 128 * 1024
	maxEventBytes        = maxOutputBytes + 32*1024
	maxGeneralSteps      = 12
)

var executablePath = "opencode"

func BirdyToolsAvailable() bool {
	if strings.TrimSpace(os.Getenv(apiKeyEnv)) == "" {
		return false
	}
	if _, err := exec.LookPath(executablePath); err != nil {
		return false
	}
	accounts, err := store.Open()
	return err == nil && accounts.Len() > 0
}

type streamEvent struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
	SessionID string `json:"sessionID"`
	Part      struct {
		ID        string     `json:"id"`
		SessionID string     `json:"sessionID"`
		MessageID string     `json:"messageID"`
		Type      string     `json:"type"`
		Text      string     `json:"text"`
		Time      *partTime  `json:"time"`
		Tool      string     `json:"tool"`
		CallID    string     `json:"callID"`
		State     *toolState `json:"state"`
	} `json:"part"`
	Error json.RawMessage `json:"error"`
}

type partTime struct {
	Start int64  `json:"start"`
	End   *int64 `json:"end"`
}

type toolState struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input"`
}

type runtimeProfile struct {
	agentName    string
	description  string
	steps        int
	allowBirdy   bool
	permission   map[string]string
	runtimeLabel string
}

var noToolsProfile = runtimeProfile{
	agentName: "birdy-harness", description: "Single-turn Birdy harness model with no tools",
	steps: 1, permission: map[string]string{"*": "deny"}, runtimeLabel: "harness",
}

var birdyToolsProfile = runtimeProfile{
	agentName: "birdy-web", description: "Birdy Web model with only the restricted Birdy command tool",
	steps: maxGeneralSteps, allowBirdy: true,
	permission: map[string]string{"*": "deny", "bash": "allow"}, runtimeLabel: "web",
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
	return buildArgs(noToolsProfile)
}

func BuildBirdyToolsArgs() []string {
	return buildArgs(birdyToolsProfile)
}

func buildArgs(profile runtimeProfile) []string {
	return []string{
		"run",
		"--pure",
		"--model", ModelDeepSeekV4Flash,
		"--agent", profile.agentName,
		"--format", "json",
	}
}

func buildConfig(systemPrompt string) (string, error) {
	return buildProfileConfig(systemPrompt, noToolsProfile)
}

func buildProfileConfig(systemPrompt string, profile runtimeProfile) (string, error) {
	value := config{
		AutoUpdate:       false,
		Share:            "disabled",
		EnabledProviders: []string{ProviderOpenCodeGo},
		Model:            ModelDeepSeekV4Flash,
		SmallModel:       ModelDeepSeekV4Flash,
		DefaultAgent:     profile.agentName,
		Agent: map[string]agentConfig{
			profile.agentName: {
				Description: profile.description,
				Mode:        "primary",
				Model:       ModelDeepSeekV4Flash,
				Prompt:      systemPrompt,
				Steps:       profile.steps,
				Permission:  profile.permission,
			},
		},
		Provider: map[string]providerConfig{
			ProviderOpenCodeGo: {Models: map[string]json.RawMessage{"deepseek-v4-flash": json.RawMessage(`{}`)}},
		},
		Permission:   profile.permission,
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
	streamWithProfile(ctx, prompt, systemPrompt, "", "", noToolsProfile, emit)
}

// StreamWithBirdy runs OpenCode in the same disposable runtime, but exposes one
// generated custom tool named bash. It replaces OpenCode's real shell and can
// execute only a bounded Birdy command via argv; no shell is ever involved.
func StreamWithBirdy(ctx context.Context, prompt, systemPrompt, birdyCommand string, emit func(claude.Event)) {
	if strings.TrimSpace(os.Getenv(apiKeyEnv)) == "" {
		emit(claude.Event{Type: claude.EventError, Error: apiKeyEnv + " is required for the OpenCode provider"})
		emit(claude.Event{Type: claude.EventDone})
		return
	}
	accounts, err := store.Open()
	if err != nil || accounts.Len() == 0 {
		emit(claude.Event{Type: claude.EventError, Error: "Birdy accounts are unavailable for OpenCode"})
		emit(claude.Event{Type: claude.EventDone})
		return
	}
	accountsJSON, err := json.Marshal(accounts.Accounts)
	if err != nil {
		emit(claude.Event{Type: claude.EventError, Error: "failed to prepare Birdy accounts for OpenCode"})
		emit(claude.Event{Type: claude.EventDone})
		return
	}
	streamWithProfile(ctx, prompt, systemPrompt, birdyCommand, string(accountsJSON), birdyToolsProfile, emit)
}

func streamWithProfile(ctx context.Context, prompt, systemPrompt, birdyCommand, accountsJSON string, profile runtimeProfile, emit func(claude.Event)) {
	runtimeDir, err := os.MkdirTemp("", "birdy-"+profile.runtimeLabel+"-opencode-")
	if err != nil {
		emit(claude.Event{Type: claude.EventError, Error: "failed to create isolated OpenCode runtime"})
		emit(claude.Event{Type: claude.EventDone})
		return
	}
	defer os.RemoveAll(runtimeDir)

	configContent, err := buildProfileConfig(systemPrompt, profile)
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
	if profile.allowBirdy {
		toolDir := filepath.Join(runtimeDir, "config", "opencode", "tools")
		if err := os.MkdirAll(toolDir, 0700); err != nil {
			emit(claude.Event{Type: claude.EventError, Error: "failed to create isolated OpenCode tool directory"})
			emit(claude.Event{Type: claude.EventDone})
			return
		}
		toolSource, err := buildBirdyTool(birdyCommand)
		if err != nil {
			emit(claude.Event{Type: claude.EventError, Error: "failed to configure restricted Birdy tool"})
			emit(claude.Event{Type: claude.EventDone})
			return
		}
		if err := os.WriteFile(filepath.Join(toolDir, "bash.ts"), []byte(toolSource), 0600); err != nil {
			emit(claude.Event{Type: claude.EventError, Error: "failed to write restricted Birdy tool"})
			emit(claude.Event{Type: claude.EventDone})
			return
		}
	}

	cmd := exec.CommandContext(ctx, executablePath, buildArgs(profile)...)
	cmd.Dir = runtimeDir
	cmd.Env = isolatedEnvironment(os.Environ(), runtimeDir, configPath)
	if profile.allowBirdy {
		cmd.Env = append(cmd.Env, "BIRDY_ACCOUNTS="+accountsJSON)
		if strings.TrimSpace(os.Getenv("BIRDY_READ_ONLY")) != "" {
			cmd.Env = append(cmd.Env, "BIRDY_READ_ONLY="+os.Getenv("BIRDY_READ_ONLY"))
		}
	}
	cmd.Stdin = strings.NewReader(prompt)
	configureProcessGroup(cmd)
	cmd.WaitDelay = 2 * time.Second
	streamCommandWithProfile(ctx, cmd, profile, emit)
}

func buildBirdyTool(birdyCommand string) (string, error) {
	birdyCommand = strings.TrimSpace(birdyCommand)
	if birdyCommand == "" || strings.ContainsAny(birdyCommand, "\x00\r\n") {
		return "", fmt.Errorf("invalid Birdy executable")
	}
	executableJSON, err := json.Marshal(birdyCommand)
	if err != nil {
		return "", err
	}
	commandsJSON, err := json.Marshal(birdtool.ModelCommands())
	if err != nil {
		return "", err
	}

	const template = `const birdyExecutable = __BIRDY_EXECUTABLE__
const allowedCommands = new Set(__ALLOWED_COMMANDS__)
const maxOutputBytes = 131072

function tokenize(command) {
  if (typeof command !== "string" || command.length === 0 || command.length > 16384) throw new Error("invalid birdy command")
  const argv = []
  let token = ""
  let quote = ""
  let escaped = false
  let started = false
  const push = () => {
    if (!started) return
    if (token.length === 0 || token.length > 4096) throw new Error("invalid birdy argument")
    argv.push(token)
    token = ""
    started = false
  }
  for (const char of command) {
    if (escaped) {
      token += char
      started = true
      escaped = false
      continue
    }
    if (char === "\\") {
      escaped = true
      started = true
      continue
    }
    if (quote) {
      if (char === quote) quote = ""
      else token += char
      started = true
      continue
    }
    if (char === "'" || char === '"') {
      quote = char
      started = true
      continue
    }
    if (/\s/.test(char)) {
      push()
      continue
    }
    if (";|&<>$(){}[]".includes(char) || char.charCodeAt(0) === 96) throw new Error("unsupported birdy command syntax")
    token += char
    started = true
  }
  if (escaped || quote) throw new Error("unterminated birdy command")
  push()
  if (argv.length < 2 || argv.length > 34 || argv[0] !== birdyExecutable || !allowedCommands.has(argv[1])) {
    throw new Error("command is outside the Birdy allowlist")
  }
  return argv
}

async function readLimited(stream, process) {
  const reader = stream.getReader()
  const decoder = new TextDecoder()
  let output = ""
  let bytes = 0
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    bytes += value.byteLength
    if (bytes > maxOutputBytes) {
      process.kill()
      throw new Error("birdy output exceeded its limit")
    }
    output += decoder.decode(value, { stream: true })
  }
  return output + decoder.decode()
}

export default {
  description: "Run one allowlisted Birdy X command. The command must begin with the exact Birdy executable shown in the system prompt. No shell syntax is supported.",
  args: {
    command: { type: "string", description: "Exact Birdy command, with quoted arguments when needed", maxLength: 16384 },
  },
  async execute({ command }) {
    const parsed = tokenize(command)
	const childEnv = {}
	for (const name of ["PATH", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "TMPDIR", "BIRDY_ACCOUNTS", "BIRDY_READ_ONLY"]) {
	  if (typeof process.env[name] === "string") childEnv[name] = process.env[name]
	}
    const child = Bun.spawn([birdyExecutable, "--strategy", "random", ...parsed.slice(1)], {
	  cwd: process.cwd(), env: childEnv, stdin: "ignore", stdout: "pipe", stderr: "pipe",
    })
	let timedOut = false
	const timeout = setTimeout(() => {
	  timedOut = true
	  child.kill()
	}, 60000)
	try {
	  const stdoutPromise = readLimited(child.stdout, child)
	  const stderrPromise = readLimited(child.stderr, child)
	  const [stdout, _stderr, exitCode] = await Promise.all([stdoutPromise, stderrPromise, child.exited])
	  if (timedOut) throw new Error("birdy command timed out")
	  if (exitCode !== 0) throw new Error("birdy command failed")
	  const result = stdout.trim()
	  return result || "birdy command completed without output"
	} finally {
	  clearTimeout(timeout)
	}
  },
}
`
	result := strings.ReplaceAll(template, "__BIRDY_EXECUTABLE__", string(executableJSON))
	result = strings.ReplaceAll(result, "__ALLOWED_COMMANDS__", string(commandsJSON))
	return result, nil
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
	streamCommandWithProfile(ctx, cmd, noToolsProfile, emit)
}

func streamCommandWithProfile(ctx context.Context, cmd *exec.Cmd, profile runtimeProfile, emit func(claude.Event)) {
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
	inStep := false
	stepCount := 0
	failed := false
	outputBytes := 0
	sessionID := ""
	messageID := ""
	var response strings.Builder
	seenPartIDs := make(map[string]struct{})
	seenCallIDs := make(map[string]struct{})

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
			if inStep || stepCount >= profile.steps || event.Part.Type != "step-start" {
				fail("invalid OpenCode step_start event")
				continue
			}
			messageID = event.Part.MessageID
			inStep = true
		case "text":
			if !inStep || event.Part.MessageID != messageID || event.Part.Type != "text" || strings.TrimSpace(event.Part.Text) == "" || !completedPart(event.Part.Time) {
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
			if !inStep || event.Part.MessageID != messageID || event.Part.Type != "step-finish" {
				fail("invalid OpenCode step_finish event")
				continue
			}
			inStep = false
			stepCount++
		case "reasoning":
			if !inStep || event.Part.MessageID != messageID || event.Part.Type != "reasoning" || !completedPart(event.Part.Time) {
				fail("invalid OpenCode reasoning event")
			}
		case "tool_use":
			if !profile.allowBirdy {
				failed = true
				emit(claude.Event{Type: claude.EventToolUse, Command: "disabled OpenCode tool"})
				continue
			}
			command, ok := validBirdyToolEvent(event, messageID, inStep, seenCallIDs)
			if !ok {
				fail("invalid OpenCode Birdy tool event")
				continue
			}
			emit(claude.Event{Type: claude.EventToolUse, Command: command})
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
	if !failed && (inStep || stepCount == 0 || outputBytes == 0) {
		fail("incomplete OpenCode stream")
	}
	emit(claude.Event{Type: claude.EventDone})
}

func validBirdyToolEvent(event streamEvent, messageID string, inStep bool, seenCallIDs map[string]struct{}) (string, bool) {
	if !inStep || event.Part.MessageID != messageID || event.Part.Type != "tool" || event.Part.Tool != "bash" || strings.TrimSpace(event.Part.CallID) == "" || event.Part.State == nil {
		return "", false
	}
	if event.Part.State.Status != "completed" && event.Part.State.Status != "error" {
		return "", false
	}
	if _, duplicate := seenCallIDs[event.Part.CallID]; duplicate {
		return "", false
	}
	var input struct {
		Command string `json:"command"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(event.Part.State.Input)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Command) == "" || len(input.Command) > 16*1024 || strings.ContainsAny(input.Command, "\x00\r\n") {
		return "", false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return "", false
	}
	seenCallIDs[event.Part.CallID] = struct{}{}
	return strings.TrimSpace(input.Command), true
}

func completedPart(value *partTime) bool {
	return value != nil && value.Start > 0 && value.End != nil && *value.End >= value.Start
}
