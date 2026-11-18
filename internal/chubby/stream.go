// Package chubby runs Birdy's chat on Office's direct streaming inference API.
package chubby

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/guzus/birdy/internal/birdtool"
	"github.com/guzus/birdy/internal/claude"
)

type ToolRunner func(context.Context, string, []string) (string, error)

const (
	defaultBase     = "https://gunux.tailc0c7fe.ts.net/office/api/chubby/v1"
	maxTurns        = 12
	maxTools        = 16
	maxRequestBytes = 1 << 20
	maxToolOutput   = 32 << 10
)

type function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type toolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function function `json:"function"`
}
type message struct {
	Role    string     `json:"role"`
	Content string     `json:"content"`
	Calls   []toolCall `json:"tool_calls,omitempty"`
	CallID  string     `json:"tool_call_id,omitempty"`
}
type request struct {
	Model         string          `json:"model"`
	Messages      []message       `json:"messages"`
	Tools         []any           `json:"tools,omitempty"`
	Stream        bool            `json:"stream"`
	StreamOptions map[string]bool `json:"stream_options"`
	// PromptCacheKey is stable for one conversation. It is not sent yet: give it
	// a json name once the inference endpoint accepts a per-conversation key.
	PromptCacheKey string `json:"-"`
}

// Message is one conversation turn supplied by the caller, oldest first.
type Message struct {
	Role    string // "user" or "assistant"
	Content string
}

// Conversation is everything one answer is generated from. Provider prompt
// caches match an exact prefix (tools, system, then messages), so callers keep
// System fixed and only ever append to Messages; context that changes belongs
// in the message of the turn that introduced it.
type Conversation struct {
	System   string
	Messages []Message
}

func toolDefinition() any {
	return map[string]any{"type": "function", "function": map[string]any{
		"name": "birdy_command", "description": "Run a public read-only Birdy X command.",
		"parameters": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"command", "args"}, "properties": map[string]any{
			"command": map[string]any{"type": "string", "enum": birdtool.ChubbyCommands()},
			"args":    map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
		}}}}
}

type client struct {
	http                 *http.Client
	endpoint, key, model string
}

func fromEnv() (*client, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("CHUBBY_BASE_URL")), "/")
	if base == "" {
		base = defaultBase
	}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		!(u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())) {
		return nil, errors.New("invalid CHUBBY_BASE_URL (HTTPS required outside loopback)")
	}
	key := strings.TrimSpace(os.Getenv("CHUBBY_API_KEY"))
	if !strings.HasPrefix(key, "chb_") {
		return nil, errors.New("CHUBBY_API_KEY must be a Chubby direct service key")
	}
	model := strings.TrimSpace(os.Getenv("CHUBBY_MODEL"))
	if model == "" {
		model = "auto"
	}
	h := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if file := strings.TrimSpace(os.Getenv("CHUBBY_CA_FILE")); file != "" {
		pem, err := os.ReadFile(file)
		if err != nil {
			return nil, errors.New("unable to read CHUBBY_CA_FILE")
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, errors.New("unable to load system CA certificates")
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("CHUBBY_CA_FILE has no CA certificates")
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		h.Transport = transport
	}
	return &client{http: h, endpoint: base + "/chat/completions", model: model, key: key}, nil
}

// ValidateConfig checks the endpoint, service key and trust roots without making a request.
func ValidateConfig() error {
	_, err := fromEnv()
	return err
}

// Stream emits Birdy's normal event sequence. A nil runner disables tools.
func Stream(ctx context.Context, conv Conversation, run ToolRunner, emit func(claude.Event)) {
	defer emit(claude.Event{Type: claude.EventDone})
	c, err := fromEnv()
	if err == nil {
		if c.http.Transport != nil {
			defer c.http.CloseIdleConnections()
		}
		err = c.chat(ctx, conv, run, emit)
	}
	if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
		emit(claude.Event{Type: claude.EventError, Error: err.Error()})
	}
}

// newRequest lays a conversation out as tools, system, then the turns in
// order, so the next turn's request begins with this one byte for byte.
func newRequest(model string, conv Conversation, tools bool) (request, error) {
	if len(conv.Messages) == 0 || conv.Messages[len(conv.Messages)-1].Role != "user" {
		return request{}, errors.New("conversation must end with a user message")
	}
	req := request{Model: model, Stream: true, StreamOptions: map[string]bool{"include_usage": true}}
	if conv.System != "" {
		req.Messages = append(req.Messages, message{Role: "system", Content: conv.System})
	}
	for _, m := range conv.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return request{}, errors.New("conversation has an unsupported role")
		}
		req.Messages = append(req.Messages, message{Role: m.Role, Content: m.Content})
	}
	if tools {
		req.Tools = []any{toolDefinition()}
	}
	req.PromptCacheKey = cacheKey(model, conv)
	return req, nil
}

// cacheKey names a conversation by what never changes in it: the model, the
// system prompt and its first message.
func cacheKey(model string, conv Conversation) string {
	h := sha256.New()
	for _, part := range []string{model, conv.System, conv.Messages[0].Content} {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return "birdy-" + hex.EncodeToString(h.Sum(nil))[:32]
}

func (c *client) chat(ctx context.Context, conv Conversation, run ToolRunner, emit func(claude.Event)) error {
	req, err := newRequest(c.model, conv, run != nil)
	if err != nil {
		return err
	}
	var seed [16]byte
	rand.Read(seed[:])
	prefix := "birdy-" + hex.EncodeToString(seed[:])
	used := map[string]bool{}
	for i := 0; i < maxTurns; i++ {
		result, err := c.turn(ctx, req, fmt.Sprintf("%s-%d", prefix, i+1), emit)
		if err != nil {
			return err
		}
		if result.finish == "stop" {
			return nil
		}
		if run == nil || i+1 == maxTurns || len(used)+len(result.calls) > maxTools {
			return errors.New("Chubby tool budget exceeded or tools unavailable")
		}
		var commands []arguments
		for _, call := range result.calls {
			args, err := decodeCall(call)
			if err != nil || used[call.ID] {
				return errors.New("invalid or repeated Chubby tool call")
			}
			used[call.ID] = true
			commands = append(commands, args)
		}
		req.Messages = append(req.Messages, message{Role: "assistant", Content: result.text.String(), Calls: result.calls})
		for j, cmd := range commands {
			if err := ctx.Err(); err != nil {
				return err
			}
			emit(claude.Event{Type: claude.EventToolUse, Command: "birdy " + cmd.Command})
			output, err := run(ctx, cmd.Command, cmd.Args)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				output = "Birdy could not complete this read."
			}
			output = strings.ToValidUTF8(strings.ReplaceAll(output, c.key, "[redacted]"), "�")
			if len(output) > maxToolOutput {
				output = output[:maxToolOutput]
				for !utf8.ValidString(output) {
					output = output[:len(output)-1]
				}
				output += "\n[Output truncated]"
			}
			req.Messages = append(req.Messages, message{Role: "tool", CallID: result.calls[j].ID, Content: output})
		}
	}
	return errors.New("Chubby turn limit reached")
}

type arguments struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func decodeCall(call toolCall) (arguments, error) {
	var a arguments
	if call.ID == "" || len(call.ID) > 200 || call.Type != "function" || call.Function.Name != "birdy_command" || !json.Valid([]byte(call.Function.Arguments)) {
		return a, errors.New("invalid tool")
	}
	d := json.NewDecoder(strings.NewReader(call.Function.Arguments))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil || !slices.Contains(birdtool.ChubbyCommands(), a.Command) || a.Args == nil {
		return a, errors.New("invalid tool arguments")
	}
	return a, nil
}

var safeRequestID = regexp.MustCompile(`^chatcmpl_[a-fA-F0-9-]{1,64}$`)

func (c *client) turn(ctx context.Context, req request, id string, emit func(claude.Event)) (*turnResult, error) {
	data, err := json.Marshal(req)
	if err != nil || len(data) > maxRequestBytes {
		return nil, errors.New("Chubby request exceeds the supported payload size")
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("invalid Chubby endpoint")
	}
	r.GetBody = nil // Never replay a paid request on transport failure.
	r.Header.Set("Authorization", "Bearer "+c.key)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/event-stream")
	r.Header.Set("Idempotency-Key", id)
	res, err := c.http.Do(r)
	if err != nil {
		return nil, errors.New("Chubby inference connection failed")
	}
	defer res.Body.Close()
	rid := res.Header.Get("X-Request-Id")
	if !safeRequestID.MatchString(rid) {
		rid = ""
	}
	log.Printf("[chubby] request_id=%s status=%d", rid, res.StatusCode)
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Chubby inference failed (HTTP %d)", res.StatusCode)
	}
	ct, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || ct != "text/event-stream" {
		return nil, errors.New("Chubby returned an unexpected response type")
	}
	result, err := readStream(res.Body, emit)
	if err == nil && result.tokens != nil {
		cost := ""
		if result.cost != nil {
			cost = fmt.Sprintf(" cost_usd=%g", *result.cost)
		}
		log.Printf("[chubby] request_id=%s total_tokens=%d prompt_tokens=%d cache_read=%d cache_write=%d%s",
			rid, *result.tokens, result.prompt, result.cacheRead, result.cacheWrite, cost)
	}
	return result, err
}
