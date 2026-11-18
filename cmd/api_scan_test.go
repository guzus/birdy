package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/chatmodel"
	"github.com/guzus/birdy/internal/claude"
)

func TestScanOverlapProgressAndGroundedSingleSynthesis(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	entered := make(chan string, 4)
	releaseHome := make(chan struct{})
	releaseRest := make(chan struct{})
	var active, peak atomic.Int32
	read := func(ctx context.Context, s scanSource) ([]scanPost, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		entered <- s.key
		wait := releaseRest
		if s.key == "home" {
			wait = releaseHome
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wait:
		}
		if s.key == "news" {
			return nil, errors.New("internal info must not be displayed")
		}
		return []scanPost{{ID: "1234", URL: "https://x.com/i/web/status/1234", Text: "A photo from orbit", Author: "NASA"}}, nil
	}
	var calls int
	text := `{"category":"AI","title":"A photo","summary":"Evidence from orbit.","bullets":["New photo."],"ids":["1234","1234"]}`
	stream := func(_ context.Context, _ chatmodel.Selection, r chatmodel.Request, emit func(claude.Event)) {
		calls++
		if r.Mode != chatmodel.ModeNoTools || r.RunBirdyTool != nil {
			t.Error("scan allowed model tools")
		}
		if n := strings.Count(r.Prompt, "A photo from orbit"); n != 1 {
			t.Errorf("summary saw duplicate evidence %d", n)
		}
		emit(claude.Event{Type: claude.EventToken, Text: text[:12]})
		emit(claude.Event{Type: claude.EventSnapshot, Text: text + "\n"})
		emit(claude.Event{Type: claude.EventSnapshot, Text: text + "\n"})
		emit(claude.Event{Type: claude.EventDone})
	}
	events := make(chan any, 8)
	finished := make(chan error, 1)
	go func() {
		finished <- runScan(ctx, chatmodel.Selection{}, scanSources, read, stream, func(_ string, data any) { events <- data })
	}()
	names := map[string]bool{}
	for range 3 {
		select {
		case name := <-entered:
			names[name] = true
		case <-ctx.Done():
			t.Fatal("three independent reads did not start")
		}
	}
	if !names["home"] || names["news"] {
		t.Fatalf("unexpected initial wave %v", names)
	}
	close(releaseHome)
	select {
	case ev := <-events:
		if s := ev.(scanSourceEvent); s.Source != "home" || s.Status != "ok" || len(s.Posts) != 1 {
			t.Fatalf("first event %+v", s)
		}
	case <-ctx.Done():
		t.Fatal("home was blocked behind slower reads")
	}
	close(releaseRest)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	close(events)
	if peak.Load() != 3 || calls != 1 {
		t.Fatalf("concurrency=%d synthesis calls=%d", peak.Load(), calls)
	}
	cards, failed := 0, 0
	for event := range events {
		switch v := event.(type) {
		case scanCardEvent:
			cards++
			if len(v.Sources) != 1 || v.Sources[0].URL != "https://x.com/i/web/status/1234" {
				t.Fatal(v)
			}
		case scanSourceEvent:
			if v.Source == "news" && v.Status == "error" && v.Posts != nil {
				failed++
			}
		}
	}
	if cards != 1 || failed != 1 {
		t.Fatalf("cards=%d failed sources=%d", cards, failed)
	}
}

func TestScanUnavailableAndInvalidEvidence(t *testing.T) {
	cases := []struct {
		name   string
		posts  []scanPost
		output string
		calls  int
	}{
		{"all empty", nil, "", 0},
		{"unfetched reference", []scanPost{{ID: "1", Text: "a"}}, `{"category":"SIGNAL","title":"T","summary":"S","ids":["unseen"]}`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			cards := 0
			err := runScan(t.Context(), chatmodel.Selection{}, scanSources, func(context.Context, scanSource) ([]scanPost, error) { return tc.posts, nil }, func(_ context.Context, _ chatmodel.Selection, _ chatmodel.Request, emit func(claude.Event)) {
				calls++
				emit(claude.Event{Type: claude.EventToken, Text: tc.output})
				emit(claude.Event{Type: claude.EventDone})
			}, func(kind string, _ any) {
				if kind == "card" {
					cards++
				}
			})
			if err == nil || calls != tc.calls || cards != 0 {
				t.Fatalf("err=%v calls=%d cards=%d", err, calls, cards)
			}
		})
	}
}

func TestScanHTTPContract(t *testing.T) {
	t.Setenv("BIRDY_WEB_CHAT_BACKEND", "chubby")
	t.Setenv("CHUBBY_API_KEY", "chb_test")
	t.Setenv("CHUBBY_MODEL", "auto")
	var reads, synthesis atomic.Int32
	h := apiScanHandler("invite", func(context.Context, scanSource) ([]scanPost, error) {
		reads.Add(1)
		return []scanPost{{ID: "22", URL: "https://x.com/i/web/status/22", Text: "sourced"}}, nil
	}, func(_ context.Context, _ chatmodel.Selection, _ chatmodel.Request, emit func(claude.Event)) {
		synthesis.Add(1)
		emit(claude.Event{Type: claude.EventToken, Text: `{"category":"SIGNAL","title":"T","summary":"S","bullets":["more"],"ids":["22"]}`})
		emit(claude.Event{Type: claude.EventDone})
	})
	for i, tc := range []struct {
		method, body, token string
		code                int
	}{{"POST", "{}", "", 401}, {"GET", "{}", "invite", 405}, {"POST", `{"query":"override"}`, "invite", 400}, {"POST", `{"model":"missing"}`, "invite", 400}, {"POST", `{"model":"codex"}`, "invite", 503}, {"POST", "{}", "invite", 200}} {
		r := httptest.NewRequest(tc.method, "/api/scan", strings.NewReader(tc.body))
		r.RemoteAddr = fmt.Sprintf("192.0.2.%d:123", 193+i)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("status %d, want %d", w.Code, tc.code)
		}
		if tc.code != 200 {
			continue
		}
		if w.Header().Get("Content-Type") != "text/event-stream" || w.Header().Get("X-Birdy-Model-ID") != "chubby" {
			t.Fatal(w.Header())
		}
		kinds := []string{}
		for _, part := range strings.Split(w.Body.String(), "\n\n") {
			if part == "" {
				continue
			}
			a, b, ok := strings.Cut(part, "\n")
			if !ok {
				t.Fatal(part)
			}
			var data map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(b, "data: ")), &data); err != nil || a != "event: "+data["type"].(string) {
				t.Fatal(part)
			}
			kinds = append(kinds, data["type"].(string))
		}
		if strings.Join(kinds, ",") != "source,source,source,source,card,done" {
			t.Fatal(kinds)
		}
	}
	if reads.Load() != 4 || synthesis.Load() != 1 {
		t.Fatalf("reads %d synthesis %d", reads.Load(), synthesis.Load())
	}
}

func TestScanNewsURLs(t *testing.T) {
	for raw, want := range map[string]string{"https://x.com/i/trending/123": "https://x.com/i/trending/123", "twitter://i/events/123": "https://x.com/i/events/123", "twitter://search?query=A%26B": "https://x.com/search?q=A%26B", "https://x.com/redirect?q=X": "", "https://x.com.evil/x": "", "https://bad@x.com/i/trending/123": "", "javascript:foo": ""} {
		if got := scanNewsURL(raw); got != want {
			t.Errorf("scanNewsURL(%q)=%q, want %q", raw, got, want)
		}
	}
}
