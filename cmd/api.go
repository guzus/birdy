package cmd

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/guzus/birdy/internal/claude"
	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
)

type apiError struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type apiCommandRequest struct {
	Command  string   `json:"command,omitempty"`
	Args     []string `json:"args,omitempty"`
	Account  string   `json:"account,omitempty"`
	Strategy string   `json:"strategy,omitempty"`
}

type apiCommandResponse struct {
	OK        bool   `json:"ok"`
	Account   string `json:"account"`
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	DurationM int64  `json:"duration_ms"`
}

type apiChatRequest struct {
	Prompt string `json:"prompt"`
	Model  string `json:"model,omitempty"`
}

var apiAllowedBirdCommands = map[string]struct{}{
	"about":         {},
	"bookmarks":     {},
	"check":         {},
	"follow":        {},
	"followers":     {},
	"following":     {},
	"home":          {},
	"likes":         {},
	"list-timeline": {},
	"lists":         {},
	"mentions":      {},
	"news":          {},
	"query-ids":     {},
	"read":          {},
	"replies":       {},
	"reply":         {},
	"search":        {},
	"thread":        {},
	"tweet":         {},
	"unbookmark":    {},
	"unfollow":      {},
	"user-tweets":   {},
	"whoami":        {},
}

func hostRequestInviteCode(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Invite-Code")); v != "" {
		return v
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return ""
	}
	const bearer = "bearer "
	if len(auth) >= len(bearer) && strings.EqualFold(auth[:len(bearer)], bearer) {
		return strings.TrimSpace(auth[len(bearer):])
	}
	return ""
}

func apiAuthorized(r *http.Request, inviteCode string) bool {
	got := hostRequestInviteCode(r)
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(inviteCode), []byte(got)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func handleAPICommand(inviteCode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !apiAuthorized(r, inviteCode) {
			writeJSON(w, http.StatusUnauthorized, apiError{OK: false, Error: "unauthorized"})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		defer r.Body.Close()

		var req apiCommandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "invalid json"})
			return
		}

		cmdName := strings.TrimSpace(req.Command)
		args := make([]string, 0, 1+len(req.Args))
		if cmdName != "" {
			args = append(args, cmdName)
			args = append(args, req.Args...)
		} else if len(req.Args) > 0 {
			args = append(args, req.Args...)
		}
		if len(args) == 0 {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "missing command"})
			return
		}

		// This API is intentionally limited to the bird commands that birdy forwards
		// (see cmd/bird_commands.go) so it can't be used to run arbitrary subcommands.
		first := firstBirdCommand(args)
		if first == "" {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "missing command"})
			return
		}
		if _, ok := apiAllowedBirdCommands[first]; !ok {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "unsupported command"})
			return
		}

		if blocked, name := isReadOnlyBirdCommand(args); blocked {
			writeJSON(w, http.StatusForbidden, apiError{OK: false, Error: fmt.Sprintf("%q is disabled in read-only mode", name)})
			return
		}

		start := time.Now()

		st, err := store.Open()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: "opening account store"})
			return
		}
		if st.Len() == 0 {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "no accounts configured"})
			return
		}

		var account *store.Account
		accountName := strings.TrimSpace(req.Account)
		if accountName != "" {
			account, err = st.Get(accountName)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: err.Error()})
				return
			}
		} else {
			strat := strategyFlag
			if strings.TrimSpace(req.Strategy) != "" {
				strat = strings.TrimSpace(req.Strategy)
			}
			parsed, err := rotation.ParseStrategy(strat)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "invalid strategy"})
				return
			}

			rs, err := state.Load()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: "loading rotation state"})
				return
			}
			account, err = rotation.Pick(st.List(), parsed, rs.LastUsedName)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: err.Error()})
				return
			}

			rs.LastUsedName = account.Name
			_ = rs.Save()
			accountName = account.Name
		}

		_ = st.RecordUsage(account.Name)
		_ = st.Save()

		exitCode, stdout, stderr, runErr := runner.RunCapture(account, args)
		if runErr != nil {
			writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: runErr.Error()})
			return
		}

		writeJSON(w, http.StatusOK, apiCommandResponse{
			OK:        true,
			Account:   account.Name,
			ExitCode:  exitCode,
			Stdout:    stdout,
			Stderr:    stderr,
			DurationM: time.Since(start).Milliseconds(),
		})
	}
}

func handleAPIChat(inviteCode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !apiAuthorized(r, inviteCode) {
			writeJSON(w, http.StatusUnauthorized, apiError{OK: false, Error: "unauthorized"})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
		defer r.Body.Close()

		var req apiChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "invalid json"})
			return
		}
		prompt := strings.TrimSpace(req.Prompt)
		if prompt == "" {
			writeJSON(w, http.StatusBadRequest, apiError{OK: false, Error: "missing prompt"})
			return
		}
		model := strings.TrimSpace(req.Model)
		if model == "" {
			model = "sonnet"
		}

		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
		defer cancel()

		exePath, err := os.Executable()
		if err != nil || strings.TrimSpace(exePath) == "" {
			exePath = "birdy"
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, apiError{OK: false, Error: "streaming unsupported"})
			return
		}

		enc := json.NewEncoder(w)
		emit := func(ev claude.Event) {
			// SSE: event + json payload
			_, _ = fmt.Fprintf(w, "event: %s\n", ev.Type)
			_, _ = fmt.Fprint(w, "data: ")
			_ = enc.Encode(ev)
			_, _ = fmt.Fprint(w, "\n")
			flusher.Flush()
		}

		claude.Stream(ctx, prompt, model, exePath, emit)
	}
}
