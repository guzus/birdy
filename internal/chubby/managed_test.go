package chubby

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagedClientPathsKeysAndErrors(t *testing.T) {
	var seen []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer chb_fixture" {
			t.Error("missing service key")
		}
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Idempotency-Key"))
		switch r.URL.Path {
		case "/v1/sessions":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["agentId"] != "agent-1" {
				t.Error(body)
			}
			w.Write([]byte(`{"id":"sess-1","agentId":"agent-1","version":2}`))
		case "/v1/runs":
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"id":"run-1","status":"queued"}`))
		case "/v1/runs/run-1":
			w.Write([]byte(`{"run":{"id":"run-1","status":"succeeded","output":"x","costUsd":0.02},"steps":[{"id":"s","kind":"tool","status":"succeeded","name":"t","output":"{}"}],"events":[]}`))
		case "/v1/runs/run-1/events":
			w.Write([]byte(`{"data":[{"id":4,"type":"run.succeeded","data":{}}]}`))
		case "/v1/runs/run-1/cancel":
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":{"code":"invalid_state","message":"Run is finished."}}`))
		case "/v1/redirect":
			http.Redirect(w, r, "/v1/sessions", http.StatusFound)
		}
	}))
	defer s.Close()
	configure(t, s)
	m, err := NewManaged()
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if sess, err := m.CreateSession(ctx, "agent-1", "t"); err != nil || sess.ID != "sess-1" || sess.Version != 2 {
		t.Fatal(sess, err)
	}
	if run, err := m.StartRun(ctx, "sess-1", "go", "birdy-scan-1"); err != nil || run.ID != "run-1" || run.Terminal() {
		t.Fatal(run, err)
	}
	d, err := m.Run(ctx, "run-1")
	if err != nil || !d.Run.Terminal() || *d.Run.Output != "x" || d.Steps[0].Name != "t" {
		t.Fatal(d, err)
	}
	if ev, err := m.Events(ctx, "run-1", 3); err != nil || len(ev) != 1 || ev[0].ID != 4 {
		t.Fatal(ev, err)
	}
	var apiErr *APIError
	if err := m.Cancel(ctx, "run-1"); !errors.As(err, &apiErr) || apiErr.Status != 409 || apiErr.Code != "invalid_state" {
		t.Fatal(err)
	}
	if err := m.call(ctx, http.MethodGet, "/redirect", nil, "", nil); !errors.As(err, &apiErr) || apiErr.Status != http.StatusFound {
		t.Fatalf("redirect followed: %v", err)
	}
	if !strings.HasSuffix(seen[1], " birdy-scan-1") || !strings.Contains(seen[3], "/events?after=3") {
		t.Fatal(seen)
	}
}

func TestBaseURLIsRequired(t *testing.T) {
	t.Setenv("CHUBBY_API_KEY", "chb_fixture")
	t.Setenv("CHUBBY_CA_FILE", "")
	t.Setenv("CHUBBY_BASE_URL", "  ")
	if err := ValidateConfig(); err == nil || !strings.Contains(err.Error(), "CHUBBY_BASE_URL is required") {
		t.Fatalf("missing base URL: %v", err)
	}
	if _, err := NewManaged(); err == nil {
		t.Fatal("managed client built without a base URL")
	}
}
