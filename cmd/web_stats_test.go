package cmd

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guzus/birdy/internal/chatmodel"
	"github.com/guzus/birdy/internal/claude"
	"github.com/guzus/birdy/internal/store"
	"github.com/jackc/pgx/v5"
)

type memWebSink struct {
	mu      sync.Mutex
	fail    bool
	batches []webStatsBatch
}

func (m *memWebSink) write(_ context.Context, b webStatsBatch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("database down")
	}
	m.batches = append(m.batches, b)
	return nil
}

func (m *memWebSink) uses() map[webUseKey]int64 {
	out := map[webUseKey]int64{}
	for _, b := range m.batches {
		for k, n := range b.uses {
			out[k] += n
		}
	}
	return out
}

// withWebStats installs a recorder for one test.
func withWebStats(t *testing.T, sink webStatsSink, salt string) *webStats {
	t.Helper()
	s := newWebStats(sink, salt)
	s.accounts = func() (int, error) { return 3, nil }
	webStatsRecorder = s
	store.SetRateLimitObserver(s.rateLimited)
	t.Cleanup(func() {
		webStatsRecorder = nil
		store.SetRateLimitObserver(nil)
	})
	return s
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	var mu sync.Mutex
	log.SetOutput(writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return buf.Write(p) }))
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestWebStatsOffWhenUnset(t *testing.T) {
	t.Setenv("BIRDY_WEB_STATS_DATABASE_URL", "")
	t.Setenv("BIRDY_WEB_STATS_SALT", "a-salt")
	logs := captureLog(t)
	if s := startWebStatsFromEnv(t.Context()); s != nil || webStatsRecorder != nil {
		t.Fatal("stats started without a database URL")
	}
	// Recording with stats off is a no-op, not a panic.
	recordWebUse(httptest.NewRequest(http.MethodPost, "/api/chat", nil), webSurfaceChat, webEngineDirect, true)
	if logs.Len() != 0 {
		t.Errorf("stats off should be silent, logged %q", logs.String())
	}
}

func TestWebStatsEnvIsRemovedAndDSNNeverLogged(t *testing.T) {
	logs := captureLog(t)
	// Not a URL pgx can parse: the stats stay off and the value is not logged.
	t.Setenv("BIRDY_WEB_STATS_DATABASE_URL", "postgres://statsuser:sekrit-pw@[bad-host/statsdb")
	t.Setenv("BIRDY_WEB_STATS_SALT", "salt-value-xyz")
	if s := startWebStatsFromEnv(t.Context()); s != nil {
		t.Fatal("an invalid URL turned stats on")
	}
	// A valid URL to a closed port: on, never connected at start.
	port := closedPort(t)
	dsn := "postgres://statsuser:sekrit-pw@127.0.0.1:" + port + "/statsdb?connect_timeout=2&sslmode=disable"
	t.Setenv("BIRDY_WEB_STATS_DATABASE_URL", dsn)
	t.Setenv("BIRDY_WEB_STATS_SALT", "salt-value-xyz")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	start := time.Now()
	s := startWebStatsFromEnv(ctx)
	t.Cleanup(func() { webStatsRecorder = nil; store.SetRateLimitObserver(nil) })
	if s == nil {
		t.Fatal("stats did not start")
	}
	if time.Since(start) > time.Second {
		t.Error("start waited on the database")
	}
	for _, k := range []string{"BIRDY_WEB_STATS_DATABASE_URL", "BIRDY_WEB_STATS_SALT"} {
		if _, ok := os.LookupEnv(k); ok {
			t.Errorf("%s is still in the environment children inherit", k)
		}
	}
	s.accounts = func() (int, error) { return 1, nil }
	s.record("192.0.2.1", webSurfaceChat, webEngineDirect, true)
	err := s.flush(t.Context())
	if err == nil {
		t.Fatal("a write to a closed port succeeded")
	}
	log.Printf("[web-stats] write failed, this minute's counts are dropped: %s", webStatsErrorText(err))
	for _, leak := range []string{"sekrit", "statsuser", "statsdb", "127.0.0.1", port, "bad-host", "salt-value"} {
		if strings.Contains(logs.String(), leak) {
			t.Errorf("logs hold %q: %s", leak, logs.String())
		}
	}
	if !strings.Contains(logs.String(), "could not reach the database") {
		t.Errorf("logs = %q", logs.String())
	}
}

func closedPort(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	return port
}

func TestWebClientHash(t *testing.T) {
	salt := []byte("s")
	for _, ip := range []string{"192.0.2.44", "2001:db8::1"} {
		h, ok := webClientHash(salt, ip, "2026-11-19")
		if !ok {
			t.Fatalf("%s not hashed", ip)
		}
		if bytes.Contains(h[:], []byte(ip)) || bytes.Contains(h[:], net.ParseIP(ip).To4()) && net.ParseIP(ip).To4() != nil {
			t.Errorf("hash of %s holds the address", ip)
		}
		mac := hmac.New(sha256.New, webMonthKey(salt, "2026-11-19"))
		mac.Write([]byte(ip))
		if !bytes.Equal(h[:], mac.Sum(nil)[:16]) {
			t.Errorf("%s: not HMAC-SHA256(month key, address)[:16]", ip)
		}
	}
	a, _ := webClientHash(salt, "192.0.2.44", "2026-11-19")
	b, _ := webClientHash(salt, "::ffff:192.0.2.44", "2026-11-19")
	if a != b {
		t.Error("a v4-mapped address hashes differently")
	}
	// The key rotates monthly: same month, same hash; next month, another.
	d1, _ := webClientHash(salt, "192.0.2.44", "2026-11-01")
	m2, _ := webClientHash(salt, "192.0.2.44", "2026-12-01")
	if d1 != a || m2 == a {
		t.Error("the hash must hold within a UTC month and change with the month")
	}
	// The same test vector as the server that shares these tables:
	// HMAC-SHA256(HMAC-SHA256("vector-salt", "birdy-client-hash:2026-11"), "192.0.2.1")[:16].
	v, _ := webClientHash([]byte("vector-salt"), "192.0.2.1", "2026-11-19")
	if got := hex.EncodeToString(v[:]); got != "8838202d167c1d83a6b0bde7bd6472f3" {
		t.Errorf("test vector = %s", got)
	}
	if _, ok := webClientHash(nil, "192.0.2.44", "2026-11-19"); ok {
		t.Error("hashed without a salt")
	}
	if _, ok := webClientHash(salt, "unknown", "2026-11-19"); ok {
		t.Error("hashed a missing address")
	}
}

func TestWebStatsCountsAndInvitedSplit(t *testing.T) {
	sink := &memWebSink{}
	s := withWebStats(t, sink, "salt")
	s.now = func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) }
	s.record("192.0.2.1", webSurfaceVisitorScan, webEngineDirect, false)
	s.record("192.0.2.1", webSurfaceChat, webEngineManaged, true) // the visitor then signs in
	s.record("192.0.2.1", webSurfaceVisitorChat, webEngineDirect, false)
	s.record("192.0.2.2", webSurfaceVisitorChat, webEngineDirect, false)
	s.record("192.0.2.3", webSurfaceCommand, webEngineNone, true)
	s.record("unknown", webSurfaceChat, webEngineDirect, true) // counted, not hashed
	if err := s.flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	b := sink.batches[0]
	invited := map[bool]int{}
	for _, inv := range b.clients {
		invited[inv]++
	}
	if invited[true] != 2 || invited[false] != 1 {
		t.Errorf("invited/visitor clients = %v, want 2 invited and 1 visitor", invited)
	}
	want := map[webUseKey]int64{
		{"2026-11-19", "visitor-scan", "direct"}: 1, {"2026-11-19", "chat", "managed"}: 1,
		{"2026-11-19", "visitor-chat", "direct"}: 2, {"2026-11-19", "command", "none"}: 1, {"2026-11-19", "chat", "direct"}: 1,
	}
	for k, n := range want {
		if b.uses[k] != n {
			t.Errorf("%v = %d, want %d", k, b.uses[k], n)
		}
	}
	if !b.poolOK || b.accounts != 3 || b.poolDay != "2026-11-19" {
		t.Errorf("pool = %+v", b)
	}
}

func TestWebStatsNoSaltKeepsCountsOnly(t *testing.T) {
	sink := &memWebSink{}
	s := withWebStats(t, sink, "")
	s.record("192.0.2.1", webSurfaceChat, webEngineDirect, true)
	if err := s.flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(sink.batches[0].clients) != 0 || len(sink.batches[0].uses) != 1 {
		t.Errorf("batch = %+v", sink.batches[0])
	}
}

func TestWebStatsRateLimitEventsFromTheStore(t *testing.T) {
	sink := &memWebSink{}
	s := withWebStats(t, sink, "salt")
	st, err := store.NewEphemeral([]store.Account{{Name: "a", AuthToken: "t", CT0: "c"}})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := st.RecordRateLimit("a"); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.RecordRateLimit("missing") // no such account: not an event
	if err := s.flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := sink.batches[0].limited[s.day()]; got != 3 {
		t.Errorf("rate-limit events = %d, want 3", got)
	}
}

// A failing database never reaches the request: the chat answers as usual
// and is counted; the failed flush is dropped.
func TestWebStatsFlushFailureIsIsolated(t *testing.T) {
	sink := &memWebSink{fail: true}
	s := withWebStats(t, sink, "salt")
	binDir := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\ndata: {\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"from claude\"}]}}\ndata: {\"type\":\"result\",\"result\":\"ok\"}\nEOF\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BIRDY_E2B_TEMPLATE", "")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "http://example.com/api/chat", bytes.NewBufferString(`{"prompt":"hello"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Invite-Code", "birdy")
		rr := httptest.NewRecorder()
		handleAPIChat("birdy").ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "from claude") {
			t.Fatalf("chat with a failing stats database = %d %q", rr.Code, rr.Body.String())
		}
		if err := s.flush(t.Context()); err == nil {
			t.Fatal("the failing sink reported success")
		}
	}
	// Unauthorized requests are neither counted nor hashed.
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/chat", bytes.NewBufferString(`{"prompt":"hello"}`))
	rr := httptest.NewRecorder()
	handleAPIChat("birdy").ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized chat = %d", rr.Code)
	}
	if len(s.uses) != 0 || len(s.clients) != 0 {
		t.Errorf("after failed flushes and an unauthorized request the buffer holds %v %v", s.uses, s.clients)
	}
	// Each answered chat was counted before its flush failed and was dropped.
	sink.fail = false
	req = httptest.NewRequest(http.MethodPost, "http://example.com/api/chat", bytes.NewBufferString(`{"prompt":"hello"}`))
	req.Header.Set("X-Invite-Code", "birdy")
	handleAPIChat("birdy").ServeHTTP(httptest.NewRecorder(), req)
	if err := s.flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := sink.uses()[webUseKey{s.day(), webSurfaceChat, webEngineDirect}]; got != 1 {
		t.Errorf("chat/direct = %d, want 1 (only the request after the outage)", got)
	}
	if len(sink.batches[0].clients) != 1 {
		t.Errorf("clients = %v", sink.batches[0].clients)
	}
	for _, inv := range sink.batches[0].clients {
		if !inv {
			t.Error("an invited chat was recorded as a visitor")
		}
	}
}

// With BIRDY_WEB_STATS_TEST_DATABASE_URL (a disposable database), the
// upserts run against Postgres in a throwaway schema.
func TestWebStatsPostgres(t *testing.T) {
	dsn := os.Getenv("BIRDY_WEB_STATS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BIRDY_WEB_STATS_TEST_DATABASE_URL not set")
	}
	ctx := t.Context()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	schema := "web_stats_test_" + strings.ToLower(rand.Text())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema+`;
CREATE TABLE `+schema+`.web_daily (day date, surface text, engine text, n bigint NOT NULL DEFAULT 0, PRIMARY KEY (day, surface, engine));
CREATE TABLE `+schema+`.web_clients (day date, client_hash bytea, invited boolean NOT NULL DEFAULT false, PRIMARY KEY (day, client_hash));
CREATE TABLE `+schema+`.web_pool (day date PRIMARY KEY, accounts int NOT NULL DEFAULT 0, rate_limited_events bigint NOT NULL DEFAULT 0, updated_at timestamptz NOT NULL DEFAULT now());`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	cfg.RuntimeParams["search_path"] = schema
	s := withWebStats(t, &pgWebStats{cfg: cfg}, "salt")
	s.now = func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) }
	s.record("192.0.2.1", webSurfaceVisitorChat, webEngineDirect, false)
	s.record("192.0.2.2", webSurfaceChat, webEngineManaged, true)
	s.rateLimited()
	if err := s.flush(ctx); err != nil {
		t.Fatal(err)
	}
	s.record("192.0.2.1", webSurfaceChat, webEngineManaged, true) // the visitor signs in
	s.record("192.0.2.3", webSurfaceVisitorChat, webEngineDirect, false)
	s.rateLimited()
	s.rateLimited()
	s.accounts = func() (int, error) { return 0, errors.New("store unreadable") } // keeps the last count
	if err := s.flush(ctx); err != nil {
		t.Fatal(err)
	}
	var managed, invited, visitors, accounts int
	var events int64
	q := func(sql string, dst ...any) {
		if err := admin.QueryRow(ctx, sql).Scan(dst...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	q("SELECT n FROM "+schema+".web_daily WHERE surface = 'chat' AND engine = 'managed'", &managed)
	q("SELECT count(*) FILTER (WHERE invited), count(*) FILTER (WHERE NOT invited) FROM "+schema+".web_clients", &invited, &visitors)
	q("SELECT accounts, rate_limited_events FROM "+schema+".web_pool WHERE day = '2026-11-19'", &accounts, &events)
	if managed != 2 || invited != 2 || visitors != 1 || accounts != 3 || events != 3 {
		t.Errorf("managed %d, invited %d, visitors %d, accounts %d, events %d; want 2, 2, 1, 3, 3", managed, invited, visitors, accounts, events)
	}
}

// With BIRDY_WEB_STATS_TEST_WRITER_URL (a disposable database whose web_*
// tables already exist, reached as the stats writer role that may only
// SELECT, INSERT and UPDATE them), a flush works with exactly those rights.
func TestWebStatsWriterRole(t *testing.T) {
	dsn := os.Getenv("BIRDY_WEB_STATS_TEST_WRITER_URL")
	if dsn == "" {
		t.Skip("BIRDY_WEB_STATS_TEST_WRITER_URL not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	s := withWebStats(t, &pgWebStats{cfg: cfg}, "salt")
	day := time.Date(2001, 2, 3, 12, 0, 0, 0, time.UTC) // a day no other data uses
	s.now = func() time.Time { return day }
	for range 2 {
		s.record("192.0.2.9", webSurfaceScan, webEngineDirect, true)
		s.rateLimited()
		if err := s.flush(t.Context()); err != nil {
			t.Fatalf("flush as the writer role: %s (%v)", webStatsErrorText(err), err)
		}
	}
	conn, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var n, events int64
	if err := conn.QueryRow(t.Context(), `SELECT (SELECT n FROM web_daily WHERE day = '2001-02-03' AND surface = 'scan' AND engine = 'direct'),
		(SELECT rate_limited_events FROM web_pool WHERE day = '2001-02-03')`).Scan(&n, &events); err != nil {
		t.Fatal(err)
	}
	if n != 2 || events != 2 {
		t.Errorf("n = %d, events = %d; want 2 and 2", n, events)
	}
}

// The scan handler labels a public visitor's scan visitor-scan, not
// invited, and an invited one scan.
func TestWebStatsScanSurfaces(t *testing.T) {
	t.Setenv("BIRDY_WEB_CHAT_BACKEND", "chubby")
	t.Setenv("CHUBBY_API_KEY", "chb_test")
	t.Setenv("CHUBBY_MODEL", "auto")
	t.Setenv("BIRDY_WEB_PUBLIC", "1")
	sink := &memWebSink{}
	s := withWebStats(t, sink, "salt")
	h := apiScanHandler("invite", func(context.Context, scanSource) ([]scanPost, error) {
		return []scanPost{{ID: "22", URL: "https://x.com/i/web/status/22", Text: "sourced"}}, nil
	}, func(_ context.Context, _ chatmodel.Selection, _ chatmodel.Request, emit func(claude.Event)) {
		emit(claude.Event{Type: claude.EventToken, Text: `{"category":"SIGNAL","title":"T","summary":"S","bullets":["more"],"ids":["22"]}`})
		emit(claude.Event{Type: claude.EventDone})
	})
	for i, token := range []string{"", "invite", "wrong"} {
		r := httptest.NewRequest(http.MethodPost, "/api/scan", strings.NewReader("{}"))
		r.RemoteAddr = "192.0.2." + strconv.Itoa(10+i) + ":123"
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := map[string]int{"": 200, "invite": 200, "wrong": 401}[token]
		if w.Code != want {
			t.Fatalf("token %q: status %d, want %d", token, w.Code, want)
		}
	}
	if err := s.flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	b := sink.batches[0]
	day := s.day()
	if b.uses[webUseKey{day, webSurfaceVisitorScan, webEngineDirect}] != 1 || b.uses[webUseKey{day, webSurfaceScan, webEngineDirect}] != 1 || len(b.uses) != 2 {
		t.Errorf("uses = %v", b.uses)
	}
	invited := map[bool]int{}
	for _, inv := range b.clients {
		invited[inv]++
	}
	if invited[true] != 1 || invited[false] != 1 {
		t.Errorf("clients invited/visitor = %v (the wrong token must not be counted)", invited)
	}
}
