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
	Stream(t.Context(), "request", "rules", func(_ context.Context, cmd string, args []string) (string, error) {
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
			Stream(t.Context(), "request", "", func(context.Context, string, []string) (string, error) {
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
	Stream(t.Context(), "request", "", nil, func(e claude.Event) { events = append(events, e) })
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
	go func() { Stream(ctx, "cancel", "", nil, func(e claude.Event) { events <- e }); close(events) }()
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
	Stream(t.Context(), "ping", "", nil, func(e claude.Event) { events = append(events, e) })
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
