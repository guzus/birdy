package chubby

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Managed is a small client for Chubby's managed-agent API: the service owns
// only sessions and runs, while Chubby's workers run the model/tool loop.
type Managed struct {
	c *client
}

// NewManaged reads the same configuration as direct chat.
func NewManaged() (*Managed, error) { return NewManagedKey("CHUBBY_API_KEY") }

// NewManagedKey is NewManaged on the service key held in keyEnv, for a caller
// that runs under a separate Chubby identity and budget.
func NewManagedKey(keyEnv string) (*Managed, error) {
	c, err := fromEnvKey(keyEnv)
	if err != nil {
		return nil, err
	}
	c.http.Timeout = 30 * time.Second // Each control-plane call, not a run's lifetime.
	return &Managed{c: c}, nil
}

// APIError is a non-2xx answer. Message is Chubby's text, which never holds
// credentials but may name resources; callers log it and show users less.
type APIError struct {
	Status  int
	Code    string
	Message string
	// RetryAfter is the server's Retry-After in seconds, or 0 when absent.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Chubby managed API HTTP %d %s: %s", e.Status, e.Code, e.Message)
}

const maxManagedResponse = 4 << 20

func (m *Managed) base() string {
	return m.c.endpoint[:len(m.c.endpoint)-len("/chat/completions")]
}

func (m *Managed) call(ctx context.Context, method, path string, body any, idempotencyKey string, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	r, err := http.NewRequestWithContext(ctx, method, m.base()+path, reader)
	if err != nil {
		return errors.New("invalid Chubby managed endpoint")
	}
	r.GetBody = nil
	r.Header.Set("Authorization", "Bearer "+m.c.key)
	r.Header.Set("Accept", "application/json")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		r.Header.Set("Idempotency-Key", idempotencyKey)
	}
	res, err := m.c.http.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Chubby managed API connection failed")
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxManagedResponse+1))
	if err != nil || len(data) > maxManagedResponse {
		return errors.New("Chubby managed API response was unreadable")
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		msg := e.Error.Message
		if len(msg) > 300 {
			msg = msg[:300]
		}
		apiErr := &APIError{Status: res.StatusCode, Code: e.Error.Code, Message: msg}
		if seconds, err := strconv.Atoi(strings.TrimSpace(res.Header.Get("Retry-After"))); err == nil && seconds > 0 {
			apiErr.RetryAfter = time.Duration(seconds) * time.Second
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("Chubby managed API returned invalid JSON")
	}
	return nil
}

type ManagedSession struct {
	ID      string `json:"id"`
	AgentID string `json:"agentId"`
	Version int    `json:"version"`
}

type ManagedRun struct {
	ID          string  `json:"id"`
	SessionID   string  `json:"sessionId"`
	AgentID     string  `json:"agentId"`
	Version     int     `json:"version"`
	Status      string  `json:"status"`
	Output      *string `json:"output"`
	Error       *string `json:"error"`
	StepCount   int     `json:"stepCount"`
	BudgetUSD   float64 `json:"budgetUsd"`
	CostUSD     float64 `json:"costUsd"`
	ReservedUSD float64 `json:"reservedUsd"`
	UnknownCost bool    `json:"unknownCost"`
	CreatedAt   string  `json:"createdAt"`
	FinishedAt  *string `json:"finishedAt"`
}

type ManagedStep struct {
	ID       string  `json:"id"`
	Sequence int     `json:"sequence"`
	Kind     string  `json:"kind"`
	Status   string  `json:"status"`
	Name     string  `json:"name"`
	ToolID   *string `json:"toolId"`
	// Arguments is the tool call's JSON object (tool steps only).
	Arguments        json.RawMessage `json:"arguments"`
	Output           *string         `json:"output"`
	RequestID        *string         `json:"requestId"`
	TraceID          *string         `json:"traceId"`
	CostUSD          float64         `json:"costUsd"`
	InputTokens      *float64        `json:"inputTokens"`
	CacheReadTokens  *float64        `json:"cacheReadTokens"`
	CacheWriteTokens *float64        `json:"cacheWriteTokens"`
	StartedAt        *string         `json:"startedAt"`
	FinishedAt       *string         `json:"finishedAt"`
}

type ManagedEvent struct {
	ID        float64         `json:"id"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt string          `json:"createdAt"`
}

type ManagedRunDetail struct {
	Run    ManagedRun     `json:"run"`
	Steps  []ManagedStep  `json:"steps"`
	Events []ManagedEvent `json:"events"`
}

// Terminal reports whether a run will do no more work on its own. Paused
// states (waiting_approval, needs_attention) are not successes.
func (r ManagedRun) Terminal() bool {
	switch r.Status {
	case "succeeded", "failed", "cancelled", "waiting_approval", "needs_attention":
		return true
	}
	return false
}

func (m *Managed) CreateSession(ctx context.Context, agentID, title string) (ManagedSession, error) {
	var s ManagedSession
	err := m.call(ctx, http.MethodPost, "/sessions", map[string]any{"agentId": agentID, "title": title}, "", &s)
	return s, err
}

// StartRun submits a run. Repeating the same request with the same key returns
// the original run, so an ambiguous failure is retried with the same key.
func (m *Managed) StartRun(ctx context.Context, sessionID, input, idempotencyKey string) (ManagedRun, error) {
	var run ManagedRun
	err := m.call(ctx, http.MethodPost, "/runs", map[string]any{"sessionId": sessionID, "input": input}, idempotencyKey, &run)
	return run, err
}

func (m *Managed) Run(ctx context.Context, id string) (ManagedRunDetail, error) {
	var d ManagedRunDetail
	err := m.call(ctx, http.MethodGet, "/runs/"+url.PathEscape(id), nil, "", &d)
	return d, err
}

func (m *Managed) Events(ctx context.Context, id string, after float64) ([]ManagedEvent, error) {
	var page struct {
		Data []ManagedEvent `json:"data"`
	}
	err := m.call(ctx, http.MethodGet, "/runs/"+url.PathEscape(id)+"/events?after="+strconv.FormatFloat(after, 'f', -1, 64), nil, "", &page)
	return page.Data, err
}

// RunText is a model step's text as Chubby's live-text API reports it: a
// delta after the cursor the caller passed, provisional until the run ends.
type RunText struct {
	StepID      string  `json:"stepId"`
	Attempt     int     `json:"attempt"`
	Cursor      string  `json:"cursor"`
	Reset       bool    `json:"reset"`
	Delta       string  `json:"delta"`
	State       string  `json:"state"` // streaming, tools, answer, discarded, unavailable
	FirstTextAt *string `json:"firstTextAt"`
	Truncated   bool    `json:"truncated,omitempty"`
}

// EventsPage is one read of a run's events with live text.
type EventsPage struct {
	Data []ManagedEvent
	// Text is the step text new since the cursor, or nil when nothing is.
	Text *RunText
	// LiveText is false against a backend without the live-text API (the
	// response has no text key): the caller then polls as before.
	LiveText bool
}

// EventsPage reads events after a cursor and, when textCursor is set ("1" at
// first, then the cursor last returned), the live text. wait (0-20 s) holds the
// read until something changes; keep it under the client's 30 s timeout.
func (m *Managed) EventsPage(ctx context.Context, id string, after float64, textCursor string, wait int) (EventsPage, error) {
	path := "/runs/" + url.PathEscape(id) + "/events?after=" + strconv.FormatFloat(after, 'f', -1, 64)
	if textCursor != "" {
		path += "&text=" + url.QueryEscape(textCursor) + "&wait=" + strconv.Itoa(max(0, min(wait, 20)))
	}
	var raw struct {
		Data []ManagedEvent  `json:"data"`
		Text json.RawMessage `json:"text"`
	}
	if err := m.call(ctx, http.MethodGet, path, nil, "", &raw); err != nil {
		return EventsPage{}, err
	}
	page := EventsPage{Data: raw.Data, LiveText: raw.Text != nil}
	if len(raw.Text) > 0 && string(raw.Text) != "null" {
		var text RunText
		if err := json.Unmarshal(raw.Text, &text); err != nil || text.StepID == "" || text.Cursor == "" {
			return EventsPage{}, errors.New("Chubby managed API returned invalid live text")
		}
		page.Text = &text
	}
	return page, nil
}

// DeleteSession removes an inactive session's conversation and run content.
func (m *Managed) DeleteSession(ctx context.Context, id string) error {
	return m.call(ctx, http.MethodDelete, "/sessions/"+url.PathEscape(id), nil, "", nil)
}

// Cancel stops future work on a run. It cannot undo a dispatched tool call.
func (m *Managed) Cancel(ctx context.Context, id string) error {
	return m.call(ctx, http.MethodPost, "/runs/"+url.PathEscape(id)+"/cancel", map[string]any{}, "", nil)
}

// Setup calls, used once by an operator-run command, never per request.

func (m *Managed) PutSecret(ctx context.Context, name, value string) error {
	return m.call(ctx, http.MethodPut, "/secrets/"+url.PathEscape(name), map[string]string{"value": value}, "", nil)
}

func (m *Managed) RegisterTool(ctx context.Context, tool map[string]any) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	err := m.call(ctx, http.MethodPost, "/tools", tool, "", &out)
	return out.ID, err
}

// CreateVersion adds a version to an agent (config is an agent definition
// without its name) and returns its number. Sessions keep the version they
// started on; Deploy makes it the one new sessions get.
func (m *Managed) CreateVersion(ctx context.Context, agentID string, config map[string]any) (int, error) {
	var out struct {
		Version int `json:"version"`
	}
	err := m.call(ctx, http.MethodPost, "/agents/"+url.PathEscape(agentID)+"/versions", config, "", &out)
	return out.Version, err
}

// Deploy makes version the agent's active version.
func (m *Managed) Deploy(ctx context.Context, agentID string, version int) error {
	return m.call(ctx, http.MethodPost, "/agents/"+url.PathEscape(agentID)+"/deploy", map[string]int{"version": version}, "", nil)
}

func (m *Managed) CreateAgent(ctx context.Context, agent map[string]any) (string, int, error) {
	var out struct {
		Agent struct {
			ID string `json:"id"`
		} `json:"agent"`
		Versions []struct {
			Version int `json:"version"`
		} `json:"versions"`
	}
	err := m.call(ctx, http.MethodPost, "/agents", agent, "", &out)
	version := 0
	if len(out.Versions) > 0 {
		version = out.Versions[len(out.Versions)-1].Version
	}
	return out.Agent.ID, version, err
}
