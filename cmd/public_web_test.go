package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/chatmodel"
	"github.com/guzus/birdy/internal/claude"
)

func TestPublicWebAccess(t *testing.T) {
	t.Setenv("BIRDY_WEB_PUBLIC", "1")
	t.Setenv("BIRDY_WEB_CHAT_BACKEND", "chubby")
	t.Setenv("CHUBBY_API_KEY", "chb_fixture")
	t.Setenv("CHUBBY_MODEL", "auto")
	t.Setenv(harnessTokenHashesEnv, `{"fixture":"`+strings.Repeat("ac", 32)+`"}`)
	t.Setenv(harnessBackendEnv, "claude-code")
	mux := buildHostedMux("private-invite", nil, t.TempDir())
	for i, tc := range []struct {
		method, path, invite, body string
		status                     int
	}{
		{"GET", "/api/chat/models", "", "", 200},
		{"GET", "/api/chat/models", "wrong", "", 401},
		{"GET", "/api/chat/models", "private-invite", "", 200},
		{"POST", "/api/command", "", `{"command":"check"}`, 401},
		{"POST", "/api/multi-command", "", "{}", 401},
		{"POST", "/api/harness/chat", "", "{}", 401},
		{"POST", "/api/shares", "", "{}", 401},
		{"POST", "/api/chat", "", `{"model":"haiku","prompt":"hello"}`, 400},
		{"POST", "/api/scan", "", `{"model":"opus"}`, 400},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.RemoteAddr = fmt.Sprintf("203.0.113.%d:1200", i+1)
		r.Header.Set("X-Invite-Code", tc.invite)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: status=%d, want=%d", tc.method, tc.path, w.Code, tc.status)
		}
		if tc.path != "/api/chat/models" || w.Code != 200 {
			continue
		}
		var catalog apiChatModelsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
			t.Fatal(err)
		}
		if tc.invite != "" {
			if catalog.Access != "invited" || len(catalog.Models) < 2 || !catalog.Models[0].SupportsBirdyTools {
				t.Fatal("lost invited workspace access")
			}
		} else if catalog.Access != "public" || len(catalog.Models) != 1 || catalog.Models[0].SupportsBirdyTools || !catalog.Models[0].Available {
			t.Fatalf("unexpected public catalog %+v", catalog)
		}
	}
	t.Setenv("BIRDY_WEB_PUBLIC", "")
	for _, path := range []string{"/api/chat/models", "/api/chat", "/api/scan"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatalf("%s status=%d, want gated", path, w.Code)
		}
	}
}

func TestPublicWebChatSendsNoTools(t *testing.T) {
	t.Setenv("BIRDY_WEB_PUBLIC", "1")
	t.Setenv("BIRDY_WEB_CHAT_BACKEND", "chubby")
	t.Setenv("CHUBBY_API_KEY", "chb_fixture")
	t.Setenv("CHUBBY_MODEL", "auto")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools []json.RawMessage `json:"tools"`
			Model string            `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if len(req.Tools) != 0 || req.Model != "auto" {
			t.Errorf("unexpected public chat tools or model: %+v", req)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello from the public model\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer upstream.Close()
	t.Setenv("CHUBBY_BASE_URL", upstream.URL+"/v1")
	t.Setenv("CHUBBY_CA_FILE", "")
	r := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"prompt":"hello"}`))
	r.RemoteAddr = "203.0.113.236:1112"
	w := httptest.NewRecorder()
	handleAPIChat("private-invite")(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hello from the public model") || !strings.Contains(w.Body.String(), "event: done") {
		t.Fatalf("public chat response %d %s", w.Code, w.Body)
	}
}

func TestPublicWebScanUsesOnlyPublicExplore(t *testing.T) {
	t.Setenv("BIRDY_WEB_PUBLIC", "1")
	t.Setenv("BIRDY_WEB_CHAT_BACKEND", "chubby")
	t.Setenv("CHUBBY_API_KEY", "chb_fixture")
	t.Setenv("CHUBBY_MODEL", "auto")
	tabs := make(chan string, 4)
	h := apiScanHandler("private-invite", func(_ context.Context, s scanSource) ([]scanPost, error) {
		if s.key != s.exploreTab || s.query != "" {
			t.Errorf("non-Explore public read: %+v", s)
		}
		tabs <- s.exploreTab
		return []scanPost{{ID: "news:demo", URL: "https://x.com/i/trending/123", Text: "An Explore headline."}}, nil
	}, func(_ context.Context, model chatmodel.Selection, req chatmodel.Request, emit func(claude.Event)) {
		if req.Mode != chatmodel.ModeNoTools || req.RunBirdyTool != nil || model.Backend != chatmodel.BackendChubby {
			t.Error("unexpected public scan model capabilities")
		}
		emit(claude.Event{Type: claude.EventToken, Text: `{"category":"TRENDING","title":"X headline","summary":"An Explore headline.","ids":["news:demo"]}`})
		emit(claude.Event{Type: claude.EventDone})
	})
	r := httptest.NewRequest("POST", "/api/scan", strings.NewReader("{}"))
	r.RemoteAddr = "203.0.113.235:1112"
	w := httptest.NewRecorder()
	h(w, r)
	close(tabs)
	var got []string
	for tab := range tabs {
		got = append(got, tab)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"entertainment", "news", "sports", "trending"}) {
		t.Fatalf("public Explore tabs=%v", got)
	}
	body, _ := io.ReadAll(w.Result().Body)
	if w.Code != 200 || !strings.Contains(string(body), "event: card") || !strings.Contains(string(body), "event: done") {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
}
