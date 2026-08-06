// Package tweet is birdy's public Go API for reading tweets.
//
// It exposes the account rotation that birdy's CLI already performs — pick an
// account, run the bird CLI with its credentials, record usage and rate limits —
// as an embeddable library, so a Go service can read X without shelling out to
// the birdy binary or standing up a hosted birdy instance.
//
// A Client is safe for concurrent use and keeps rotation state in memory, so it
// works on a read-only filesystem (Cloud Run, scratch containers). Accounts are
// normally supplied through the BIRDY_ACCOUNTS environment variable:
//
//	BIRDY_ACCOUNTS=[{"name":"main","auth_token":"...","ct0":"..."}]
//
// Reading still requires the bird CLI (https://github.com/steipete/bird) to be
// installed, since birdy drives it as a subprocess. Point BIRDY_BIRD_PATH at
// bird's cli.js when it is not on PATH.
package tweet

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/store"
)

// Author identifies who posted a tweet.
type Author struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

// Media is a single attachment on a tweet.
//
// For photos, URL is the image. For videos and animated GIFs, URL is the still
// thumbnail and VideoURL is the playable mp4 — use DownloadURL to get whichever
// one actually holds the asset.
type Media struct {
	Type       string `json:"type"`
	URL        string `json:"url"`
	PreviewURL string `json:"previewUrl,omitempty"`
	VideoURL   string `json:"videoUrl,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

// IsVideo reports whether this attachment has playable video.
func (m Media) IsVideo() bool {
	return m.VideoURL != ""
}

// DownloadURL returns the URL holding the actual asset bytes.
func (m Media) DownloadURL() string {
	if m.VideoURL != "" {
		return m.VideoURL
	}
	return m.URL
}

// Tweet is a single post as returned by the bird CLI.
type Tweet struct {
	ID                string  `json:"id"`
	Text              string  `json:"text"`
	CreatedAt         string  `json:"createdAt,omitempty"`
	ConversationID    string  `json:"conversationId,omitempty"`
	InReplyToStatusID string  `json:"inReplyToStatusId,omitempty"`
	Author            Author  `json:"author"`
	AuthorID          string  `json:"authorId,omitempty"`
	Media             []Media `json:"media,omitempty"`
	ReplyCount        int     `json:"replyCount,omitempty"`
	RetweetCount      int     `json:"retweetCount,omitempty"`
	LikeCount         int     `json:"likeCount,omitempty"`
}

// IsReply reports whether this tweet sits below the root of a conversation.
func (t Tweet) IsReply() bool {
	if t.InReplyToStatusID != "" {
		return true
	}
	return t.ConversationID != "" && t.ConversationID != t.ID
}

// URL returns a canonical permalink for the tweet.
func (t Tweet) URL() string {
	if t.Author.Username != "" {
		return "https://x.com/" + t.Author.Username + "/status/" + t.ID
	}
	return "https://x.com/i/status/" + t.ID
}

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

// Client reads tweets through a rotating pool of X accounts.
type Client struct {
	store    *store.Store
	strategy rotation.Strategy
	pinned   string

	mu           sync.Mutex
	lastUsedName string
}

// NewClient builds a Client from the given options.
//
// It fails when no accounts are configured, since every call needs credentials.
func NewClient(opts Options) (*Client, error) {
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

	return &Client{store: st, strategy: strategy, pinned: pinned}, nil
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
		names = append(names, a.Name)
	}
	return names
}

// Read fetches a single tweet. ref may be a status URL or a bare tweet ID.
func (c *Client) Read(ctx context.Context, ref string) (*Tweet, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("read: empty tweet reference")
	}

	stdout, err := c.run(ctx, "read", ref, "--json")
	if err != nil {
		return nil, err
	}
	return decodeTweet(stdout)
}

// Thread fetches every tweet in a conversation, root first. ref may be a status
// URL or a bare tweet ID for any tweet in the conversation.
//
// bird returns the conversation flat; ancestry is expressed through each
// tweet's InReplyToStatusID. Use AncestorChain to walk it.
func (c *Client) Thread(ctx context.Context, ref string) ([]Tweet, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("thread: empty tweet reference")
	}

	stdout, err := c.run(ctx, "thread", ref, "--json")
	if err != nil {
		return nil, err
	}
	return decodeTweets(stdout)
}

// run picks an account, invokes bird, and records the outcome.
func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	account, err := c.pick()
	if err != nil {
		return "", err
	}

	res, stdout, stderr, err := runner.RunCaptureContext(ctx, account, args, runner.Options{})

	// Record the outcome even on failure: a 429 is exactly the signal the
	// quota-aware strategy needs to route around this account next time.
	if res.RateLimited {
		_ = c.store.RecordRateLimit(account.Name)
	}
	_ = c.store.RecordUsage(account.Name)
	_ = c.store.Save() // no-op for env-backed stores
	c.setLastUsed(account.Name)

	if err != nil {
		return "", fmt.Errorf("bird %s (account %q): %w", args[0], account.Name, err)
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = fmt.Sprintf("exit code %d", res.ExitCode)
		}
		if res.RateLimited {
			return "", fmt.Errorf("bird %s: rate limited on account %q: %s", args[0], account.Name, detail)
		}
		return "", fmt.Errorf("bird %s failed on account %q: %s", args[0], account.Name, detail)
	}
	return stdout, nil
}

// pick selects the account for the next call.
func (c *Client) pick() (*store.Account, error) {
	if c.pinned != "" {
		return c.store.Get(c.pinned)
	}

	c.mu.Lock()
	lastUsed := c.lastUsedName
	c.mu.Unlock()

	account, err := rotation.Pick(c.store.List(), c.strategy, lastUsed)
	if err != nil {
		return nil, fmt.Errorf("selecting account: %w", err)
	}
	return account, nil
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

// decodeTweet extracts a single tweet from bird's stdout.
func decodeTweet(stdout string) (*Tweet, error) {
	payload, err := sliceJSON(stdout, '{', '}')
	if err != nil {
		return nil, err
	}

	var t Tweet
	if err := json.Unmarshal([]byte(payload), &t); err != nil {
		return nil, fmt.Errorf("decoding tweet json: %w", err)
	}
	if t.ID == "" {
		return nil, fmt.Errorf("bird returned an unrecognized tweet payload")
	}
	return &t, nil
}

// decodeTweets extracts an array of tweets from bird's stdout.
func decodeTweets(stdout string) ([]Tweet, error) {
	payload, err := sliceJSON(stdout, '[', ']')
	if err != nil {
		return nil, err
	}

	var tweets []Tweet
	if err := json.Unmarshal([]byte(payload), &tweets); err != nil {
		return nil, fmt.Errorf("decoding thread json: %w", err)
	}
	return tweets, nil
}

// sliceJSON narrows stdout to its outermost JSON value. bird prefixes output
// with human-readable progress lines ("ℹ️ Looking up ..."), so decoding the raw
// stream would fail.
func sliceJSON(stdout string, open, close byte) (string, error) {
	start := strings.IndexByte(stdout, open)
	end := strings.LastIndexByte(stdout, close)
	if start < 0 || end <= start {
		return "", fmt.Errorf("bird returned no tweet data")
	}
	return stdout[start : end+1], nil
}
