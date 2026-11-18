package cmd

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Remote tools for the managed briefing agents. Chubby's worker POSTs JSON
// arguments with a bound secret header; each tool reads one source from a
// fixed plan and answers 200 with a per-source status, so a failed X read is
// data for the model, not a failed step. Outputs go into model context and
// traces: posts only, never account names or error details.

type scanToolKind struct {
	name      string // the registered tool name the agent calls
	argName   string // the single enum argument
	secretEnv string // header secret; the endpoint is absent while unset
	plan      []scanSource
}

var (
	publicScanTool = scanToolKind{
		name: "birdy_explore", argName: "tab", secretEnv: "BIRDY_SCAN_TOOL_SECRET_PUBLIC",
		plan: publicScanSources,
	}
	invitedScanTool = scanToolKind{
		name: "birdy_scan_source", argName: "source", secretEnv: "BIRDY_SCAN_TOOL_SECRET_INVITED",
		plan: scanSources,
	}
)

const (
	scanToolHeader     = "X-Birdy-Tool-Key"
	scanToolMaxOutput  = 60 << 10 // The worker fails a step over 64 KiB.
	scanToolCacheTTL   = 60 * time.Second
	scanToolMinSecret  = 32
	scanManagedModeEnv = "BIRDY_SCAN_MANAGED"
)

// scanManagedMode is "" (off), "trial" (tools live; invited scans opt in with
// a header) or "on" (every scan).
func scanManagedMode() string {
	switch strings.TrimSpace(os.Getenv(scanManagedModeEnv)) {
	case "trial":
		return "trial"
	case "on":
		return "on"
	}
	return ""
}

func (k scanToolKind) keys() []string {
	keys := make([]string, len(k.plan))
	for i, s := range k.plan {
		keys[i] = s.key
	}
	return keys
}

func (k scanToolKind) source(key string) (scanSource, bool) {
	for _, s := range k.plan {
		if s.key == key {
			return s, true
		}
	}
	return scanSource{}, false
}

// toolSecret returns the configured secret, or "" when the tool is disabled.
func (k scanToolKind) toolSecret() string {
	if scanManagedMode() == "" {
		return ""
	}
	secret := strings.TrimSpace(os.Getenv(k.secretEnv))
	if len(secret) < scanToolMinSecret {
		return ""
	}
	return secret
}

// The registered input schema: one enum, no free text.
func (k scanToolKind) parameters() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{k.argName},
		"properties": map[string]any{k.argName: map[string]any{"type": "string", "enum": k.keys()}},
	}
}

type scanToolResult struct {
	Source string     `json:"source"`
	Status string     `json:"status"`
	Posts  []scanPost `json:"posts"`
}

type scanToolCache struct {
	mu       sync.Mutex
	entries  map[string]scanToolCacheEntry
	inflight map[string]chan struct{}
}

type scanToolCacheEntry struct {
	at     time.Time
	result scanToolResult
}

var scanToolResults = scanToolCache{entries: map[string]scanToolCacheEntry{}}

// A failed read is kept briefly too: a model retrying a failing source must
// not pull a fresh account from the pool on every call.
const scanToolErrorTTL = 10 * time.Second

// get reads a source at most once a minute (once per 10 s after a failure):
// a run may call a source twice, and every public visitor reads the same
// Explore tabs. Concurrent calls for one source share a single read.
func (c *scanToolCache) get(ctx context.Context, tool string, s scanSource, read scanReader, now time.Time) scanToolResult {
	key := tool + "\x00" + s.key
	for {
		c.mu.Lock()
		if e, ok := c.entries[key]; ok {
			ttl := scanToolCacheTTL
			if e.result.Status != "ok" {
				ttl = scanToolErrorTTL
			}
			if now.Sub(e.at) < ttl {
				c.mu.Unlock()
				return e.result
			}
		}
		wait, busy := c.inflight[key]
		if !busy {
			if c.inflight == nil {
				c.inflight = map[string]chan struct{}{}
			}
			done := make(chan struct{})
			c.inflight[key] = done
			c.mu.Unlock()
			return c.fill(ctx, key, s, read, now, done)
		}
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return scanToolResult{Source: s.key, Status: "error", Posts: []scanPost{}}
		}
	}
}

func (c *scanToolCache) fill(ctx context.Context, key string, s scanSource, read scanReader, now time.Time, done chan struct{}) scanToolResult {
	posts, err := read(ctx, s)
	result := scanToolResult{Source: s.key, Status: "ok", Posts: posts}
	if err != nil {
		result = scanToolResult{Source: s.key, Status: "error", Posts: []scanPost{}}
	}
	if result.Posts == nil {
		result.Posts = []scanPost{}
	}
	c.mu.Lock()
	c.entries[key] = scanToolCacheEntry{at: now, result: result}
	delete(c.inflight, key)
	close(done)
	c.mu.Unlock()
	return result
}

func secretMatches(got, want string) bool {
	a, b := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

var scanToolLimiter = newRateLimiter(60, time.Minute)

func handleScanTool(kind scanToolKind, read scanReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		secret := kind.toolSecret()
		if secret == "" {
			http.NotFound(w, r)
			return
		}
		if !secretMatches(r.Header.Get(scanToolHeader), secret) {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "unauthorized"})
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !scanToolLimiter.admit(w, r, 1) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
		defer r.Body.Close()
		var args map[string]string
		if err := decodeStrictJSON(r, &args); err != nil || len(args) != 1 {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid arguments"})
			return
		}
		// The schema's enum is the model's guide; this lookup is the rule.
		source, ok := kind.source(args[kind.argName])
		if !ok {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "unsupported source"})
			return
		}
		result := scanToolResults.get(r.Context(), kind.name, source, read, time.Now())
		writeJSON(w, http.StatusOK, boundScanToolResult(result))
	}
}

// boundScanToolResult drops trailing posts until the answer fits the worker's
// output limit.
func boundScanToolResult(result scanToolResult) scanToolResult {
	for {
		data, _ := json.Marshal(result)
		if len(data) <= scanToolMaxOutput || len(result.Posts) == 0 {
			return result
		}
		result.Posts = result.Posts[:len(result.Posts)-1]
	}
}
