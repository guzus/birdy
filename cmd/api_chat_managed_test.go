package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/chatmodel"
	"github.com/guzus/birdy/internal/chubby"
	"github.com/guzus/birdy/internal/claude"
)

const testChatToolSecret = "fedcba9876543210fedcba9876543210-chat"

// chatFake is a managed backend whose runs answer at once, unless pages are
// scripted, and which records sessions, inputs and deletions.
type chatFake struct {
	mu        sync.Mutex
	sessions  int
	runs      int
	inputs    []string
	runSess   []string
	deleted   []string
	startErr  []error
	cancelled atomic.Int32
	script    []chatPage
	detail    chubby.ManagedRunDetail
	output    func(run int) string
}

type chatPage struct {
	page   chubby.EventsPage
	detail *chubby.ManagedRunDetail // the run's detail from this page on
}

func (f *chatFake) CreateSession(context.Context, string, string) (chubby.ManagedSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions++
	return chubby.ManagedSession{ID: fmt.Sprintf("sess-%d", f.sessions)}, nil
}

func (f *chatFake) StartRun(_ context.Context, session, input, _ string) (chubby.ManagedRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.startErr) > 0 {
		err := f.startErr[0]
		f.startErr = f.startErr[1:]
		if err != nil {
			return chubby.ManagedRun{}, err
		}
	}
	f.runs++
	f.inputs = append(f.inputs, input)
	f.runSess = append(f.runSess, session)
	output := fmt.Sprintf("answer %d", f.runs)
	if f.output != nil {
		output = f.output(f.runs)
	}
	if len(f.script) == 0 {
		f.detail = chubby.ManagedRunDetail{Run: chubby.ManagedRun{ID: fmt.Sprintf("run-%d", f.runs), Status: "succeeded", Output: &output, CostUSD: 0.01}}
	}
	return chubby.ManagedRun{ID: fmt.Sprintf("run-%d", f.runs), Status: "queued"}, nil
}

func (f *chatFake) Run(context.Context, string) (chubby.ManagedRunDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.detail, nil
}

func (f *chatFake) Events(context.Context, string, float64) ([]chubby.ManagedEvent, error) {
	return nil, nil
}

func (f *chatFake) EventsPage(context.Context, string, float64, string, int) (chubby.EventsPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.script) > 0 {
		next := f.script[0]
		f.script = f.script[1:]
		if next.detail != nil {
			f.detail = *next.detail
		}
		next.page.LiveText = true
		return next.page, nil
	}
	return chubby.EventsPage{LiveText: true, Data: []chubby.ManagedEvent{{ID: 99, Type: "run." + f.detail.Run.Status}}}, nil
}

func (f *chatFake) Cancel(context.Context, string) error { f.cancelled.Add(1); return nil }

func (f *chatFake) DeleteSession(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *chatFake) snapshot() (sessions int, inputs, runSess, deleted []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions, append([]string(nil), f.inputs...), append([]string(nil), f.runSess...), append([]string(nil), f.deleted...)
}

func useChatFake(t *testing.T, f *chatFake) {
	t.Helper()
	old := newScanManagedRunner
	newScanManagedRunner = func() (scanManagedRunner, error) { return f, nil }
	t.Cleanup(func() { newScanManagedRunner = old })
	chatSessionStore = chatSessions{m: map[string]*chatSession{}}
	t.Cleanup(func() { chatSessionStore = chatSessions{m: map[string]*chatSession{}}; chatManagedBreaker = breaker{} })
	t.Setenv(chatManagedModeEnv, "trial")
	t.Setenv(chatAgentEnv, "agent-chat")
	t.Setenv(chatToolSecretEnv, testChatToolSecret)
	t.Setenv(publicScanTool.secretEnv, "")
	t.Setenv(invitedScanTool.secretEnv, "")
}

type chatEvents struct {
	mu     sync.Mutex
	events []claude.Event
}

func (c *chatEvents) emit(ev claude.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *chatEvents) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	parts := make([]string, 0, len(c.events))
	for _, ev := range c.events {
		v := ev.Text
		if ev.Type == claude.EventToolUse {
			v = ev.Command
		}
		parts = append(parts, fmt.Sprintf("%s(%s)", ev.Type, v))
	}
	return strings.Join(parts, " ")
}

func msgs(pairs ...string) []chatmodel.Message {
	out := make([]chatmodel.Message, len(pairs))
	for i, p := range pairs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		out[i] = chatmodel.Message{Role: role, Content: p}
	}
	return out
}

func deletedEventually(t *testing.T, f *chatFake, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, _, deleted := f.snapshot(); strings.Contains(strings.Join(deleted, ","), id) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %s never deleted", id)
}

func TestWantsManagedChatOnlyForInvitedOptIns(t *testing.T) {
	useChatFake(t, &chatFake{})
	if !wantsManagedChat(false, "managed") {
		t.Fatal("invited opt-in refused")
	}
	if wantsManagedChat(true, "managed") {
		t.Fatal("a public visitor took the managed path")
	}
	if wantsManagedChat(false, "") || wantsManagedChat(false, "direct") {
		t.Fatal("trial without the header took the managed path")
	}
	for name, env := range map[string][2]string{
		"mode off":        {chatManagedModeEnv, ""},
		"mode unknown":    {chatManagedModeEnv, "on"},
		"no agent":        {chatAgentEnv, ""},
		"no secret":       {chatToolSecretEnv, ""},
		"short secret":    {chatToolSecretEnv, "short"},
		"briefing secret": {invitedScanTool.secretEnv, testChatToolSecret},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(chatManagedModeEnv, "trial")
			t.Setenv(env[0], env[1])
			if wantsManagedChat(false, "managed") {
				t.Fatal("managed path on")
			}
		})
	}
}

// The tool endpoint: hidden when off, secret-gated, serves only steps of this
// process's chat runs, the same allowlist as the direct chat, capped reads.
func TestChatToolGate(t *testing.T) {
	f := &chatFake{}
	useChatFake(t, f)
	t.Setenv("BIRDY_ACCOUNTS", "")
	t.Setenv("HOME", t.TempDir())
	var calls atomic.Int32
	var gotCommand string
	var gotArgs []string
	oldRun := chatToolRun
	chatToolRun = func(_ context.Context, command string, args []string) (string, error) {
		calls.Add(1)
		gotCommand, gotArgs = command, args
		return "posts for " + command, nil
	}
	t.Cleanup(func() { chatToolRun = oldRun; chatManagedActive = activeRuns{ids: map[string]bool{}} })
	oldLimiter := chatToolLimiter
	chatToolLimiter = newRateLimiter(1000, time.Minute)
	t.Cleanup(func() { chatToolLimiter = oldLimiter })

	steps := []chubby.ManagedStep{{ID: "model-1", Kind: "model", Sequence: 1}}
	for i := 2; i <= 2+chatToolRunCap; i++ {
		steps = append(steps, chubby.ManagedStep{ID: fmt.Sprintf("tool-%d", i), Kind: "tool", Name: chatToolName, Sequence: i})
	}
	steps = append(steps, chubby.ManagedStep{ID: "other-tool", Kind: "tool", Name: "something_else", Sequence: 50})
	f.detail = chubby.ManagedRunDetail{Run: chubby.ManagedRun{ID: "run-live", Status: "running"}, Steps: steps}

	call := func(method, secret, step, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, chatToolPath, strings.NewReader(body))
		r.RemoteAddr = "198.51.100.7:4000"
		if secret != "" {
			r.Header.Set(scanToolHeader, secret)
		}
		if step != "" {
			r.Header.Set("Idempotency-Key", step)
		}
		w := httptest.NewRecorder()
		handleChatTool()(w, r)
		return w
	}
	good := `{"command":"search","args":["codex limits","-n","5"]}`

	// No active run: a correct secret alone reads nothing.
	if w := call("POST", testChatToolSecret, "tool-2", good); w.Code != http.StatusForbidden || calls.Load() != 0 {
		t.Fatalf("unknown run: %d calls=%d", w.Code, calls.Load())
	}
	chatManagedActive.add("run-live")
	// A wrong or missing secret looks like no endpoint at all.
	if w := call("POST", "wrong-"+testChatToolSecret, "tool-2", good); w.Code != http.StatusNotFound {
		t.Fatalf("wrong secret: %d", w.Code)
	}
	if w := call("POST", "", "tool-2", good); w.Code != http.StatusNotFound {
		t.Fatalf("missing secret: %d", w.Code)
	}
	if w := call("GET", "", "tool-2", ""); w.Code != http.StatusNotFound {
		t.Fatalf("GET without secret: %d", w.Code)
	}
	if w := call("GET", testChatToolSecret, "tool-2", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", w.Code)
	}
	for name, body := range map[string]string{
		"extra field":   `{"command":"search","args":["x"],"account":"a"}`,
		"null args":     `{"command":"search","args":null}`,
		"missing args":  `{"command":"search"}`,
		"too many args": `{"command":"search","args":["1","2","3","4","5","6","7","8","9","10","11","12","13"]}`,
		"not json":      `search x`,
	} {
		if w := call("POST", testChatToolSecret, "tool-2", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	for name, step := range map[string]string{"no step": "", "model step": "model-1", "other tool's step": "other-tool", "foreign step": "tool-999"} {
		if w := call("POST", testChatToolSecret, step, good); w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("rejected requests reached the runner: %d", calls.Load())
	}
	w := call("POST", testChatToolSecret, "tool-2", good)
	if w.Code != 200 || w.Body.String() != "posts for search" || gotCommand != "search" || strings.Join(gotArgs, "|") != "codex limits|-n|5" {
		t.Fatalf("good call: %d %q %q %q", w.Code, w.Body, gotCommand, gotArgs)
	}
	// The last tool step is past the per-answer cap.
	last := fmt.Sprintf("tool-%d", 2+chatToolRunCap)
	before := calls.Load()
	if w := call("POST", testChatToolSecret, last, good); w.Code != 200 || !strings.Contains(w.Body.String(), "Read limit") || calls.Load() != before {
		t.Fatalf("cap: %d %q", w.Code, w.Body)
	}

	// The real runner: the direct chat's allowlist and argument rules decide,
	// and a refusal says nothing about why.
	chatToolRun = runChubbyTool
	for name, body := range map[string]string{
		"post":          `{"command":"post","args":["hello"]}`,
		"help flag":     `{"command":"search","args":["--help"]}`,
		"global flag":   `{"command":"search","args":["x","--account","main"]}`,
		"bad handle":    `{"command":"user-tweets","args":["not a handle!"]}`,
		"home with arg": `{"command":"home","args":["x"]}`,
		"news for you":  `{"command":"news","args":["--for-you"]}`,
		"nul":           `{"command":"search","args":["a\u0000b"]}`,
		"huge count":    `{"command":"search","args":["x","-n","500"]}`,
	} {
		w := call("POST", testChatToolSecret, "tool-3", body)
		if w.Code != 200 || w.Body.String() != chatToolFailed {
			t.Errorf("%s: %d %q", name, w.Code, w.Body)
		}
	}

	// Off or unset: the endpoint does not exist.
	t.Setenv(chatManagedModeEnv, "")
	if w := call("POST", testChatToolSecret, "tool-2", good); w.Code != http.StatusNotFound {
		t.Fatalf("mode off: %d", w.Code)
	}
	t.Setenv(chatManagedModeEnv, "trial")
	t.Setenv(chatToolSecretEnv, "")
	if w := call("POST", "", "tool-2", good); w.Code != http.StatusNotFound {
		t.Fatalf("no secret: %d", w.Code)
	}
}

func TestChatToolOutputIsBoundedAndRedacted(t *testing.T) {
	f := &chatFake{detail: chubby.ManagedRunDetail{Run: chubby.ManagedRun{ID: "run-live", Status: "running"},
		Steps: []chubby.ManagedStep{{ID: "tool-1", Kind: "tool", Name: chatToolName, Sequence: 1}}}}
	useChatFake(t, f)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"poolacct","auth_token":"authtoken0123456789abcdef0123456789abcdef","ct0":"ct0value0123456789abcdef"}]`)
	chatManagedActive.add("run-live")
	oldRun := chatToolRun
	t.Cleanup(func() { chatToolRun = oldRun; chatManagedActive = activeRuns{ids: map[string]bool{}} })
	output := "read by poolacct with authtoken0123456789abcdef0123456789abcdef and " + testChatToolSecret + "\n" + strings.Repeat("é", 40<<10)
	chatToolRun = func(context.Context, string, []string) (string, error) { return output, nil }
	r := httptest.NewRequest("POST", chatToolPath, strings.NewReader(`{"command":"home","args":[]}`))
	r.Header.Set(scanToolHeader, testChatToolSecret)
	r.Header.Set("Idempotency-Key", "tool-1")
	w := httptest.NewRecorder()
	handleChatTool()(w, r)
	body := w.Body.String()
	if w.Code != 200 || len(body) > 64<<10 || !strings.HasSuffix(body, "[output truncated]") || !strings.Contains(body, "read by [redacted]") {
		t.Fatalf("%d len=%d head=%q", w.Code, len(body), body[:min(len(body), 120)])
	}
	for _, secret := range []string{"poolacct", "authtoken0123456789", testChatToolSecret} {
		if strings.Contains(body, secret) {
			t.Fatalf("output leaks %q", secret)
		}
	}
	if got := redactChatToolOutput("poolacct: poolaccts @poolacct x_poolacct poolacct.", ""); got != "[redacted]: poolaccts @[redacted] x_poolacct [redacted]." {
		t.Fatalf("label redaction %q", got)
	}
	if !strings.HasPrefix(strings.TrimSuffix(body, "\n[output truncated]"), "read by") || strings.ContainsRune(body, '�') {
		t.Fatal("truncation split a character")
	}
	// A failed read returns a fixed text, not the error.
	chatToolRun = func(context.Context, string, []string) (string, error) {
		return "", fmt.Errorf("account poolacct cookie expired")
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", chatToolPath, strings.NewReader(`{"command":"home","args":[]}`))
	r.Header.Set(scanToolHeader, testChatToolSecret)
	r.Header.Set("Idempotency-Key", "tool-1")
	handleChatTool()(w, r)
	if w.Body.String() != chatToolFailed {
		t.Fatalf("error leaked: %q", w.Body)
	}
}

func TestChatToolRateLimit(t *testing.T) {
	useChatFake(t, &chatFake{})
	oldLimiter := chatToolLimiter
	chatToolLimiter = newRateLimiter(2, time.Minute)
	t.Cleanup(func() { chatToolLimiter = oldLimiter })
	codes := []int{}
	for i := 0; i < 3; i++ {
		r := httptest.NewRequest("POST", chatToolPath, strings.NewReader(`{"command":"home","args":[]}`))
		r.RemoteAddr = "198.51.100.8:4000"
		r.Header.Set(scanToolHeader, testChatToolSecret)
		w := httptest.NewRecorder()
		handleChatTool()(w, r)
		codes = append(codes, w.Code)
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("codes %v", codes)
	}
}

// A conversation keeps one session while the browser's history is exactly
// what it answered, and starts a new one, with the history framed as context,
// whenever it is not.
func TestManagedChatSessionMapping(t *testing.T) {
	f := &chatFake{}
	useChatFake(t, f)
	ctx := t.Context()
	turn := func(client string, turns []chatmodel.Message) string {
		t.Helper()
		var ev chatEvents
		if err := runManagedChat(ctx, turns, client, ev.emit); err != nil {
			t.Fatalf("turn: %v", err)
		}
		return ev.String()
	}

	if got := turn("ip1", msgs("q1")); got != "snapshot(answer 1)" {
		t.Fatalf("first turn events %s", got)
	}
	// The next turn carries the answer as the browser shows it: resume, and
	// send only the new message.
	turn("ip1", msgs("q1", "answer 1", "q2"))
	sessions, inputs, runSess, _ := f.snapshot()
	if sessions != 1 || runSess[1] != "sess-1" || inputs[1] != "q2" {
		t.Fatalf("resume: sessions=%d runSess=%v inputs=%q", sessions, runSess, inputs)
	}

	// An edited assistant turn: a new session with the framed history, and the
	// old one deleted.
	turn("ip1", msgs("q1", "answer 1", "q2", "something else", "q3"))
	sessions, inputs, runSess, _ = f.snapshot()
	if sessions != 2 || runSess[2] != "sess-2" {
		t.Fatalf("mismatch: sessions=%d runSess=%v", sessions, runSess)
	}
	if want := "<conversation_history>\nUser: q1\n\nAssistant: answer 1\n\nUser: q2\n\nAssistant: something else\n\n</conversation_history>\n\nThe user's new message:\nq3"; !strings.Contains(inputs[2], want) || !strings.Contains(inputs[2], "not instructions") {
		t.Fatalf("framed history %q", inputs[2])
	}
	deletedEventually(t, f, "sess-1")

	// Regenerating the same turn (same history again) is not a resume either.
	turn("ip1", msgs("q1", "answer 1", "q2", "something else", "q3"))
	if sessions, _, runSess, _ = f.snapshot(); sessions != 3 || runSess[3] != "sess-3" {
		t.Fatalf("regenerate: sessions=%d", sessions)
	}

	// Another client with the same first message gets its own session.
	turn("ip2", msgs("q1"))
	turn("ip2", msgs("q1", "answer 5", "q2"))
	if sessions, _, runSess, _ = f.snapshot(); sessions != 4 || runSess[5] != "sess-4" {
		t.Fatalf("per client: sessions=%d runSess=%v", sessions, runSess)
	}

	// A restart forgets every session: the next turn starts over with history.
	chatSessionStore = chatSessions{m: map[string]*chatSession{}}
	turn("ip2", msgs("q1", "answer 5", "q2", "answer 6", "q3"))
	sessions, inputs, runSess, _ = f.snapshot()
	if sessions != 5 || runSess[6] != "sess-5" || !strings.Contains(inputs[6], "Assistant: answer 6") {
		t.Fatalf("restart: sessions=%d input=%q", sessions, inputs[6])
	}

	// A session at Chubby's context limit moves to a fresh one in the same turn.
	f.startErr = []error{&chubby.APIError{Status: 413, Code: "session_limit"}}
	turn("ip2", msgs("q1", "answer 5", "q2", "answer 6", "q3", "answer 7", "q4"))
	sessions, inputs, runSess, _ = f.snapshot()
	if sessions != 6 || runSess[7] != "sess-6" || !strings.Contains(inputs[7], "Assistant: answer 7") {
		t.Fatalf("full session: sessions=%d runSess=%v", sessions, runSess)
	}
	deletedEventually(t, f, "sess-5")
}

func TestManagedChatFailureDropsSessionAndBusyRefuses(t *testing.T) {
	f := &chatFake{}
	useChatFake(t, f)
	var ev chatEvents
	if err := runManagedChat(t.Context(), msgs("q1"), "ip", ev.emit); err != nil {
		t.Fatal(err)
	}
	// The next answer fails: its session is forgotten and deleted.
	f.output = func(int) string { return "" }
	if err := runManagedChat(t.Context(), msgs("q1", "answer 1", "q2"), "ip", ev.emit); err == nil {
		t.Fatal("empty output succeeded")
	}
	deletedEventually(t, f, "sess-1")
	f.output = nil
	if err := runManagedChat(t.Context(), msgs("q1", "answer 1", "q2"), "ip", ev.emit); err != nil {
		t.Fatal(err)
	}
	if sessions, _, _, _ := f.snapshot(); sessions != 2 {
		t.Fatalf("sessions %d", sessions)
	}

	// The same conversation while an answer is running.
	key := chatConversationKey("agent-chat", "ip", "q1")
	chatSessionStore.m[key].busy = true
	if err := runManagedChat(t.Context(), msgs("q1", "answer 3", "q2"), "ip", ev.emit); err != errChatManagedBusy {
		t.Fatalf("busy: %v", err)
	}
}

func TestChatSessionsEvictIdleAndOldest(t *testing.T) {
	s := chatSessions{m: map[string]*chatSession{}}
	now := time.Now()
	if _, _, err := s.claim("a", "", now); err != nil {
		t.Fatal(err)
	}
	s.settle("a", "sess-a", "h1", true, now)
	// Idle past the TTL: evicted for deletion on the next claim.
	id, stale, err := s.claim("b", "", now.Add(chatSessionTTL+time.Minute))
	if err != nil || id != "" || len(stale) != 1 || stale[0] != "sess-a" {
		t.Fatalf("ttl: %q %v %v", id, stale, err)
	}
	s = chatSessions{m: map[string]*chatSession{}}
	for i := 0; i < chatSessionMax; i++ {
		k := fmt.Sprintf("k%d", i)
		s.claim(k, "", now)
		s.settle(k, "sess-"+k, "h", true, now.Add(time.Duration(i)*time.Second))
	}
	_, stale, err = s.claim("new", "", now.Add(time.Hour))
	if err != nil || len(stale) != 1 || stale[0] != "sess-k0" || len(s.m) != chatSessionMax {
		t.Fatalf("max: %v %v len=%d", stale, err, len(s.m))
	}
}

// Live text streams, interim text from a tool step included; tool steps show
// as chips; run.output replaces the text at the end.
func TestManagedChatStreamsTextChipsAndFinalOutput(t *testing.T) {
	running := chubby.ManagedRunDetail{Run: chubby.ManagedRun{ID: "run-1", Status: "running"}, Steps: []chubby.ManagedStep{
		{ID: "m1", Kind: "model", Sequence: 1},
		{ID: "t1", Kind: "tool", Name: chatToolName, Sequence: 2, Arguments: json.RawMessage(`{"command":"search","args":["codex"]}`)},
	}}
	output := "Here is the answer."
	done := running
	done.Run.Status, done.Run.Output = "succeeded", &output
	text := func(step, delta, state string, reset bool) *chubby.RunText {
		return &chubby.RunText{StepID: step, Delta: delta, State: state, Reset: reset, Cursor: step + delta}
	}
	f := &chatFake{script: []chatPage{
		{page: chubby.EventsPage{Text: text("m1", "Let me look", "streaming", false)}},
		{page: chubby.EventsPage{Text: text("m1", " that up.", "tools", false)}},
		{page: chubby.EventsPage{Data: []chubby.ManagedEvent{{ID: 3, Type: "tool.started"}}}, detail: &running},
		{page: chubby.EventsPage{Data: []chubby.ManagedEvent{{ID: 4, Type: "tool.completed"}}}},
		{page: chubby.EventsPage{Text: text("m2", "Draft", "streaming", false)}},
		{page: chubby.EventsPage{Text: text("m2", "", "discarded", false)}},
		{page: chubby.EventsPage{Text: text("m3", "Here is the answer", "answer", false)}},
		{page: chubby.EventsPage{Data: []chubby.ManagedEvent{{ID: 9, Type: "run.succeeded"}}}, detail: &done},
	}}
	useChatFake(t, f)
	var ev chatEvents
	if err := runManagedChat(t.Context(), msgs("q"), "ip", ev.emit); err != nil {
		t.Fatal(err)
	}
	want := "token(Let me look) token( that up.) tool_use(birdy search) token(\n\nDraft) snapshot(Let me look that up.) token(\n\nHere is the answer) snapshot(Here is the answer.)"
	if got := ev.String(); got != want {
		t.Fatalf("events\n got %s\nwant %s", got, want)
	}
}

// Only a stop on the service budget pauses managed chats; an answer that hit
// its own limit fails alone.
func TestManagedChatBudgetBreaker(t *testing.T) {
	stopped := func(required, project float64) *chatFake {
		msg := "Budget cannot reserve the maximum estimated cost of the next model call."
		data, _ := json.Marshal(map[string]float64{"requiredUsd": required, "runAvailableUsd": 0.1, "projectAvailableUsd": project})
		d := chubby.ManagedRunDetail{Run: chubby.ManagedRun{ID: "run-1", Status: "needs_attention", Error: &msg},
			Events: []chubby.ManagedEvent{{ID: 5, Type: "budget.blocked", Data: data}}}
		return &chatFake{script: []chatPage{{page: chubby.EventsPage{Data: []chubby.ManagedEvent{{ID: 5, Type: "run.needs_attention"}}}, detail: &d}}}
	}
	model := chatmodel.Selection{ID: "x"}
	f := stopped(1, 50)
	useChatFake(t, f)
	var ev chatEvents
	err := runManagedChat(t.Context(), msgs("q"), "ip", ev.emit)
	if err == nil || !strings.Contains(err.Error(), "spending limit") {
		t.Fatalf("run budget: %v", err)
	}
	if until := chatManagedBreaker.openUntil(); time.Now().Before(until) {
		t.Fatal("one answer's limit paused every managed chat")
	}
	if f.cancelled.Load() != 1 {
		t.Fatalf("paused run not cancelled: %d", f.cancelled.Load())
	}

	f = stopped(1, 0.2)
	useChatFake(t, f)
	if err := runManagedChat(t.Context(), msgs("q"), "ip", ev.emit); err == nil {
		t.Fatal("service budget stop succeeded")
	}
	old := scanClientModels
	scanClientModels = func() []chatmodel.Selection {
		return []chatmodel.Selection{{ID: "x", Backend: chatmodel.BackendChubby}}
	}
	t.Cleanup(func() { scanClientModels = old })
	model.Backend = chatmodel.BackendChubby
	if reason := chatManagedBlocked(model, time.Now()); !strings.Contains(reason, "paused after a budget stop") {
		t.Fatalf("breaker not tripped: %q", reason)
	}
	if reason := chatManagedBlocked(model, time.Now().Add(2*time.Hour)); reason != "" {
		t.Fatalf("breaker never closes: %q", reason)
	}
	if reason := chatManagedBlocked(chatmodel.Selection{ID: "other", Backend: chatmodel.BackendChubby}, time.Now().Add(2*time.Hour)); reason == "" {
		t.Fatal("another model ran on the agent's model")
	}
}

// Through the handler: the header opts an invited chat in, the response says
// which engine answered, and a blocked trial reports why rather than answering
// directly.
func TestAPIChatManagedTrialRouting(t *testing.T) {
	f := &chatFake{}
	useChatFake(t, f)
	t.Setenv("BIRDY_WEB_CHAT_BACKEND", "chubby")
	t.Setenv("CHUBBY_API_KEY", "chb_fixture")
	t.Setenv("CHUBBY_MODEL", "auto")
	t.Setenv("CHUBBY_CA_FILE", "")
	var direct atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"direct ok\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	t.Setenv("CHUBBY_BASE_URL", upstream.URL+"/v1")
	post := func(visitor bool, engine string) *httptest.ResponseRecorder {
		if visitor {
			t.Setenv("BIRDY_WEB_PUBLIC", "1")
		} else {
			t.Setenv("BIRDY_WEB_PUBLIC", "")
		}
		r := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`))
		r.RemoteAddr = fmt.Sprintf("203.0.113.%d:1000", 10+direct.Load()+int32(len(f.inputs)))
		if !visitor {
			r.Header.Set("X-Invite-Code", "birdy")
		}
		if engine != "" {
			r.Header.Set(chatEngineHeader, engine)
		}
		w := httptest.NewRecorder()
		handleAPIChat("birdy")(w, r)
		return w
	}

	if w := post(false, ""); direct.Load() != 1 || f.runs != 0 || w.Header().Get(chatEngineHeader) != "direct" {
		t.Fatalf("no header: direct=%d runs=%d engine=%q", direct.Load(), f.runs, w.Header().Get(chatEngineHeader))
	}
	if post(true, "managed"); direct.Load() != 2 || f.runs != 0 {
		t.Fatalf("visitor: direct=%d runs=%d", direct.Load(), f.runs)
	}
	w := post(false, "managed")
	if direct.Load() != 2 || f.runs != 1 || w.Header().Get(chatEngineHeader) != "managed" || !strings.Contains(w.Body.String(), "answer 1") || !strings.Contains(w.Body.String(), "event: done") {
		t.Fatalf("managed: direct=%d runs=%d %s", direct.Load(), f.runs, w.Body)
	}
	chatManagedBreaker.trip(time.Now())
	w = post(false, "managed")
	if direct.Load() != 2 || f.runs != 1 || !strings.Contains(w.Body.String(), "managed chat is unavailable: paused") || !strings.Contains(w.Body.String(), "event: done") {
		t.Fatalf("blocked trial: direct=%d runs=%d %s", direct.Load(), f.runs, w.Body)
	}
}

func TestChatBenchReadsTheStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]string `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Header.Get("X-Invite-Code") != "inv" || r.Header.Get(chatEngineHeader) != "managed" || len(body.Messages) != 3 {
			http.Error(w, "bad", 400)
			return
		}
		w.Header().Set(chatEngineHeader, "managed")
		for _, ev := range []string{`{"type":"token","text":"Hel"}`, `{"type":"tool_use","command":"birdy search"}`, `{"type":"token","text":"lo"}`, `{"type":"snapshot","text":"Hello."}`, `{"type":"done"}`} {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", ev)
		}
	}))
	defer srv.Close()
	turns := []map[string]string{{"role": "user", "content": "a"}, {"role": "assistant", "content": "b"}, {"role": "user", "content": "c"}}
	r := runChatBench(t.Context(), srv.URL, "inv", "managed", 1, turns)
	if r.Error != "" || r.Turn != 2 || r.Answered != "managed" || r.Text != "Hello." || r.ToolRounds != 1 || r.FirstToken <= 0 || r.Done < r.FirstToken {
		t.Fatalf("%+v", r)
	}
}
