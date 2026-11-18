package chubby

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/claude"
)

func configure(t *testing.T, s *httptest.Server) {
	t.Helper()
	t.Setenv("CHUBBY_API_KEY", "chb_fixture")
	t.Setenv("CHUBBY_BASE_URL", s.URL+"/v1")
	t.Setenv("CHUBBY_MODEL", "")
	t.Setenv("CHUBBY_CA_FILE", "")
}
func ask(prompt, system string) Conversation {
	return Conversation{System: system, Messages: []Message{{Role: "user", Content: prompt}}}
}
func frame(w io.Writer, payload string) { fmt.Fprintf(w, "data: %s\n\n", payload) }
func finish(w io.Writer, reason string) {
	frame(w, fmt.Sprintf(`{"choices":[{"index":0,"delta":{},"finish_reason":%q}]}`, reason))
}

const textFrame = `{"choices":[{"index":0,"delta":{"content":"answer"}}]}`
const callFrame = `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"birdy_command","arguments":"{\"command\":\"read\",\"args\":[\"12345\"]}"}}]}}]}`

func TestStreamToolAndReplay(t *testing.T) {
	var hits atomic.Int32
	var firstKey string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer chb_fixture" || r.Header.Get("Accept") != "text/event-stream" {
			t.Error("unexpected endpoint or credentials")
		}
		var req request
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if err := d.Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if !req.Stream || !req.StreamOptions["include_usage"] || req.Model != "auto" || len(req.Tools) != 1 || req.Messages[0].Role != "system" || req.Messages[0].Content != "rules" {
			t.Error("unexpected inference options")
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Error("missing idempotency key")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if hits.Add(1) == 1 {
			firstKey = r.Header.Get("Idempotency-Key")
			if len(req.Messages) != 2 || req.Messages[1].Content != "request" {
				t.Error("first prompt")
			}
			frame(w, callFrame)
			finish(w, "tool_calls")
		} else {
			if r.Header.Get("Idempotency-Key") == firstKey {
				t.Error("reused billing key")
			}
			if len(req.Messages) != 4 || len(req.Messages[2].Calls) != 1 || req.Messages[2].Calls[0].ID != "call1" || req.Messages[3].CallID != "call1" || req.Messages[3].Content != "ok" {
				t.Error("incomplete tool transcript")
			}
			frame(w, textFrame)
			finish(w, "stop")
		}
		frame(w, `{"choices":[],"usage":{"total_tokens":23}}`)
		frame(w, "[DONE]")
	}))
	defer s.Close()
	configure(t, s)
	var events []claude.Event
	n := 0
	Stream(t.Context(), ask("request", "rules"), func(_ context.Context, cmd string, args []string) (string, error) {
		n++
		if cmd != "read" || !slices.Equal(args, []string{"12345"}) {
			t.Fatal("wrong command")
		}
		return "ok", nil
	}, func(e claude.Event) { events = append(events, e) })
	if n != 1 || hits.Load() != 2 || len(events) != 3 || events[0].Type != claude.EventToolUse || events[1].Text != "answer" || events[2].Type != claude.EventDone {
		t.Fatalf("events %v", events)
	}
}

func TestStreamRefusesUnfinishedOrUnsafeToolTurn(t *testing.T) {
	for name, write := range map[string]func(io.Writer){
		"disconnect": func(w io.Writer) { frame(w, callFrame); finish(w, "tool_calls") },
		"no_finish":  func(w io.Writer) { frame(w, callFrame); frame(w, "[DONE]") },
		"in_band_error": func(w io.Writer) {
			frame(w, callFrame)
			finish(w, "tool_calls")
			frame(w, `{"error":{"message":"private-message"}}`)
			frame(w, "[DONE]")
		},
		"duplicate_tool": func(w io.Writer) {
			frame(w, callFrame)
			frame(w, strings.Replace(callFrame, `"index":0,"id"`, `"index":1,"id"`, 1))
			finish(w, "tool_calls")
			frame(w, "[DONE]")
		},
		"bad_index": func(w io.Writer) {
			frame(w, strings.Replace(callFrame, `"index":0,"id"`, `"index":100,"id"`, 1))
			finish(w, "tool_calls")
			frame(w, "[DONE]")
		},
		"forbidden": func(w io.Writer) {
			frame(w, strings.Replace(callFrame, `read`, `tweet`, 1))
			finish(w, "tool_calls")
			frame(w, "[DONE]")
		},
		"usage_before_finish": func(w io.Writer) { frame(w, `{"choices":[],"usage":{"total_tokens":23}}`); frame(w, "[DONE]") },
		"empty_answer":        func(w io.Writer) { finish(w, "stop"); frame(w, "[DONE]") },
		"truncated_answer":    func(w io.Writer) { finish(w, "length"); frame(w, "[DONE]") },
	} {
		t.Run(name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				write(w)
			}))
			defer s.Close()
			configure(t, s)
			var events []claude.Event
			Stream(t.Context(), ask("request", ""), func(context.Context, string, []string) (string, error) {
				t.Fatal("invalid tool was executed")
				return "", nil
			}, func(e claude.Event) { events = append(events, e) })
			if len(events) != 2 || events[0].Type != claude.EventError || strings.Contains(events[0].Error, "private-message") || events[1].Type != claude.EventDone {
				t.Fatal(events)
			}
		})
	}
}
func TestNoToolAndNoRetry(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var req map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&req)
		if _, ok := req["tools"]; ok {
			t.Error("tools leaked into no-tool call")
		}
		http.Error(w, "private-message", http.StatusConflict)
	}))
	defer s.Close()
	configure(t, s)
	var events []claude.Event
	Stream(t.Context(), ask("request", ""), nil, func(e claude.Event) { events = append(events, e) })
	if hits.Load() != 1 || len(events) != 2 || events[0].Type != claude.EventError || !strings.Contains(events[0].Error, "409") || strings.Contains(events[0].Error, "private-message") || events[1].Type != claude.EventDone {
		t.Fatal(events)
	}
}
func TestStreamCancellation(t *testing.T) {
	started := make(chan struct{})
	upstreamDone := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(upstreamDone)
	}))
	defer s.Close()
	configure(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := make(chan claude.Event, 3)
	go func() { Stream(ctx, ask("cancel", ""), nil, func(e claude.Event) { events <- e }); close(events) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request stalled")
	}
	cancel()
	select {
	case <-upstreamDone:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not reach upstream")
	}
	for e := range events {
		if e.Type != claude.EventDone {
			t.Fatal(e)
		}
	}
}

func TestStreamUsesConfiguredRelayCA(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		frame(w, textFrame)
		finish(w, "stop")
		frame(w, `{"choices":[],"usage":{"total_tokens":1,"cost":0.00001}}`)
		frame(w, "[DONE]")
	}))
	defer s.Close()
	configure(t, s)
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})
	certFile := filepath.Join(t.TempDir(), "relay.pem")
	if err := os.WriteFile(certFile, cert, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHUBBY_CA_FILE", certFile)
	if err := ValidateConfig(); err != nil {
		t.Fatal(err)
	}
	var events []claude.Event
	Stream(t.Context(), ask("ping", ""), nil, func(e claude.Event) { events = append(events, e) })
	if len(events) != 2 || events[0].Text != "answer" || events[1].Type != claude.EventDone {
		t.Fatal(events)
	}
	if err := os.WriteFile(certFile, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if ValidateConfig() == nil {
		t.Fatal("invalid CA accepted")
	}
	t.Setenv("CHUBBY_CA_FILE", "")
	t.Setenv("CHUBBY_BASE_URL", "http://remote.invalid/v1")
	if ValidateConfig() == nil {
		t.Fatal("cleartext remote endpoint accepted")
	}
}

// A conversation's next turn must begin with its previous request byte for
// byte (tools, system, earlier turns), or the provider's prompt cache misses.
func TestNextTurnExtendsPreviousRequestExactly(t *testing.T) {
	type wire struct {
		Tools    json.RawMessage   `json:"tools"`
		Messages []json.RawMessage `json:"messages"`
	}
	var bodies []wire
	var raw, keys, retention []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		raw = append(raw, string(data))
		keys = append(keys, r.Header.Get("X-Chubby-Cache-Key"))
		retention = append(retention, r.Header.Get("X-Chubby-Cache-Retention"))
		var got wire
		if err := json.Unmarshal(data, &got); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, got)
		w.Header().Set("Content-Type", "text/event-stream")
		if len(bodies) == 1 {
			frame(w, callFrame)
			finish(w, "tool_calls")
		} else {
			frame(w, textFrame)
			finish(w, "stop")
		}
		frame(w, `{"choices":[],"usage":{"total_tokens":3}}`)
		frame(w, "[DONE]")
	}))
	defer s.Close()
	configure(t, s)
	run := func(context.Context, string, []string) (string, error) { return "tool output", nil }
	first := Conversation{System: "rules", Messages: []Message{{Role: "user", Content: "first question"}}, Retention: RetentionShort}
	Stream(t.Context(), first, run, func(claude.Event) {})
	if len(bodies) != 2 {
		t.Fatalf("turn 1 made %d requests", len(bodies))
	}
	second := first
	second.Messages = append(slices.Clone(first.Messages), Message{Role: "assistant", Content: "answer"}, Message{Role: "user", Content: "follow up"})
	Stream(t.Context(), second, run, func(claude.Event) {})
	if len(bodies) != 3 {
		t.Fatalf("turn 2 made %d requests", len(bodies)-2)
	}
	turn1, turn2 := bodies[0], bodies[2]
	if string(turn1.Tools) != string(turn2.Tools) || len(turn1.Tools) == 0 {
		t.Fatal("tool definitions changed between turns")
	}
	if len(turn2.Messages) != len(turn1.Messages)+2 {
		t.Fatalf("turn 2 has %d messages, turn 1 had %d", len(turn2.Messages), len(turn1.Messages))
	}
	for i := range turn1.Messages {
		if string(turn1.Messages[i]) != string(turn2.Messages[i]) {
			t.Fatalf("message %d rewritten: %s != %s", i, turn1.Messages[i], turn2.Messages[i])
		}
	}
	if !strings.HasPrefix(raw[2], strings.TrimSuffix(strings.SplitN(raw[0], `"tools"`, 2)[0], "],")) {
		t.Fatal("turn 2's body does not begin with turn 1's model and messages")
	}
	// One key for the whole conversation, in headers only: today's body schema
	// refuses unknown fields.
	if !regexp.MustCompile(`^birdy:[0-9a-f]{32}$`).MatchString(keys[0]) {
		t.Fatalf("cache key %q", keys[0])
	}
	for i, body := range raw {
		if keys[i] != keys[0] || retention[i] != "5m" {
			t.Fatalf("request %d cache headers %q %q", i, keys[i], retention[i])
		}
		if strings.Contains(body, "birdy:") || strings.Contains(body, "cache") {
			t.Fatal("cache fields sent in the body")
		}
	}
}

func TestRequestLayoutAndCacheKey(t *testing.T) {
	conv := Conversation{System: "rules", Messages: []Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}, {Role: "user", Content: "c"}}, Retention: RetentionShort}
	req, err := newRequest("m", conv, true)
	if err != nil {
		t.Fatal(err)
	}
	roles := []string{}
	for _, m := range req.Messages {
		roles = append(roles, m.Role)
	}
	if !slices.Equal(roles, []string{"system", "user", "assistant", "user"}) {
		t.Fatal(roles)
	}
	a, _ := json.Marshal(req)
	b, _ := json.Marshal(req)
	if string(a) != string(b) {
		t.Fatal("request encoding is not deterministic")
	}
	longer := conv
	longer.Messages = append(slices.Clone(conv.Messages), Message{Role: "assistant", Content: "d"}, Message{Role: "user", Content: "e"})
	next, _ := newRequest("m", longer, true)
	other, _ := newRequest("m", Conversation{System: "rules", Messages: []Message{{Role: "user", Content: "z"}}, Retention: RetentionShort}, true)
	if req.PromptCacheKey == "" || req.PromptCacheKey != next.PromptCacheKey || req.PromptCacheKey == other.PromptCacheKey {
		t.Fatal("cache key is not stable per conversation")
	}
	oneOff, _ := newRequest("m", Conversation{Messages: conv.Messages, Retention: RetentionNone}, false)
	if oneOff.PromptCacheKey != "" || oneOff.CacheRetention != "none" {
		t.Fatalf("one-off call %q %q", oneOff.PromptCacheKey, oneOff.CacheRetention)
	}
	if _, err := newRequest("m", Conversation{Messages: conv.Messages, Retention: "forever"}, false); err == nil {
		t.Fatal("accepted an unknown retention")
	}
	for _, bad := range []Conversation{
		{},
		{Messages: []Message{{Role: "assistant", Content: "x"}}},
		{Messages: []Message{{Role: "system", Content: "x"}, {Role: "user", Content: "y"}}},
		{Messages: []Message{{Role: "tool", Content: "x"}, {Role: "user", Content: "y"}}},
	} {
		if _, err := newRequest("m", bad, false); err == nil {
			t.Fatalf("accepted %#v", bad)
		}
	}
}
