package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// X rotates the persisted-query hash for each GraphQL operation periodically and
// serves a 404 for a stale one. The hashes generated from bird's snapshot go
// stale the same way, so this discovers current ones the way the web client
// implicitly publishes them: they are embedded in x.com's JavaScript bundles.
//
// Discovery is only attempted after a request has actually 404'd, and the result
// is cached on disk, so the common path costs nothing.

const (
	queryIDCacheFile = "query-ids-cache.json"
	queryIDCacheTTL  = 24 * time.Hour
	discoveryTimeout = 30 * time.Second
	maxBundleBytes   = 16 << 20
	maxBundles       = 12
)

// discoveryPages are x.com routes whose HTML references the client bundles.
var discoveryPages = []string{
	"https://x.com/?lang=en",
	"https://x.com/explore",
	"https://x.com/notifications",
}

var bundleURLPattern = regexp.MustCompile(`https://abs\.twimg\.com/responsive-web/client-web(?:-legacy)?/[A-Za-z0-9.-]+\.js`)

// operationPatterns match the shapes X's bundlers emit for the
// operationName/queryId pair. Ordered most precise first.
var operationPatterns = []struct {
	re        *regexp.Regexp
	opGroup   int
	hashGroup int
}{
	{regexp.MustCompile(`(?s)e\.exports=\{queryId\s*:\s*["']([^"']+)["']\s*,\s*operationName\s*:\s*["']([^"']+)["']`), 2, 1},
	{regexp.MustCompile(`(?s)e\.exports=\{operationName\s*:\s*["']([^"']+)["']\s*,\s*queryId\s*:\s*["']([^"']+)["']`), 1, 2},
	// Go's RE2 caps a bounded repeat at 1000, so these proximity patterns use a
	// narrower window than bird's 4000. The exact e.exports forms above match
	// the overwhelming majority; these only backstop unusual bundling.
	{regexp.MustCompile(`(?s)operationName\s*[:=]\s*["']([^"']+)["'].{0,1000}?queryId\s*[:=]\s*["']([^"']+)["']`), 1, 2},
	{regexp.MustCompile(`(?s)queryId\s*[:=]\s*["']([^"']+)["'].{0,1000}?operationName\s*[:=]\s*["']([^"']+)["']`), 2, 1},
}

var validHash = regexp.MustCompile(`^[A-Za-z0-9_-]{8,}$`)

// resolver is the process-wide discovered-hash cache.
var resolver = &queryIDResolver{}

type queryIDResolver struct {
	mu        sync.Mutex
	ids       map[string]string
	fetchedAt time.Time
	loaded    bool
	// attempted guards against repeatedly hammering x.com within one process
	// when discovery itself is failing.
	attempted time.Time
}

type queryIDSnapshot struct {
	FetchedAt time.Time         `json:"fetchedAt"`
	IDs       map[string]string `json:"ids"`
}

// lookup returns a discovered hash for an operation, loading the disk cache on
// first use. It never triggers a network fetch.
func (r *queryIDResolver) lookup(operation string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.loaded {
		r.loaded = true
		if snapshot, err := readQueryIDCache(); err == nil && time.Since(snapshot.FetchedAt) < queryIDCacheTTL {
			r.ids, r.fetchedAt = snapshot.IDs, snapshot.FetchedAt
		}
	}

	id, ok := r.ids[operation]
	return id, ok && id != ""
}

// refresh rediscovers hashes from x.com. Concurrent callers share one refresh,
// and a failed attempt backs off so a broken discovery path does not turn every
// request into three page fetches.
func (r *queryIDResolver) refresh(ctx context.Context, client *http.Client) error {
	r.mu.Lock()
	if time.Since(r.attempted) < time.Minute {
		r.mu.Unlock()
		return fmt.Errorf("query id discovery attempted recently; backing off")
	}
	r.attempted = time.Now()
	r.mu.Unlock()

	ids, err := discoverQueryIDs(ctx, client)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("no query ids found in x.com bundles")
	}

	r.mu.Lock()
	r.ids, r.fetchedAt, r.loaded = ids, time.Now(), true
	r.mu.Unlock()

	// Cache failures are not fatal; discovery just repeats next process.
	_ = writeQueryIDCache(queryIDSnapshot{FetchedAt: time.Now(), IDs: ids})
	return nil
}

// discoverQueryIDs fetches x.com's client bundles and extracts every
// operationName -> queryId pair it can find.
func discoverQueryIDs(ctx context.Context, client *http.Client) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	bundles := make([]string, 0, maxBundles)
	seen := make(map[string]bool)
	for _, page := range discoveryPages {
		html, err := fetchDiscovery(ctx, client, page)
		if err != nil {
			continue
		}
		for _, url := range bundleURLPattern.FindAllString(string(html), -1) {
			if seen[url] || len(bundles) >= maxBundles {
				continue
			}
			seen[url] = true
			bundles = append(bundles, url)
		}
		if len(bundles) >= maxBundles {
			break
		}
	}
	if len(bundles) == 0 {
		return nil, fmt.Errorf("no client bundles discovered; x.com layout may have changed")
	}

	ids := make(map[string]string)
	for _, url := range bundles {
		body, err := fetchDiscovery(ctx, client, url)
		if err != nil {
			continue
		}
		extractQueryIDs(string(body), ids)
	}
	return ids, nil
}

// extractQueryIDs merges pairs found in one bundle into ids. The first pattern
// to claim an operation wins, since patterns run most-precise first.
func extractQueryIDs(source string, ids map[string]string) {
	for _, pattern := range operationPatterns {
		for _, m := range pattern.re.FindAllStringSubmatch(source, -1) {
			operation, hash := m[pattern.opGroup], m[pattern.hashGroup]
			if operation == "" || !validHash.MatchString(hash) {
				continue
			}
			if _, exists := ids[operation]; !exists {
				ids[operation] = hash
			}
		}
	}
}

func fetchDiscovery(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBundleBytes))
}

func queryIDCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "birdy", queryIDCacheFile), nil
}

func readQueryIDCache() (queryIDSnapshot, error) {
	var snapshot queryIDSnapshot

	path, err := queryIDCachePath()
	if err != nil {
		return snapshot, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return snapshot, err
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return queryIDSnapshot{}, err
	}
	return snapshot, nil
}

func writeQueryIDCache(snapshot queryIDSnapshot) error {
	path, err := queryIDCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	// Written atomically so a concurrent reader never sees a partial file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// --- Introspection -----------------------------------------------------------

// QueryIDEntry describes the hashes birdy would try for one operation.
type QueryIDEntry struct {
	Operation string   `json:"operation"`
	IDs       []string `json:"ids"`
	// Source names where the first hash came from: the env override, the
	// generated snapshot, or discovery. It is the field that answers "why is
	// birdy using this hash".
	Source string `json:"source,omitempty"`
}

// QueryIDReport is what `birdy query-ids` prints.
type QueryIDReport struct {
	Operations []QueryIDEntry `json:"operations"`
	CachePath  string         `json:"cachePath"`
}

// QueryIDSnapshot reports the hashes birdy would use right now, without
// triggering discovery.
func QueryIDSnapshot() QueryIDReport {
	report := QueryIDReport{}
	if path, err := queryIDCachePath(); err == nil {
		report.CachePath = path
	}

	operations := make([]string, 0, len(queryIDs))
	for operation := range queryIDs {
		operations = append(operations, operation)
	}
	slices.Sort(operations)

	for _, operation := range operations {
		ids := operationQueryIDs(operation)
		entry := QueryIDEntry{Operation: operation, IDs: ids}
		envKey := "BIRDY_" + strings.ToUpper(camelToSnake(operation)) + "_QUERY_ID"
		switch {
		case strings.TrimSpace(os.Getenv(envKey)) != "":
			entry.Source = envKey
		case len(queryIDs[operation]) > 0:
			entry.Source = "generated"
		default:
			entry.Source = "discovered"
		}
		report.Operations = append(report.Operations, entry)
	}
	return report
}
