package tweet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	// AccountOptions configures the native reader when NoCookie is false.
	AccountOptions Options
}

// NewReader creates a reusable single-post reader. The zero options use the
// same native client and account configuration as NewClient(Options{}).
func NewReader(opts ReaderOptions) (Reader, error) {
	if opts.NoCookie {
		return publicReader{client: &http.Client{
			Timeout:       15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}, endpoint: "https://birdy.guzus.xyz/v1/posts/"}, nil
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
	endpoint string
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
	req.Header.Set("Accept", "application/json")
	res, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("birdy public post: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		seconds, _ := strconv.ParseUint(res.Header.Get("Retry-After"), 10, 32)
		return nil, &PublicPostError{StatusCode: res.StatusCode, RetryAfter: time.Duration(seconds) * time.Second}
	}
	const maxResponse = 1 << 20
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponse {
		return nil, fmt.Errorf("birdy public post: response exceeds %d bytes", maxResponse)
	}
	var post struct {
		ID         string `json:"id"`
		Text       string `json:"text"`
		Handle     string `json:"author_handle"`
		AuthorName string `json:"author_name"`
	}
	if err := json.Unmarshal(body, &post); err != nil {
		return nil, fmt.Errorf("birdy public post: %w", err)
	}
	if post.ID != id || post.Handle == "" {
		return nil, fmt.Errorf("birdy public post: invalid response for ID %s", id)
	}
	return &Tweet{ID: post.ID, Text: post.Text, Author: Author{Username: post.Handle, Name: post.AuthorName}}, nil
}
