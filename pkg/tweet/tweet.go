// Package tweet is birdy's public Go API for reading tweets.
//
// It exposes the account rotation that birdy's CLI performs — pick an account,
// call X with its credentials, record usage and rate limits — as an embeddable
// library, so a Go service can read X without shelling out to the birdy binary
// or standing up a hosted birdy instance.
//
// Reading is implemented natively in Go against X's GraphQL API, so this
// package needs no Node.js runtime and no bird CLI.
//
// A Client is safe for concurrent use and keeps rotation state in memory, so it
// works on a read-only filesystem (Cloud Run, scratch containers). Accounts are
// normally supplied through the BIRDY_ACCOUNTS environment variable:
//
//	BIRDY_ACCOUNTS=[{"name":"main","auth_token":"...","ct0":"..."}]
//
// # Stability
//
// The identifiers and struct fields in this package are covered by birdy's
// semver promise; see COMPATIBILITY.md in the repository root. That promise is
// about this package's shape, not about X: X's GraphQL API is unversioned and
// undocumented, so a stable signature guarantees your code keeps compiling, not
// that a call keeps succeeding. Handle errors from every call.
package tweet

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/store"
	"github.com/guzus/birdy/internal/xapi"
)

// Tweet, Media, and Author are declared in types.go.

// Options configures a Client.
type Options struct {
	// Strategy names the rotation strategy: round-robin, least-recently-used,
	// least-used, random, or quota-aware. Defaults to quota-aware, which skips
	// accounts that recently saw a 429 — the best default for a server.
	Strategy string

	// Account pins every call to a single named account, disabling rotation.
	Account string

	// AccountsJSON supplies accounts directly, in the same shape as the
	// BIRDY_ACCOUNTS environment variable. When empty, the Client falls back to
	// birdy's usual sources: ~/.config/birdy/accounts.json plus BIRDY_ACCOUNTS.
	AccountsJSON string
}

// MonitoringOptions configures a Client used for timeline and graph polling.
// It is separate from Options so adding the read-pool capability does not break
// callers that use an unkeyed Options literal.
type MonitoringOptions struct {
	Strategy     string
	Account      string
	AccountsJSON string
	AccountPool  []string
}

// Client reads tweets through a rotating pool of X accounts.
type Client struct {
	store    *store.Store
	strategy rotation.Strategy
	pinned   string
	allowed  map[string]struct{}

	mu           sync.Mutex
	lastUsedName string
	reserved     map[string]int64
	// apiClients is one X client per account, built lazily and reused so each
	// keeps a stable client identity across requests.
	apiClients map[string]*xapi.Client
}

// NewClient builds a Client from the given options.
//
// It fails when no accounts are configured, since every call needs credentials.
func NewClient(opts Options) (*Client, error) {
	return newClient(opts, nil)
}

// NewMonitoringClient builds a Client whose automatic selection is restricted
// to AccountPool. Unknown, empty, and duplicate names fail construction.
func NewMonitoringClient(opts MonitoringOptions) (*Client, error) {
	return newClient(Options{
		Strategy: opts.Strategy, Account: opts.Account, AccountsJSON: opts.AccountsJSON,
	}, opts.AccountPool)
}

func newClient(opts Options, accountPool []string) (*Client, error) {
	strategyName := strings.TrimSpace(opts.Strategy)
	if strategyName == "" {
		strategyName = string(rotation.QuotaAware)
	}
	strategy, err := rotation.ParseStrategy(strategyName)
	if err != nil {
		return nil, err
	}

	st, err := openStore(opts.AccountsJSON)
	if err != nil {
		return nil, err
	}
	if st.Len() == 0 {
		return nil, fmt.Errorf("no accounts configured: set BIRDY_ACCOUNTS or run `birdy account add`")
	}

	pinned := strings.TrimSpace(opts.Account)
	if pinned != "" {
		if _, err := st.Get(pinned); err != nil {
			return nil, err
		}
	}
	allowed, err := validateAccountPool(st, accountPool)
	if err != nil {
		return nil, err
	}
	if pinned != "" && allowed != nil {
		if _, ok := allowed[pinned]; !ok {
			return nil, fmt.Errorf("account %q is not in AccountPool", pinned)
		}
	}

	return &Client{
		store:      st,
		strategy:   strategy,
		pinned:     pinned,
		allowed:    allowed,
		apiClients: make(map[string]*xapi.Client),
		reserved:   make(map[string]int64),
	}, nil
}

func validateAccountPool(st *store.Store, names []string) (map[string]struct{}, error) {
	if len(names) == 0 {
		return nil, nil
	}
	allowed := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, fmt.Errorf("AccountPool contains an empty account name")
		}
		if _, exists := allowed[name]; exists {
			return nil, fmt.Errorf("AccountPool contains duplicate account %q", name)
		}
		if _, err := st.Get(name); err != nil {
			return nil, fmt.Errorf("AccountPool: %w", err)
		}
		allowed[name] = struct{}{}
	}
	return allowed, nil
}

// openStore loads accounts, preferring an explicit JSON blob when supplied.
func openStore(accountsJSON string) (*store.Store, error) {
	accountsJSON = strings.TrimSpace(accountsJSON)
	if accountsJSON == "" {
		st, err := store.Open()
		if err != nil {
			return nil, fmt.Errorf("opening account store: %w", err)
		}
		return st, nil
	}

	var accounts []store.Account
	if err := json.Unmarshal([]byte(accountsJSON), &accounts); err != nil {
		return nil, fmt.Errorf("parsing accounts json: %w", err)
	}

	// Build an in-memory store; Save is a no-op on it, so nothing touches disk.
	st, err := store.NewEphemeral(accounts)
	if err != nil {
		return nil, fmt.Errorf("parsing accounts json: %w", err)
	}
	return st, nil
}

// Accounts returns the configured account names, for diagnostics.
func (c *Client) Accounts() []string {
	list := c.store.List()
	names := make([]string, 0, len(list))
	for _, a := range list {
		if c.allowed != nil {
			if _, ok := c.allowed[a.Name]; !ok {
				continue
			}
		}
		names = append(names, a.Name)
	}
	return names
}

// Read fetches a single tweet. ref may be a status URL or a bare tweet ID.
func (c *Client) Read(ctx context.Context, ref string) (*Tweet, error) {
	tweetID, err := ExtractTweetID(ref)
	if err != nil {
		return nil, err
	}

	var result *Tweet
	err = c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		t, err := api.Tweet(ctx, tweetID)
		if err != nil {
			return err
		}
		converted := convertTweet(*t)
		result = &converted
		return nil
	})
	return result, err
}

// Thread fetches the conversation around a tweet. ref may be a status URL or a
// bare tweet ID.
//
// X returns the conversation flat: the focal tweet's ancestors, the focal tweet
// itself, and its replies. Ancestry is expressed through each tweet's
// InReplyToStatusID — use AncestorChain to walk it.
func (c *Client) Thread(ctx context.Context, ref string) ([]Tweet, error) {
	tweetID, err := ExtractTweetID(ref)
	if err != nil {
		return nil, err
	}

	var result []Tweet
	err = c.withAccount(ctx, func(ctx context.Context, api *xapi.Client) error {
		tweets, err := api.Conversation(ctx, tweetID)
		if err != nil {
			return err
		}
		result = convertTweets(tweets)
		return nil
	})
	return result, err
}

// IsRateLimited reports whether err represents an X rate limit, including a
// GraphQL error delivered inside an HTTP-200 response. Wrapped errors work.
func IsRateLimited(err error) bool {
	return xapi.IsRateLimited(err)
}

// withAccount picks an account, runs fn against it, and records the outcome.
func (c *Client) withAccount(ctx context.Context, fn func(context.Context, *xapi.Client) error) error {
	account, err := c.reserve()
	if err != nil {
		return err
	}
	defer c.release(account.Name)

	api, err := c.apiClientFor(account)
	if err != nil {
		return err
	}

	runErr := fn(ctx, api)

	// Record the outcome even on failure: a 429 is exactly the signal the
	// quota-aware strategy needs to route around this account next time.
	if runErr != nil && xapi.IsRateLimited(runErr) {
		_ = c.store.RecordRateLimit(account.Name)
	}
	_ = c.store.RecordUsage(account.Name)
	_ = c.store.Save() // no-op for env-backed stores

	if runErr != nil {
		return fmt.Errorf("account %q: %w", account.Name, runErr)
	}
	return nil
}

// reserve selects and accounts for an in-flight call atomically. Without the
// reservation, synchronized callers all observe the same stale usage counters
// and overload one session even when a pool is configured.
func (c *Client) reserve() (*store.Account, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pinned != "" {
		account, err := c.store.Get(c.pinned)
		if err != nil {
			return nil, err
		}
		c.reserved[account.Name]++
		c.lastUsedName = account.Name
		return account, nil
	}

	accounts := c.eligibleAccounts()
	// Model reservations as temporary uses. This preserves each strategy's
	// meaning while letting quota-aware/LRU/least-used see concurrent work.
	now := time.Now()
	for i := range accounts {
		if n := c.reserved[accounts[i].Name]; n > 0 {
			accounts[i].UseCount += n
			accounts[i].LastUsed = now
		}
	}
	account, err := rotation.Pick(accounts, c.strategy, c.lastUsedName)
	if err != nil {
		return nil, fmt.Errorf("selecting account: %w", err)
	}
	c.reserved[account.Name]++
	c.lastUsedName = account.Name
	return account, nil
}

func (c *Client) release(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reserved[name] <= 1 {
		delete(c.reserved, name)
		return
	}
	c.reserved[name]--
}

// apiClientFor returns the cached X client for an account, building it on first use.
func (c *Client) apiClientFor(account *store.Account) (*xapi.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if api, ok := c.apiClients[account.Name]; ok {
		return api, nil
	}

	api, err := xapi.NewClient(xapi.Credentials{
		AuthToken: account.AuthToken,
		CT0:       account.CT0,
	})
	if err != nil {
		return nil, fmt.Errorf("account %q: %w", account.Name, err)
	}
	c.apiClients[account.Name] = api
	return api, nil
}

// pick selects the account for the next call.
func (c *Client) pick() (*store.Account, error) {
	if c.pinned != "" {
		return c.store.Get(c.pinned)
	}

	c.mu.Lock()
	lastUsed := c.lastUsedName
	c.mu.Unlock()

	accounts := c.eligibleAccounts()
	account, err := rotation.Pick(accounts, c.strategy, lastUsed)
	if err != nil {
		return nil, fmt.Errorf("selecting account: %w", err)
	}
	return account, nil
}

func (c *Client) eligibleAccounts() []store.Account {
	accounts := c.store.List()
	if c.allowed != nil {
		filtered := make([]store.Account, 0, len(c.allowed))
		for _, account := range accounts {
			if _, ok := c.allowed[account.Name]; ok {
				filtered = append(filtered, account)
			}
		}
		accounts = filtered
	}
	return accounts
}

func (c *Client) setLastUsed(name string) {
	c.mu.Lock()
	c.lastUsedName = name
	c.mu.Unlock()
}

// AncestorChain walks up from targetID through InReplyToStatusID and returns
// the tweets above it, ordered root-first. The target itself is excluded.
//
// Tweets whose parent is absent from the thread stop the walk, and cyclic data
// is cut short rather than looping.
func AncestorChain(thread []Tweet, targetID string) []Tweet {
	byID := make(map[string]Tweet, len(thread))
	for _, t := range thread {
		byID[t.ID] = t
	}

	target, ok := byID[targetID]
	if !ok {
		return nil
	}

	var chain []Tweet
	seen := map[string]bool{targetID: true}
	for parentID := target.InReplyToStatusID; parentID != ""; {
		if seen[parentID] {
			break
		}
		seen[parentID] = true

		parent, found := byID[parentID]
		if !found {
			break
		}
		chain = append(chain, parent)
		parentID = parent.InReplyToStatusID
	}

	// chain is parent-first; reverse so the root leads.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}
