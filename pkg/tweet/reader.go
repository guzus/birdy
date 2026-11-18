package tweet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Reader reads a single post by X status URL or decimal ID.
type Reader interface {
	Read(ctx context.Context, ref string) (*Tweet, error)
}

// ReaderOptions selects the source for individual post lookups.
type ReaderOptions struct {
	// NoCookie uses Birdy's public post server. Only ID, text and author are
	// available in this mode. No local account settings are read.
	NoCookie bool
	// PublicAPIURL is the Birdy public post server for NoCookie, as a base URL
	// ("https://birdy.guzus.xyz", the default when empty). It must be https,
	// or http on localhost or a loopback address.
	PublicAPIURL string
	// AccountOptions configures the native reader when NoCookie is false.
	AccountOptions Options
}

// NewReader creates a reusable single-post reader. The zero options use the
// same native client and account configuration as NewClient(Options{}).
func NewReader(opts ReaderOptions) (Reader, error) {
	if opts.NoCookie {
		return newPublicReader(opts.PublicAPIURL)
	}
	native, err := NewClient(opts.AccountOptions)
	if err != nil {
		return nil, err
	}
	return native, nil
}

// PublicPostError is an HTTP error from the Birdy public post server.
// RetryAfter is the suggested wait when the server supplies one.
type PublicPostError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *PublicPostError) Error() string {
	return fmt.Sprintf("birdy public post: HTTP %d %s", e.StatusCode, http.StatusText(e.StatusCode))
}

type publicReader struct {
	client   *http.Client
	endpoint string // ".../v1/posts/"
}

// DefaultPublicAPIURL is the Birdy public post server NoCookie uses by default.
const DefaultPublicAPIURL = "https://birdy.guzus.xyz"

func newPublicReader(base string) (publicReader, error) {
	endpoint, err := publicPostsEndpoint(base)
	if err != nil {
		return publicReader{}, err
	}
	return publicReader{client: &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, endpoint: endpoint}, nil
}

// publicPostsEndpoint validates a public API base URL and returns its posts
// endpoint. Only https leaves the machine; plain http is for a local server.
func publicPostsEndpoint(base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = DefaultPublicAPIURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("invalid public API URL %q", truncateRef(base))
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", fmt.Errorf("public API URL %q must use https (plain http only for localhost)", truncateRef(base))
		}
	default:
		return "", fmt.Errorf("public API URL %q must use https", truncateRef(base))
	}
	return strings.TrimRight(u.String(), "/") + "/v1/posts/", nil
}

func publicPostRef(ref string) (string, error) {
	id := strings.TrimSpace(ref)
	// Early X posts have IDs shorter than the native reader's bare-ID pattern.
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		var extractErr error
		id, extractErr = ExtractTweetID(ref)
		if extractErr != nil {
			return "", extractErr
		}
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != id {
		return "", fmt.Errorf("invalid public post ID %q", truncateRef(id))
	}
	return id, nil
}

func (r publicReader) Read(ctx context.Context, ref string) (*Tweet, error) {
	id, err := publicPostRef(ref)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint+id, nil)
	if err != nil {
		return nil, err
	}
	var post publicPost
	if err := r.get(req, &post); err != nil {
		return nil, err
	}
	if post.ID != id || post.Handle == "" {
		return nil, fmt.Errorf("birdy public post: invalid response for ID %s", id)
	}
	return post.tweet(), nil
}

type publicPost struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	Handle     string `json:"author_handle"`
	AuthorName string `json:"author_name"`
}

func (p publicPost) tweet() *Tweet {
	return &Tweet{ID: p.ID, Text: p.Text, Author: Author{Username: p.Handle, Name: p.AuthorName}}
}

// get sends req and decodes a 200 JSON body into out.
func (r publicReader) get(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")
	res, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("birdy public post: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		seconds, _ := strconv.ParseUint(res.Header.Get("Retry-After"), 10, 32)
		return &PublicPostError{StatusCode: res.StatusCode, RetryAfter: time.Duration(seconds) * time.Second}
	}
	const maxResponse = 1 << 20
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return err
	}
	if len(body) > maxResponse {
		return fmt.Errorf("birdy public post: response exceeds %d bytes", maxResponse)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("birdy public post: %w", err)
	}
	return nil
}

// MaxPublicSearch is the most posts one public search returns.
const MaxPublicSearch = 100

// SearchPublic searches Birdy's public post cache: posts the server has
// verified as public, newest first. It is not X search: the cache holds
// what the server has collected, and only ID, text and author come back.
// No local account settings are read.
func SearchPublic(ctx context.Context, opts ReaderOptions, query string, limit int) ([]*Tweet, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 200 || strings.ContainsRune(query, 0) {
		return nil, fmt.Errorf("public search needs a query of 1 to 200 bytes")
	}
	if limit < 1 || limit > MaxPublicSearch {
		return nil, fmt.Errorf("public search returns 1 to %d posts, not %d", MaxPublicSearch, limit)
	}
	r, err := newPublicReader(opts.PublicAPIURL)
	if err != nil {
		return nil, err
	}
	q := url.Values{"q": {query}, "limit": {strconv.Itoa(limit)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(r.endpoint, "/")+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var page struct {
		Posts []publicPost `json:"posts"`
	}
	if err := r.get(req, &page); err != nil {
		return nil, err
	}
	out := make([]*Tweet, 0, len(page.Posts))
	for _, p := range page.Posts {
		if _, err := publicPostRef(p.ID); err != nil || p.Handle == "" {
			return nil, fmt.Errorf("birdy public search: invalid post in response")
		}
		out = append(out, p.tweet())
	}
	return out, nil
}
