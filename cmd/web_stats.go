package cmd

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"log"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/guzus/birdy/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Optional daily usage counts for the web host, written to Postgres only
// when BIRDY_WEB_STATS_DATABASE_URL is set; otherwise nothing here runs.
//
// Per UTC day it counts answered requests by surface and engine, the
// distinct clients (an HMAC of the client address under
// BIRDY_WEB_STATS_SALT, cut to 16 bytes, marked invited or not), and the
// account pool's size and rate-limit events. No address, prompt, answer,
// account name or credential is stored. Counts are buffered in memory and
// written once a minute; a failed write is logged and dropped, and never
// touches request handling. The tables must already exist: the writer
// creates nothing.

const (
	webSurfaceChat        = "chat"
	webSurfaceScan        = "scan"
	webSurfaceVisitorChat = "visitor-chat"
	webSurfaceVisitorScan = "visitor-scan"
	webSurfaceCommand     = "command"

	webEngineManaged = "managed"
	webEngineDirect  = "direct"
	webEngineNone    = "none"

	webStatsFlushEvery = time.Minute
	webStatsHashLen    = 16
	// webStatsClientCap bounds the distinct clients held between flushes.
	webStatsClientCap = 50_000
)

type webUseKey struct{ day, surface, engine string }

type webClientKey struct {
	day  string
	hash [webStatsHashLen]byte
}

// webStatsBatch is one flush.
type webStatsBatch struct {
	uses     map[webUseKey]int64
	clients  map[webClientKey]bool // invited
	limited  map[string]int64      // rate-limit events per day
	poolDay  string
	accounts int
	poolOK   bool // accounts was read
}

type webStatsSink interface {
	write(ctx context.Context, b webStatsBatch) error
}

type webStats struct {
	sink     webStatsSink
	salt     []byte
	now      func() time.Time
	accounts func() (int, error)

	mu      sync.Mutex
	uses    map[webUseKey]int64
	clients map[webClientKey]bool
	limited map[string]int64
	dropped int64
}

// webStatsRecorder is the process's recorder, nil when stats are off.
var webStatsRecorder *webStats

func newWebStats(sink webStatsSink, salt string) *webStats {
	return &webStats{sink: sink, salt: []byte(salt), now: time.Now, accounts: poolAccounts,
		uses: map[webUseKey]int64{}, clients: map[webClientKey]bool{}, limited: map[string]int64{}}
}

// startWebStatsFromEnv turns the stats on when BIRDY_WEB_STATS_DATABASE_URL
// is set. It reads both variables and removes them from the environment,
// so no child process (the TTY, commands, model runs) inherits them. It
// never connects here: an unreachable database cannot delay or stop the host.
func startWebStatsFromEnv(ctx context.Context) *webStats {
	dsn, salt := os.Getenv("BIRDY_WEB_STATS_DATABASE_URL"), os.Getenv("BIRDY_WEB_STATS_SALT")
	_ = os.Unsetenv("BIRDY_WEB_STATS_DATABASE_URL")
	_ = os.Unsetenv("BIRDY_WEB_STATS_SALT")
	if dsn == "" {
		return nil
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		// The parse error can quote the URL, so it is not logged.
		log.Print("[web-stats] off: BIRDY_WEB_STATS_DATABASE_URL is not a valid database URL")
		return nil
	}
	s := newWebStats(&pgWebStats{cfg: cfg}, salt)
	if salt == "" {
		log.Print("[web-stats] BIRDY_WEB_STATS_SALT is not set: request counts are kept, distinct clients are not")
	}
	store.SetRateLimitObserver(s.rateLimited)
	webStatsRecorder = s
	go s.run(ctx)
	log.Print("[web-stats] on: daily counts are written every minute")
	return s
}

// webClientHash is HMAC-SHA256(salt, the address in canonical text form),
// cut to 16 bytes; false when there is no salt or no valid address.
func webClientHash(salt []byte, ip string) ([webStatsHashLen]byte, bool) {
	var out [webStatsHashLen]byte
	addr, err := netip.ParseAddr(ip)
	if len(salt) == 0 || err != nil {
		return out, false
	}
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte(addr.WithZone("").Unmap().String()))
	copy(out[:], mac.Sum(nil))
	return out, true
}

// recordWebUse counts one answered request. Call it after authentication,
// where the engine that answers is known. A nil recorder does nothing.
func recordWebUse(r *http.Request, surface, engine string, invited bool) {
	if s := webStatsRecorder; s != nil {
		s.record(clientIP(r), surface, engine, invited)
	}
}

func (s *webStats) day() string { return s.now().UTC().Format(time.DateOnly) }

func (s *webStats) record(ip, surface, engine string, invited bool) {
	day := s.day()
	hash, hashed := webClientHash(s.salt, ip)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uses[webUseKey{day, surface, engine}]++
	if !hashed {
		return
	}
	k := webClientKey{day, hash}
	if was, ok := s.clients[k]; ok {
		s.clients[k] = was || invited
		return
	}
	if len(s.clients) >= webStatsClientCap {
		s.dropped++
		return
	}
	s.clients[k] = invited
}

func (s *webStats) rateLimited() {
	day := s.day()
	s.mu.Lock()
	s.limited[day]++
	s.mu.Unlock()
}

// poolAccounts is the number of accounts in rotation (disabled ones are
// out of it). Only the count leaves this function.
func poolAccounts() (int, error) {
	st, err := store.Open()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range st.List() {
		if !a.Disabled {
			n++
		}
	}
	return n, nil
}

// flush writes and clears the buffer. A failure drops it: these are
// counts for a dashboard, and a retry queue is not worth the memory.
func (s *webStats) flush(ctx context.Context) error {
	s.mu.Lock()
	b := webStatsBatch{uses: s.uses, clients: s.clients, limited: s.limited, poolDay: s.day()}
	dropped := s.dropped
	s.uses, s.clients, s.limited, s.dropped = map[webUseKey]int64{}, map[webClientKey]bool{}, map[string]int64{}, 0
	s.mu.Unlock()
	if dropped > 0 {
		log.Printf("[web-stats] %d distinct clients over the %d cap were not counted", dropped, webStatsClientCap)
	}
	if n, err := s.accounts(); err == nil {
		b.accounts, b.poolOK = n, true
	}
	return s.sink.write(ctx, b)
}

func (s *webStats) run(ctx context.Context) {
	tick := time.NewTicker(webStatsFlushEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		if err := s.flush(fctx); err != nil {
			log.Printf("[web-stats] write failed, this minute's counts are dropped: %s", webStatsErrorText(err))
		}
		cancel()
	}
}

// webStatsErrorText describes a write failure without anything that could
// come from the database URL (host, user, database name): a server error's
// SQLSTATE, or the kind of failure.
func webStatsErrorText(err error) string {
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr):
		return "server error SQLSTATE " + pgErr.Code
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case pgconn.Timeout(err):
		return "timed out"
	}
	return "could not reach the database"
}

// pgWebStats writes a batch in one short-lived connection and transaction.
type pgWebStats struct{ cfg *pgx.ConnConfig }

func (p *pgWebStats) write(ctx context.Context, b webStatsBatch) error {
	conn, err := pgx.ConnectConfig(ctx, p.cfg.Copy())
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if len(b.uses) > 0 {
			var days, surfaces, engines []string
			var ns []int64
			for k, n := range b.uses {
				days, surfaces, engines, ns = append(days, k.day), append(surfaces, k.surface), append(engines, k.engine), append(ns, n)
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO web_daily AS d (day, surface, engine, n)
SELECT day::date, surface, engine, n
  FROM unnest($1::text[], $2::text[], $3::text[], $4::bigint[]) AS u(day, surface, engine, n)
ON CONFLICT (day, surface, engine) DO UPDATE SET n = d.n + excluded.n`, days, surfaces, engines, ns); err != nil {
				return err
			}
		}
		if len(b.clients) > 0 {
			var days []string
			var hashes [][]byte
			var invited []bool
			for k, inv := range b.clients {
				days, hashes, invited = append(days, k.day), append(hashes, append([]byte(nil), k.hash[:]...)), append(invited, inv)
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO web_clients AS c (day, client_hash, invited)
SELECT day::date, h, inv FROM unnest($1::text[], $2::bytea[], $3::boolean[]) AS u(day, h, inv)
ON CONFLICT (day, client_hash) DO UPDATE SET invited = c.invited OR excluded.invited`, days, hashes, invited); err != nil {
				return err
			}
		}
		// Rate-limit events add up per day; the pool size is today's latest.
		days := map[string]bool{}
		for d := range b.limited {
			days[d] = true
		}
		if b.poolOK {
			days[b.poolDay] = true
		}
		for d := range days {
			var accounts *int
			if b.poolOK && d == b.poolDay {
				accounts = &b.accounts
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO web_pool AS p (day, accounts, rate_limited_events, updated_at)
VALUES ($1::date, coalesce($2::int, 0), $3, now())
ON CONFLICT (day) DO UPDATE SET
	accounts = coalesce($2::int, p.accounts),
	rate_limited_events = p.rate_limited_events + excluded.rate_limited_events,
	updated_at = now()`, d, accounts, b.limited[d]); err != nil {
				return err
			}
		}
		return nil
	})
}
