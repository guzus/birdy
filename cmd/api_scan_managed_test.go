package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/chatmodel"
	"github.com/guzus/birdy/internal/chubby"
	"github.com/guzus/birdy/internal/claude"
)

const testToolSecret = "0123456789abcdef0123456789abcdef-secret"

func toolRequest(t *testing.T, kind scanToolKind, read scanReader, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/scan-tools/x", strings.NewReader(body))
	r.RemoteAddr = "198.51.100.7:1234"
	if secret != "" {
		r.Header.Set(scanToolHeader, secret)
	}
	w := httptest.NewRecorder()
	handleScanTool(kind, read)(w, r)
	return w
}

func resetScanToolState() {
	scanToolResults = scanToolCache{entries: map[string]scanToolCacheEntry{}}
	scanToolLimiter = newRateLimiter(1_000_000, time.Minute)
}

func TestScanToolIsAbsentUntilEnabledAndKeyed(t *testing.T) {
	resetScanToolState()
	scanToolResults = scanToolCache{entries: map[string]scanToolCacheEntry{}}
	reads := 0
	read := func(_ context.Context, s scanSource) ([]scanPost, error) {
		reads++
		return []scanPost{{ID: "news:1", URL: "https://x.com/i/trending/1", Text: s.key + " headline"}}, nil
	}
	t.Setenv(scanManagedModeEnv, "")
	t.Setenv(publicScanTool.secretEnv, testToolSecret)
	if w := toolRequest(t, publicScanTool, read, testToolSecret, `{"tab":"news"}`); w.Code != http.StatusNotFound {
		t.Fatalf("flag off: %d", w.Code)
	}
	t.Setenv(scanManagedModeEnv, "trial")
	t.Setenv(publicScanTool.secretEnv, "short")
	if w := toolRequest(t, publicScanTool, read, "short", `{"tab":"news"}`); w.Code != http.StatusNotFound {
		t.Fatalf("weak secret: %d", w.Code)
	}
	t.Setenv(publicScanTool.secretEnv, testToolSecret)
	for name, tc := range map[string]struct {
		secret, body string
		code         int
	}{
		"no key":          {"", `{"tab":"news"}`, http.StatusUnauthorized},
		"wrong key":       {testToolSecret + "x", `{"tab":"news"}`, http.StatusUnauthorized},
		"other plan":      {testToolSecret, `{"tab":"home"}`, http.StatusBadRequest},
		"free text":       {testToolSecret, `{"tab":"from:someone"}`, http.StatusBadRequest},
		"extra argument":  {testToolSecret, `{"tab":"news","query":"x"}`, http.StatusBadRequest},
		"wrong arg name":  {testToolSecret, `{"source":"news"}`, http.StatusBadRequest},
		"not json object": {testToolSecret, `["news"]`, http.StatusBadRequest},
	} {
		if w := toolRequest(t, publicScanTool, read, tc.secret, tc.body); w.Code != tc.code {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if reads != 0 {
		t.Fatalf("refused requests read X %d times", reads)
	}
	w := toolRequest(t, publicScanTool, read, testToolSecret, `{"tab":"news"}`)
	var got scanToolResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Source != "news" || got.Status != "ok" || len(got.Posts) != 1 {
		t.Fatalf("read: %d %s", w.Code, w.Body)
	}
	toolRequest(t, publicScanTool, read, testToolSecret, `{"tab":"news"}`)
	if reads != 1 {
		t.Fatalf("a repeated read within a minute reached X: %d", reads)
	}
	// The invited tool has its own secret and plan.
	if w := toolRequest(t, invitedScanTool, read, testToolSecret, `{"source":"home"}`); w.Code != http.StatusNotFound {
		t.Fatalf("invited tool without its own secret: %d", w.Code)
	}
}

func TestScanToolHidesReadErrorsAndBoundsOutput(t *testing.T) {
	resetScanToolState()
	scanToolResults = scanToolCache{entries: map[string]scanToolCacheEntry{}}
	t.Setenv(scanManagedModeEnv, "on")
	t.Setenv(invitedScanTool.secretEnv, testToolSecret)
	failing := func(context.Context, scanSource) ([]scanPost, error) {
		return nil, errors.New("account alice is rate limited")
	}
	w := toolRequest(t, invitedScanTool, failing, testToolSecret, `{"source":"home"}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "alice") || !strings.Contains(w.Body.String(), `"status":"error"`) {
		t.Fatalf("error result %d %s", w.Code, w.Body)
	}
	big := func(context.Context, scanSource) ([]scanPost, error) {
		var posts []scanPost
		for i := range 200 {
			posts = append(posts, scanPost{ID: strings.Repeat("1", 10) + string(rune('0'+i%10)), Text: strings.Repeat("y", 640)})
		}
		return posts, nil
	}
	w = toolRequest(t, invitedScanTool, big, testToolSecret, `{"source":"ai"}`)
	if w.Code != 200 || w.Body.Len() > 64<<10 {
		t.Fatalf("output %d bytes", w.Body.Len())
	}
}

func TestScanAgentDefinitionsUseOnlySupportedSchema(t *testing.T) {
	for _, kind := range []scanToolKind{publicScanTool, invitedScanTool} {
		tool, agent := scanAgentDefinitions(kind, "https://birdy.example/api/scan-tools/x", "anthropic/model", "secret-name", "tool-1", scanAgentLimits{6, 4096, 180, 5})
		data, _ := json.Marshal(tool)
		for _, forbidden := range []string{`"pattern"`, `"format"`, `"$ref"`} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("%s schema uses %s", kind.name, forbidden)
			}
		}
		if tool["behavior"] != "read_only" || tool["approvalRequired"] != false {
			t.Fatal(tool)
		}
		enum := kind.parameters()["properties"].(map[string]any)[kind.argName].(map[string]any)["enum"].([]string)
		if strings.Join(enum, ",") != strings.Join(kind.keys(), ",") {
			t.Fatal(enum)
		}
		system := agent["system"].(string)
		if !strings.Contains(system, kind.name) || !strings.Contains(system, "untrusted") || agent["maxSteps"] != 6 {
			t.Fatal(agent)
		}
	}
	if strings.Join(publicScanTool.keys(), ",") != "news,trending,sports,entertainment" || strings.Join(invitedScanTool.keys(), ",") != "home,crypto,ai,news" {
		t.Fatal("tool plans drifted from the direct briefing's plans")
	}
}

func TestWantsManagedScan(t *testing.T) {
	t.Setenv(scanAgentPublicEnv, "agent-public")
	t.Setenv(scanAgentInvitedEnv, "agent-invited")
	t.Setenv(publicScanTool.secretEnv, testToolSecret+"-public")
	t.Setenv(invitedScanTool.secretEnv, testToolSecret+"-invited")
	for _, tc := range []struct {
		mode    string
		visitor bool
		header  string
		want    bool
	}{
		{"", false, "managed", false},
		{"trial", false, "", false},
		{"trial", false, "managed", true},
		{"trial", true, "managed", false},
		{"on", true, "", true},
		{"on", false, "", true},
		{"yes", false, "managed", false},
	} {
		t.Setenv(scanManagedModeEnv, tc.mode)
		if got := wantsManagedScan(tc.visitor, tc.header); got != tc.want {
			t.Errorf("%+v: %v", tc, got)
		}
	}
	t.Setenv(scanManagedModeEnv, "on")
	t.Setenv(invitedScanTool.secretEnv, "")
	if wantsManagedScan(false, "") {
		t.Fatal("managed without the tool's secret")
	}
	t.Setenv(invitedScanTool.secretEnv, testToolSecret+"-public")
	if wantsManagedScan(false, "") || wantsManagedScan(true, "") || publicScanTool.toolSecret() != "" {
		t.Fatal("one secret shared by both tools was accepted")
	}
	t.Setenv(scanAgentPublicEnv, "")
	if wantsManagedScan(true, "") {
		t.Fatal("managed without an agent")
	}
}

type fakeManaged struct {
	detail      chubby.ManagedRunDetail
	startErrs   []error
	keys        []string
	sessionErr  error
	cancelled   atomic.Int32
	runCalls    atomic.Int32
	blockEvents chan struct{}
	detailFn    func() chubby.ManagedRunDetail
	pageMu      sync.Mutex
	pages       []chubby.EventsPage
	onPage      func(remaining int)
}

func (f *fakeManaged) CreateSession(context.Context, string, string) (chubby.ManagedSession, error) {
	return chubby.ManagedSession{ID: "sess"}, f.sessionErr
}
func (f *fakeManaged) StartRun(_ context.Context, _, _, key string) (chubby.ManagedRun, error) {
	f.keys = append(f.keys, key)
	if len(f.startErrs) > 0 {
		err := f.startErrs[0]
		f.startErrs = f.startErrs[1:]
		return chubby.ManagedRun{}, err
	}
	return chubby.ManagedRun{ID: "run-1", Status: "queued"}, nil
}
func (f *fakeManaged) Run(context.Context, string) (chubby.ManagedRunDetail, error) {
	f.runCalls.Add(1)
	if f.detailFn != nil {
		return f.detailFn(), nil
	}
	return f.detail, nil
}
func (f *fakeManaged) Events(ctx context.Context, _ string, after float64) ([]chubby.ManagedEvent, error) {
	if f.blockEvents != nil {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if after > 0 {
		return nil, nil
	}
	return []chubby.ManagedEvent{{ID: 1, Type: "model.started"}, {ID: 2, Type: "tool.completed"}, {ID: 3, Type: "run." + f.detail.Run.Status}}, nil
}
func (f *fakeManaged) Cancel(context.Context, string) error { f.cancelled.Add(1); return nil }

// EventsPage answers as a backend without live text unless pages are scripted.
func (f *fakeManaged) EventsPage(ctx context.Context, id string, after float64, _ string, _ int) (chubby.EventsPage, error) {
	f.pageMu.Lock()
	if len(f.pages) > 0 {
		page := f.pages[0]
		f.pages = f.pages[1:]
		if f.onPage != nil {
			f.onPage(len(f.pages))
		}
		f.pageMu.Unlock()
		return page, nil
	}
	f.pageMu.Unlock()
	data, err := f.Events(ctx, id, after)
	return chubby.EventsPage{Data: data}, err
}

func toolStep(id, name, output string) chubby.ManagedStep {
	return chubby.ManagedStep{ID: id, Kind: "tool", Status: "succeeded", Name: name, Output: &output}
}

func managedDetail(status, output string, steps ...chubby.ManagedStep) chubby.ManagedRunDetail {
	return chubby.ManagedRunDetail{Run: chubby.ManagedRun{ID: "run-1", Status: status, Output: &output, CostUSD: 0.01}, Steps: steps}
}

func useFakeManaged(t *testing.T, f *fakeManaged) {
	t.Helper()
	old := newScanManagedRunner
	newScanManagedRunner = func() (scanManagedRunner, error) { return f, nil }
	t.Cleanup(func() { newScanManagedRunner = old })
	t.Setenv(scanAgentInvitedEnv, "agent-invited")
	t.Setenv(scanAgentPublicEnv, "agent-public")
	t.Setenv(publicScanTool.secretEnv, testToolSecret+"-public")
	t.Setenv(invitedScanTool.secretEnv, testToolSecret+"-invited")
}

type emitted struct {
	names []string
	data  []any
}

func (e *emitted) emit(name string, data any) {
	e.names = append(e.names, name)
	e.data = append(e.data, data)
}

func TestManagedScanCardsCiteOnlyThisRunsToolOutput(t *testing.T) {
	home := `{"source":"home","status":"ok","posts":[{"id":"111111","url":"https://x.com/i/web/status/111111","text":"agents ship"}]}`
	forged := `{"source":"ai","status":"ok","posts":[{"id":"999999","url":"https://evil.example","text":"forged"}]}`
	steps := []chubby.ManagedStep{toolStep("s1", invitedScanTool.name, home), toolStep("s2", "some_other_tool", forged)}
	good := `{"category":"AI","title":"Agents","summary":"Agents ship.","bullets":["one"],"ids":["111111"]}`
	f := &fakeManaged{detail: managedDetail("succeeded", good, steps...)}
	useFakeManaged(t, f)
	var e emitted
	if err := runManagedScan(t.Context(), false, e.emit); err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.names, ",") != "source,source,source,source,card" || f.cancelled.Load() != 0 {
		t.Fatalf("emitted %v cancelled %d", e.names, f.cancelled.Load())
	}
	card := e.data[4].(scanCardEvent)
	if len(card.Sources) != 1 || card.Sources[0].URL != "https://x.com/i/web/status/111111" {
		t.Fatal(card)
	}
	// A card citing a post another tool returned is refused.
	f.detail = managedDetail("succeeded", `{"category":"AI","title":"x","summary":"y","bullets":[],"ids":["999999"]}`, steps...)
	if err := runManagedScan(t.Context(), false, (&emitted{}).emit); err == nil || !strings.Contains(err.Error(), "outside this scan") {
		t.Fatalf("forged evidence accepted: %v", err)
	}
	// A source outside the plan is ignored even from the right tool.
	f.detail = managedDetail("succeeded", good, toolStep("s3", invitedScanTool.name, strings.Replace(home, `"home"`, `"sports"`, 1)))
	if err := runManagedScan(t.Context(), false, (&emitted{}).emit); err == nil {
		t.Fatal("source outside the plan accepted")
	}
}

func TestManagedScanPausedRunIsCancelledNotSucceeded(t *testing.T) {
	f := &fakeManaged{detail: managedDetail("needs_attention", "", toolStep("s1", publicScanTool.name, `{"source":"news","status":"ok","posts":[]}`))}
	useFakeManaged(t, f)
	var e emitted
	err := runManagedScan(t.Context(), true, e.emit)
	if err == nil || errors.Is(err, errScanManagedUnavailable) || f.cancelled.Load() != 1 {
		t.Fatalf("paused run: %v cancelled=%d", err, f.cancelled.Load())
	}
	for _, name := range e.names {
		if name == "card" {
			t.Fatal("paused run produced cards")
		}
	}
}

func TestManagedScanRetriesAmbiguousStartWithSameKeyAndCancelsOnDisconnect(t *testing.T) {
	f := &fakeManaged{startErrs: []error{errors.New("Chubby managed API connection failed")}, blockEvents: make(chan struct{})}
	useFakeManaged(t, f)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := runManagedScan(ctx, false, (&emitted{}).emit)
	if len(f.keys) != 2 || f.keys[0] != f.keys[1] || !strings.HasPrefix(f.keys[0], "birdy-scan-") {
		t.Fatalf("keys %v", f.keys)
	}
	if !errors.Is(err, context.DeadlineExceeded) || f.cancelled.Load() != 1 {
		t.Fatalf("disconnect: %v cancelled=%d", err, f.cancelled.Load())
	}
	// A refused submission is not retried and falls back to the direct briefing.
	f2 := &fakeManaged{startErrs: []error{&chubby.APIError{Status: 402, Code: "budget"}}}
	useFakeManaged(t, f2)
	if err := runManagedScan(t.Context(), false, (&emitted{}).emit); !errors.Is(err, errScanManagedUnavailable) || len(f2.keys) != 1 {
		t.Fatalf("refused start: %v keys=%d", err, len(f2.keys))
	}
}

func TestManagedScanFallsBackWhenFull(t *testing.T) {
	useFakeManaged(t, &fakeManaged{})
	for range cap(scanManagedSlots) {
		scanManagedSlots <- struct{}{}
	}
	defer func() {
		for range cap(scanManagedSlots) {
			<-scanManagedSlots
		}
	}()
	if err := runManagedScan(t.Context(), false, (&emitted{}).emit); !errors.Is(err, errScanManagedUnavailable) {
		t.Fatal(err)
	}
}

func TestAPIScanTrialUsesManagedOnlyWhenAsked(t *testing.T) {
	t.Setenv("BIRDY_WEB_CHAT_BACKEND", "chubby")
	t.Setenv("CHUBBY_API_KEY", "chb_fixture")
	t.Setenv("CHUBBY_MODEL", "auto")
	t.Setenv(scanManagedModeEnv, "trial")
	home := `{"source":"home","status":"ok","posts":[{"id":"111111","url":"https://x.com/i/web/status/111111","text":"agents ship"}]}`
	f := &fakeManaged{detail: managedDetail("succeeded", `{"category":"AI","title":"Agents","summary":"Agents ship.","bullets":[],"ids":["111111"]}`, toolStep("s1", invitedScanTool.name, home))}
	useFakeManaged(t, f)
	var directReads atomic.Int32 // the direct briefing reads sources concurrently
	read := func(context.Context, scanSource) ([]scanPost, error) {
		directReads.Add(1)
		return nil, errors.New("direct")
	}
	stream := func(context.Context, chatmodel.Selection, chatmodel.Request, func(claude.Event)) {}
	for _, managed := range []bool{true, false} {
		r := httptest.NewRequest(http.MethodPost, "/api/scan", strings.NewReader("{}"))
		r.Header.Set("X-Invite-Code", "birdy")
		r.RemoteAddr = "203.0.113.50:1"
		if managed {
			r.Header.Set("X-Birdy-Scan-Engine", "managed")
		}
		w := httptest.NewRecorder()
		apiScanHandler("birdy", read, stream)(w, r)
		body := w.Body.String()
		if managed != strings.Contains(body, "event: card") || !strings.Contains(body, "event: done") {
			t.Fatalf("managed=%v body=%s", managed, body)
		}
	}
	if int(directReads.Load()) != len(scanSources) || f.runCalls.Load() == 0 {
		t.Fatalf("direct reads %d, managed detail reads %d", directReads.Load(), f.runCalls.Load())
	}
}

func TestAPIScanTrialNeverFallsBackSilently(t *testing.T) {
	t.Setenv("BIRDY_WEB_CHAT_BACKEND", "chubby")
	t.Setenv("CHUBBY_API_KEY", "chb_fixture")
	t.Setenv("CHUBBY_MODEL", "auto")
	f := &fakeManaged{sessionErr: errors.New("no such agent")}
	useFakeManaged(t, f)
	var directReads atomic.Int32
	read := func(context.Context, scanSource) ([]scanPost, error) {
		directReads.Add(1)
		return nil, errors.New("direct")
	}
	stream := func(context.Context, chatmodel.Selection, chatmodel.Request, func(claude.Event)) {}
	for _, mode := range []string{"trial", "on"} {
		t.Setenv(scanManagedModeEnv, mode)
		directReads.Store(0)
		r := httptest.NewRequest(http.MethodPost, "/api/scan", strings.NewReader("{}"))
		r.Header.Set("X-Invite-Code", "birdy")
		r.Header.Set("X-Birdy-Scan-Engine", "managed")
		r.RemoteAddr = "203.0.113.51:1"
		w := httptest.NewRecorder()
		apiScanHandler("birdy", read, stream)(w, r)
		fellBack := directReads.Load() > 0
		if mode == "trial" && (fellBack || !strings.Contains(w.Body.String(), "managed briefing could not start")) {
			t.Fatalf("trial fell back: %s", w.Body)
		}
		if mode == "on" && !fellBack {
			t.Fatal("on did not fall back to the direct briefing")
		}
	}
}

func TestServerTimelineUsesChubbyTimestamps(t *testing.T) {
	ev := func(id float64, typ, at, data string) chubby.ManagedEvent {
		return chubby.ManagedEvent{ID: id, Type: typ, CreatedAt: at, Data: json.RawMessage(data)}
	}
	got, reserve := serverTimeline([]chubby.ManagedEvent{
		ev(1, "run.queued", "2026-11-18T07:00:00.000Z", `{}`),
		ev(2, "run.claimed", "2026-11-18T07:00:01.250Z", `{}`),
		ev(3, "model.started", "2026-11-18T07:00:01.300Z", `{"reservedUsd":2.04096}`),
		ev(4, "model.started", "2026-11-18T07:00:09.000Z", `{"reservedUsd":2.04096}`),
		ev(5, "run.succeeded", "2026-11-18T07:00:20.000Z", `{}`),
	}, "2026-11-18T07:00:12.500Z")
	if got != "run.queued=0ms run.claimed=1250ms model.started=1300ms run.succeeded=20000ms first_text=12500ms" || reserve != 2.04096 {
		t.Fatalf("%q %g", got, reserve)
	}
}

func TestManagedScanNeverFallsBackWhenARunMayExist(t *testing.T) {
	refusal := func(status int) error { return &chubby.APIError{Status: status, Code: "x"} }
	for _, tc := range []struct {
		name     string
		errs     []error
		attempts int
		fallback bool
	}{
		{"refused first", []error{refusal(429)}, 1, true},
		{"5xx then refused replay", []error{refusal(502), refusal(409)}, 2, false},
		{"transport then refused replay", []error{errors.New("Chubby managed API connection failed"), refusal(429)}, 2, false},
		{"redirect then refused replay", []error{refusal(302), refusal(404)}, 2, false},
		{"5xx twice", []error{refusal(503), refusal(503)}, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeManaged{startErrs: tc.errs}
			useFakeManaged(t, f)
			err := runManagedScan(t.Context(), false, (&emitted{}).emit)
			if len(f.keys) != tc.attempts || errors.Is(err, errScanManagedUnavailable) != tc.fallback || err == nil {
				t.Fatalf("attempts %d err %v", len(f.keys), err)
			}
			if tc.attempts == 2 && f.keys[0] != f.keys[1] {
				t.Fatal("the replay changed its key")
			}
		})
	}
}

func TestScanToolCacheSharesReadsAndHoldsFailures(t *testing.T) {
	c := scanToolCache{entries: map[string]scanToolCacheEntry{}}
	src := scanSource{key: "home"}
	now := time.Unix(1000, 0)
	var reads atomic.Int32
	release := make(chan struct{})
	slow := func(context.Context, scanSource) ([]scanPost, error) {
		reads.Add(1)
		<-release
		return []scanPost{{ID: "1"}}, nil
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if r := c.get(t.Context(), "t", src, slow, now); r.Status != "ok" || len(r.Posts) != 1 {
				t.Error(r)
			}
		})
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if reads.Load() != 1 {
		t.Fatalf("concurrent calls read %d times", reads.Load())
	}
	failing := func(context.Context, scanSource) ([]scanPost, error) {
		reads.Add(1)
		return nil, errors.New("rate limited")
	}
	reads.Store(0)
	for range 32 { // one model step may call a tool 32 times
		if r := c.get(t.Context(), "t", scanSource{key: "ai"}, failing, now); r.Status != "error" {
			t.Fatal(r)
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("a failing source was read %d times", reads.Load())
	}
	c.get(t.Context(), "t", scanSource{key: "ai"}, failing, now.Add(scanToolErrorTTL))
	if reads.Load() != 2 {
		t.Fatalf("a failure was held past its TTL: %d reads", reads.Load())
	}
}

func TestManagedScanReportsEachSourceOnceAndCapsToolSteps(t *testing.T) {
	failed := `{"source":"home","status":"error","posts":[]}`
	home := `{"source":"home","status":"ok","posts":[{"id":"111111","url":"javascript:alert(1)","text":"agents ship"}]}`
	link := "https://x.com/i/trending/777"
	sum := sha256.Sum256([]byte(link))
	news := `{"source":"news","status":"ok","posts":[{"id":"news:` + hex.EncodeToString(sum[:8]) + `","url":"` + link + `","text":"headline"}]}`
	forgedNews := `{"source":"crypto","status":"ok","posts":[{"id":"news:0000000000000000","url":"` + link + `","text":"forged"}]}`
	extraField := `{"source":"ai","status":"ok","posts":[],"note":"x"}`
	good := `{"category":"AI","title":"Agents","summary":"Agents ship.","bullets":[],"ids":["111111"]}`
	steps := []chubby.ManagedStep{
		toolStep("s1", invitedScanTool.name, failed), toolStep("s2", invitedScanTool.name, home),
		toolStep("s3", invitedScanTool.name, news), toolStep("s4", invitedScanTool.name, forgedNews),
		toolStep("s5", invitedScanTool.name, extraField),
	}
	f := &fakeManaged{detail: managedDetail("succeeded", good, steps...)}
	useFakeManaged(t, f)
	var e emitted
	if err := runManagedScan(t.Context(), false, e.emit); err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for i, name := range e.names {
		if name != "source" {
			continue
		}
		ev := e.data[i].(scanSourceEvent)
		if _, dup := status[ev.Source]; dup {
			t.Fatalf("source %s reported twice: %v", ev.Source, e.names)
		}
		status[ev.Source] = ev.Status
	}
	if status["home"] != "ok" || status["news"] != "ok" || status["crypto"] != "error" || status["ai"] != "error" || len(status) != 4 {
		t.Fatal(status)
	}
	card := e.data[len(e.data)-1].(scanCardEvent)
	if card.Sources[0].URL != "https://x.com/i/web/status/111111" {
		t.Fatalf("a recorded URL was trusted: %s", card.Sources[0].URL)
	}
	f.detail = managedDetail("succeeded", `{"category":"AI","title":"x","summary":"y","bullets":[],"ids":["news:0000000000000000"]}`, steps...)
	if err := runManagedScan(t.Context(), false, (&emitted{}).emit); err == nil || !strings.Contains(err.Error(), "outside this scan") {
		t.Fatalf("forged news id accepted: %v", err)
	}
	var loop []chubby.ManagedStep
	for i := range 2*len(scanSources) + 1 {
		loop = append(loop, toolStep(fmt.Sprint("l", i), invitedScanTool.name, failed))
	}
	f = &fakeManaged{detail: managedDetail("running", "", loop...)}
	useFakeManaged(t, f)
	e = emitted{}
	err := runManagedScan(t.Context(), false, e.emit)
	if err == nil || !strings.Contains(err.Error(), "too many source reads") || f.cancelled.Load() != 1 || strings.Count(strings.Join(e.names, ","), "source") != 4 {
		t.Fatalf("looping agent: %v cancelled=%d events=%v", err, f.cancelled.Load(), e.names)
	}
}

func TestScanToolMarksASourceAlreadyReturnedInTheSameRun(t *testing.T) {
	resetScanToolState()
	t.Setenv(scanManagedModeEnv, "trial")
	t.Setenv(invitedScanTool.secretEnv, testToolSecret)
	home := `{"source":"home","status":"ok","posts":[{"id":"111111","url":"https://x.com/i/web/status/111111","text":"agents ship"}]}`
	first := toolStep("s1", invitedScanTool.name, home)
	first.Sequence = 2
	calling := chubby.ManagedStep{ID: "s2", Sequence: 4, Kind: "tool", Status: "running", Name: invitedScanTool.name}
	f := &fakeManaged{detail: managedDetail("running", "", first, calling)}
	useFakeManaged(t, f)
	t.Setenv(invitedScanTool.secretEnv, testToolSecret)
	scanManagedActive.add("run-1")
	t.Cleanup(func() { scanManagedActive.remove("run-1") })
	reads := 0
	read := func(context.Context, scanSource) ([]scanPost, error) {
		reads++
		return []scanPost{{ID: "222222", URL: "https://x.com/i/web/status/222222", Text: "fresh"}}, nil
	}
	call := func(step, body string) scanToolResult {
		r := httptest.NewRequest(http.MethodPost, "/api/scan-tools/invited", strings.NewReader(body))
		r.Header.Set(scanToolHeader, testToolSecret)
		r.Header.Set("Idempotency-Key", step)
		w := httptest.NewRecorder()
		handleScanTool(invitedScanTool, read)(w, r)
		var got scanToolResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		return got
	}
	if got := call("s2", `{"source":"home"}`); got.Status != "already_returned" || len(got.Posts) != 0 || reads != 0 {
		t.Fatalf("repeat in one run: %+v reads=%d", got, reads)
	}
	if got := call("s2", `{"source":"ai"}`); got.Status != "ok" || reads != 1 {
		t.Fatalf("a new source in the run: %+v", got)
	}
	if got := call("chst_other_run", `{"source":"home"}`); got.Status != "ok" {
		t.Fatalf("a step of no active run: %+v", got)
	}
	// The marker is never evidence.
	if _, ok := parseScanToolResult(`{"source":"home","status":"already_returned","posts":[]}`, invitedScanTool); ok {
		t.Fatal("already_returned parsed as a source result")
	}
}

type flakyEvents struct {
	*fakeManaged
	failures []error
}

func (f *flakyEvents) EventsPage(ctx context.Context, id string, after float64, _ string, _ int) (chubby.EventsPage, error) {
	data, err := f.Events(ctx, id, after)
	return chubby.EventsPage{Data: data}, err
}

func (f *flakyEvents) Events(ctx context.Context, id string, after float64) ([]chubby.ManagedEvent, error) {
	if len(f.failures) > 0 {
		err := f.failures[0]
		f.failures = f.failures[1:]
		return nil, err
	}
	return f.fakeManaged.Events(ctx, id, after)
}

func TestManagedScanRetriesATransientEventsReadOnly(t *testing.T) {
	home := `{"source":"home","status":"ok","posts":[{"id":"111111","url":"https://x.com/i/web/status/111111","text":"agents ship"}]}`
	good := `{"category":"AI","title":"Agents","summary":"Agents ship.","bullets":[],"ids":["111111"]}`
	for name, tc := range map[string]struct {
		failures []error
		ok       bool
	}{
		"dropped connection": {[]error{errors.New("Chubby managed API connection failed")}, true},
		"two 503s":           {[]error{&chubby.APIError{Status: 503}, &chubby.APIError{Status: 502}}, true},
		"run not found":      {[]error{&chubby.APIError{Status: 404}}, false},
		"three failures":     {[]error{errors.New("x"), errors.New("x"), errors.New("x")}, false},
	} {
		t.Run(name, func(t *testing.T) {
			f := &flakyEvents{fakeManaged: &fakeManaged{detail: managedDetail("succeeded", good, toolStep("s1", invitedScanTool.name, home))}, failures: tc.failures}
			old := newScanManagedRunner
			newScanManagedRunner = func() (scanManagedRunner, error) { return f, nil }
			t.Cleanup(func() { newScanManagedRunner = old })
			t.Setenv(scanAgentInvitedEnv, "agent-invited")
			err := runManagedScan(t.Context(), false, (&emitted{}).emit)
			if (err == nil) != tc.ok {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestManagedScanRefusesAnotherModelAndPausesAfterABudgetStop(t *testing.T) {
	t.Setenv("BIRDY_WEB_CHAT_BACKEND", "chubby")
	t.Setenv("CHUBBY_API_KEY", "chb_fixture")
	t.Setenv("CHUBBY_MODEL", "anthropic/claude-sonnet-5-5")
	t.Setenv("BIRDY_SCAN_MANAGED_BREAKER_MINUTES", "10")
	t.Cleanup(func() { scanManagedBreaker = breaker{} })
	now := time.Now()
	def := chatmodel.ClientModels()[0]
	if reason := scanManagedBlocked(def, now); reason != "" {
		t.Fatalf("default model refused: %s", reason)
	}
	if reason := scanManagedBlocked(chatmodel.Selection{ID: "codex", Backend: chatmodel.BackendCodex}, now); !strings.Contains(reason, "not the managed agent's model") {
		t.Fatalf("other model: %q", reason)
	}
	old := scanClientModels
	scanClientModels = func() []chatmodel.Selection { return nil }
	if reason := scanManagedBlocked(def, now); reason != "no default Chubby model" {
		t.Fatalf("empty model list: %q", reason)
	}
	scanClientModels = old

	budget := "Budget cannot reserve the maximum estimated cost of the next model call."
	paused := managedDetail("needs_attention", "")
	paused.Run.Error = &budget
	paused.Events = []chubby.ManagedEvent{{ID: 9, Type: "budget.blocked"}}
	f := &fakeManaged{detail: paused}
	useFakeManaged(t, f)
	if err := runManagedScan(t.Context(), false, (&emitted{}).emit); err == nil || f.cancelled.Load() != 1 {
		t.Fatalf("budget pause: %v cancelled=%d", err, f.cancelled.Load())
	}
	reason := scanManagedBlocked(def, now.Add(time.Minute))
	if !strings.Contains(reason, "paused after a budget stop") {
		t.Fatalf("breaker not open: %q", reason)
	}
	if scanManagedBlocked(def, now.Add(11*time.Minute)) != "" {
		t.Fatal("breaker did not close after its window")
	}
	// A trial request is told why, never given a direct briefing.
	t.Setenv(scanManagedModeEnv, "trial")
	t.Setenv(invitedScanTool.secretEnv, testToolSecret)
	var directReads atomic.Int32
	read := func(context.Context, scanSource) ([]scanPost, error) {
		directReads.Add(1)
		return nil, errors.New("direct")
	}
	r := httptest.NewRequest(http.MethodPost, "/api/scan", strings.NewReader("{}"))
	r.Header.Set("X-Invite-Code", "birdy")
	r.Header.Set("X-Birdy-Scan-Engine", "managed")
	r.RemoteAddr = "203.0.113.52:1"
	w := httptest.NewRecorder()
	apiScanHandler("birdy", read, func(context.Context, chatmodel.Selection, chatmodel.Request, func(claude.Event)) {})(w, r)
	if directReads.Load() != 0 || !strings.Contains(w.Body.String(), "paused after a budget stop") {
		t.Fatalf("trial during the pause: reads=%d %s", directReads.Load(), w.Body)
	}
	// A budget-free pause (an uncertain tool) does not trip it.
	scanManagedBreaker = breaker{}
	other := managedDetail("needs_attention", "")
	if budgetPaused(other) {
		t.Fatal("a non-budget pause tripped the breaker")
	}
}

// A live-text scenario: one model step that calls the tool, the tool step,
// then the answer step whose text arrives in pages.
type liveScenario struct {
	f        *fakeManaged
	log      []string // emitted names, plus "TERMINAL" when the run is read finished
	terminal bool
}

const (
	liveCard1 = `{"category":"AI","title":"Agents","summary":"Agents ship.","bullets":[],"ids":["111111"]}`
	liveCard2 = `{"category":"CRYPTO","title":"Stables","summary":"Supply grew.","bullets":[],"ids":["222222"]}`
)

func liveText(step, cursor, delta, state string, reset bool) *chubby.RunText {
	return &chubby.RunText{StepID: step, Attempt: 1, Cursor: cursor, Delta: delta, State: state, Reset: reset}
}

func liveEvent(id float64, typ, data string) chubby.ManagedEvent {
	return chubby.ManagedEvent{ID: id, Type: typ, Data: json.RawMessage(data)}
}

func newLiveScenario(t *testing.T, output string, pages ...chubby.EventsPage) *liveScenario {
	t.Helper()
	home := `{"source":"home","status":"ok","posts":[{"id":"111111","url":"https://x.com/i/web/status/111111","text":"agents ship"},{"id":"222222","url":"https://x.com/i/web/status/222222","text":"stables grew"}]}`
	sc := &liveScenario{}
	tool := toolStep("s1", invitedScanTool.name, home)
	tool.Sequence = 2
	running := chubby.ManagedRunDetail{Run: chubby.ManagedRun{ID: "run-1", Status: "running"}, Steps: []chubby.ManagedStep{
		{ID: "m1", Sequence: 1, Kind: "model", Status: "succeeded"}, tool, {ID: "m2", Sequence: 3, Kind: "model", Status: "running"}}}
	done := running
	done.Run = chubby.ManagedRun{ID: "run-1", Status: "succeeded", Output: &output}
	done.Steps = append([]chubby.ManagedStep{}, running.Steps...)
	done.Steps[2].Status = "succeeded"
	sc.f = &fakeManaged{pages: pages, detailFn: func() chubby.ManagedRunDetail {
		if sc.terminal {
			sc.log = append(sc.log, "TERMINAL")
			return done
		}
		return running
	}}
	sc.f.onPage = func(remaining int) {
		if remaining == 0 {
			sc.terminal = true
		}
	}
	sc.f.detail = done // what the old-backend fallback reads once pages run out
	useFakeManaged(t, sc.f)
	return sc
}

func (sc *liveScenario) emit(name string, _ any) { sc.log = append(sc.log, name) }

func (sc *liveScenario) cards() int {
	n := 0
	for _, name := range sc.log {
		if name == "card" {
			n++
		}
	}
	return n
}

func livePage(text *chubby.RunText, events ...chubby.ManagedEvent) chubby.EventsPage {
	return chubby.EventsPage{Data: events, Text: text, LiveText: true}
}

func TestManagedScanStreamsCardsBeforeTheRunEnds(t *testing.T) {
	output := liveCard1 + "\n" + liveCard2
	sc := newLiveScenario(t, output,
		livePage(nil, liveEvent(1, "model.started", `{"stepId":"m1"}`), liveEvent(2, "tool.completed", `{"stepId":"s1"}`)),
		// The tool-calling step's own text is never parsed.
		livePage(liveText("m1", "m1:1:t", "Let me read the sources.", "tools", true)),
		livePage(liveText("m2", "m2:1:s", liveCard1+"\n"+liveCard2[:20], "streaming", true), liveEvent(3, "model.started", `{"stepId":"m2"}`)),
		livePage(liveText("m2", "m2:2:a", liveCard2[20:], "answer", false)),
		livePage(nil, liveEvent(4, "model.completed", `{"stepId":"m2","answer":true}`), liveEvent(5, "run.succeeded", `{}`)),
	)
	if err := runManagedScan(t.Context(), false, sc.emit); err != nil {
		t.Fatal(err)
	}
	first := slices.Index(sc.log, "card")
	terminal := slices.Index(sc.log, "TERMINAL")
	if sc.cards() != 2 || first < 0 || terminal < 0 || first > terminal {
		t.Fatalf("cards must stream before the run ends: %v", sc.log)
	}
}

func TestManagedScanLiveTextResets(t *testing.T) {
	output := liveCard1 + "\n" + liveCard2
	before := newLiveScenario(t, output,
		livePage(nil, liveEvent(1, "tool.completed", `{}`)),
		livePage(liveText("m2", "m2:1:s", liveCard1[:30], "streaming", true)),
		livePage(liveText("m2", "m2:2:s", output, "answer", true)),
		livePage(nil, liveEvent(4, "run.succeeded", `{}`)),
	)
	if err := runManagedScan(t.Context(), false, before.emit); err != nil || before.cards() != 2 {
		t.Fatalf("reset before a card restarts the parser: %v %v", err, before.log)
	}
	after := newLiveScenario(t, output,
		livePage(nil, liveEvent(1, "tool.completed", `{}`)),
		livePage(liveText("m2", "m2:1:s", liveCard1+"\n", "streaming", true)),
		livePage(liveText("m2", "m2:2:s", output, "streaming", true)),
	)
	if err := runManagedScan(t.Context(), false, after.emit); !errors.Is(err, errScanAnswerChanged) || after.f.cancelled.Load() != 1 {
		t.Fatalf("reset after a card: %v cancelled=%d", err, after.f.cancelled.Load())
	}
}

func TestManagedScanAnswerThatTurnsIntoToolsFails(t *testing.T) {
	output := liveCard1 + "\n" + liveCard2
	sc := newLiveScenario(t, output,
		livePage(nil, liveEvent(1, "tool.completed", `{}`)),
		livePage(liveText("m2", "m2:1:s", liveCard1+"\n", "streaming", true)),
		livePage(liveText("m2", "m2:1:t", "", "tools", false)),
	)
	if err := runManagedScan(t.Context(), false, sc.emit); !errors.Is(err, errScanAnswerChanged) {
		t.Fatalf("tools after a card: %v", err)
	}
	completed := newLiveScenario(t, output,
		livePage(nil, liveEvent(1, "tool.completed", `{}`)),
		livePage(liveText("m2", "m2:1:s", liveCard1+"\n", "streaming", true)),
		livePage(nil, liveEvent(2, "model.completed", `{"stepId":"m2","answer":false,"toolCalls":1}`)),
	)
	if err := runManagedScan(t.Context(), false, completed.emit); !errors.Is(err, errScanAnswerChanged) {
		t.Fatalf("answer:false after a card: %v", err)
	}
	// Before any card, a step that turns to tools is simply not the answer.
	quiet := newLiveScenario(t, output,
		livePage(nil, liveEvent(1, "tool.completed", `{}`)),
		livePage(liveText("m2", "m2:1:s", liveCard1[:30], "streaming", true)),
		livePage(liveText("m2", "m2:1:t", "", "tools", false)),
		livePage(nil, liveEvent(4, "run.succeeded", `{}`)),
	)
	if err := runManagedScan(t.Context(), false, quiet.emit); err != nil || quiet.cards() != 2 {
		t.Fatalf("tools before a card: %v %v", err, quiet.log)
	}
}

func TestManagedScanFallsBackToRunOutput(t *testing.T) {
	output := liveCard1 + "\n" + liveCard2
	for name, pages := range map[string][]chubby.EventsPage{
		"discarded": {
			livePage(nil, liveEvent(1, "tool.completed", `{}`)),
			livePage(liveText("m2", "m2:1:s", liveCard1[:30], "streaming", true)),
			livePage(liveText("m2", "m2:1:d", "", "discarded", false)),
			livePage(nil, liveEvent(4, "run.succeeded", `{}`)),
		},
		"unavailable after a card": {
			livePage(nil, liveEvent(1, "tool.completed", `{}`)),
			livePage(liveText("m2", "m2:1:s", liveCard1+"\n", "streaming", true)),
			livePage(liveText("m2", "m2:1:u", "", "unavailable", false)),
			livePage(nil, liveEvent(4, "run.succeeded", `{}`)),
		},
		"old backend": {{Data: []chubby.ManagedEvent{liveEvent(1, "tool.completed", `{}`)}}},
		"full page without text": {
			{Data: make([]chubby.ManagedEvent, 200), LiveText: true},
			livePage(liveText("m2", "m2:1:s", output, "answer", true), liveEvent(201, "tool.completed", `{}`)),
			livePage(nil, liveEvent(202, "run.succeeded", `{}`)),
		},
	} {
		t.Run(name, func(t *testing.T) {
			sc := newLiveScenario(t, output, pages...)
			if err := runManagedScan(t.Context(), false, sc.emit); err != nil || sc.cards() != 2 {
				t.Fatalf("%v %v", err, sc.log)
			}
		})
	}
}

func TestManagedScanStreamedTextMustBeginTheOutput(t *testing.T) {
	output := liveCard2 + "\n" + liveCard1
	shown := newLiveScenario(t, output,
		livePage(nil, liveEvent(1, "tool.completed", `{}`)),
		livePage(liveText("m2", "m2:1:s", liveCard1+"\n", "streaming", true)),
		livePage(nil, liveEvent(4, "run.succeeded", `{}`)),
	)
	if err := runManagedScan(t.Context(), false, shown.emit); !errors.Is(err, errScanAnswerChanged) {
		t.Fatalf("output mismatch after a card: %v", err)
	}
	unseen := newLiveScenario(t, output,
		livePage(nil, liveEvent(1, "tool.completed", `{}`)),
		livePage(liveText("m2", "m2:1:s", liveCard1[:30], "streaming", true)),
		livePage(nil, liveEvent(4, "run.succeeded", `{}`)),
	)
	if err := runManagedScan(t.Context(), false, unseen.emit); err != nil || unseen.cards() != 2 {
		t.Fatalf("output mismatch before a card: %v %v", err, unseen.log)
	}
}

// pacedRunner answers every events read the same way, counting reads.
type pacedRunner struct {
	*fakeManaged
	reads atomic.Int32
	page  func() (chubby.EventsPage, error)
}

func (p *pacedRunner) EventsPage(context.Context, string, float64, string, int) (chubby.EventsPage, error) {
	p.reads.Add(1)
	return p.page()
}

func runPaced(t *testing.T, page func() (chubby.EventsPage, error)) (*pacedRunner, error) {
	t.Helper()
	running := chubby.ManagedRunDetail{Run: chubby.ManagedRun{ID: "run-1", Status: "running"}}
	p := &pacedRunner{fakeManaged: &fakeManaged{detail: running}, page: page}
	old := newScanManagedRunner
	newScanManagedRunner = func() (scanManagedRunner, error) { return p, nil }
	t.Cleanup(func() { newScanManagedRunner = old })
	t.Setenv(scanAgentInvitedEnv, "agent-invited")
	ctx, cancel := context.WithTimeout(t.Context(), 2500*time.Millisecond)
	defer cancel()
	return p, runManagedScan(ctx, false, (&emitted{}).emit)
}

func TestManagedScanBacksOffACappedLongPoll(t *testing.T) {
	p, err := runPaced(t, func() (chubby.EventsPage, error) {
		return chubby.EventsPage{}, &chubby.APIError{Status: 429, Code: "long_poll_limit", RetryAfter: time.Second}
	})
	// Retry-After 1 s over 2.5 s: about three reads, and the cap is not a failure.
	if reads := p.reads.Load(); reads < 2 || reads > 4 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capped polls: reads=%d err=%v", reads, err)
	}
	if longPollBackoff(&chubby.APIError{Status: 429, Code: "long_poll_limit", RetryAfter: time.Minute}) != 10*time.Second ||
		longPollBackoff(&chubby.APIError{Status: 429, Code: "long_poll_limit"}) != time.Second {
		t.Fatal("Retry-After is not kept within 1-10 s")
	}
	// Any other 429, and a 503 without Retry-After, keep the bounded retries.
	if longPollCapped(&chubby.APIError{Status: 429, Code: "rate_limited"}) || longPollCapped(&chubby.APIError{Status: 503}) {
		t.Fatal("an ordinary 429 or a bare 503 read as a request to come back")
	}
}

func TestManagedScanWaitsOutARelay503WithRetryAfter(t *testing.T) {
	p, err := runPaced(t, func() (chubby.EventsPage, error) {
		return chubby.EventsPage{}, &chubby.APIError{Status: 503, RetryAfter: time.Second}
	})
	// Three failed attempts would have ended the briefing; it waits instead.
	if reads := p.reads.Load(); reads < 2 || reads > 4 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("relay 503: reads=%d err=%v", reads, err)
	}
}

func TestManagedScanPacesReadsWhenWaitIsIgnored(t *testing.T) {
	p, err := runPaced(t, func() (chubby.EventsPage, error) {
		return chubby.EventsPage{LiveText: true}, nil // empty, instantly
	})
	// Paced at scanManagedPoll (700 ms): about four reads in 2.5 s, not thousands.
	if reads := p.reads.Load(); reads < 2 || reads > 5 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("instant empty pages: reads=%d err=%v", reads, err)
	}
	if detail := p.runCalls.Load(); detail > 5 {
		t.Fatalf("run detail read %d times", detail)
	}
}
