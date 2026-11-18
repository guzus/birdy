package cmd

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/guzus/birdy/internal/birdtool"
	"github.com/guzus/birdy/internal/store"
)

// The managed chat's remote tool: Chubby's worker POSTs {"command","args"}
// with a bound secret header, and birdy runs the same public read the direct
// chat's birdy_command runs (runChubbyTool), with the same allowlist and
// argument checks. The X account pool never leaves birdy. Unlike the briefing
// tools, this one takes free-form arguments, so it only answers steps of runs
// this process started, and caps the reads one answer may make.

const (
	chatToolPath       = "/api/chat-tools/invited"
	chatToolName       = "birdy_command"
	chatToolSecretEnv  = "BIRDY_CHAT_TOOL_SECRET"
	chatManagedModeEnv = "BIRDY_CHAT_MANAGED"
	chatToolMaxOutput  = 60 << 10 // The worker fails a step over 64 KiB.
	chatToolRunCap     = 8        // reads one answer may make
	chatToolMaxArgs    = 12
	chatToolMaxArgLen  = 512
	chatToolFailed     = "Birdy could not complete this read."
)

// chatManagedMode is "" (off) or "trial": invited chats that send
// X-Birdy-Chat-Engine: managed take the managed path.
func chatManagedMode() string {
	if strings.TrimSpace(os.Getenv(chatManagedModeEnv)) == "trial" {
		return "trial"
	}
	return ""
}

// chatToolSecret is the configured secret, or "" while the tool is off. It
// must differ from the briefing tools' secrets: one shared value would let a
// briefing agent drive free-form reads.
func chatToolSecret() string {
	if chatManagedMode() == "" {
		return ""
	}
	secret := strings.TrimSpace(os.Getenv(chatToolSecretEnv))
	if len(secret) < scanToolMinSecret {
		return ""
	}
	for _, other := range []string{publicScanTool.secretEnv, invitedScanTool.secretEnv} {
		if strings.TrimSpace(os.Getenv(other)) == secret {
			return ""
		}
	}
	return secret
}

// chatToolParameters is the registered input schema. The schema only guides
// the model; chubbyNativeArgs is the rule.
func chatToolParameters() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"command", "args"},
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "enum": birdtool.ChubbyCommands()},
			"args": map[string]any{"type": "array", "maxItems": chatToolMaxArgs,
				"items": map[string]any{"type": "string", "maxLength": chatToolMaxArgLen}},
		},
	}
}

// chatManagedActive lists this process's running managed chat answers; the
// tool serves only their steps.
var chatManagedActive = activeRuns{ids: map[string]bool{}}

var (
	chatToolLimiter = newRateLimiter(120, time.Minute)
	chatToolRun     = runChubbyTool // replaced in tests
)

// chatToolStepRun finds the active managed chat run that owns stepID and
// reports how many reads that run has asked for up to and including this one.
func chatToolStepRun(ctx context.Context, stepID string) (runID string, reads int, ok bool) {
	ids := chatManagedActive.list()
	if stepID == "" || len(ids) == 0 {
		return "", 0, false
	}
	m, err := newScanManagedRunner()
	if err != nil {
		return "", 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, id := range ids {
		var d, err = m.Run(ctx, id)
		if err != nil {
			continue
		}
		sequence := -1
		for _, step := range d.Steps {
			if step.ID == stepID && step.Kind == "tool" && step.Name == chatToolName {
				sequence = step.Sequence
			}
		}
		if sequence < 0 {
			continue
		}
		for _, step := range d.Steps {
			if step.Kind == "tool" && step.Name == chatToolName && step.Sequence <= sequence {
				reads++
			}
		}
		return id, reads, true
	}
	return "", 0, false
}

// redactChatToolOutput removes what must never reach a model or a trace: the
// pool's account labels and credentials, and the tool's own secret.
func redactChatToolOutput(out, secret string) string {
	var secrets, labels []string
	if secret != "" {
		secrets = append(secrets, secret)
	}
	if st, err := store.Open(); err == nil {
		for _, a := range st.List() {
			for _, v := range []string{a.AuthToken, a.CT0} {
				if len(v) >= 8 {
					secrets = append(secrets, v)
				}
			}
			if len(a.Name) >= 3 {
				labels = append(labels, a.Name)
			}
		}
	}
	for _, s := range secrets {
		out = strings.ReplaceAll(out, s, "[redacted]")
	}
	// A label goes only as a whole word, so "acct" leaves "account" alone;
	// a label that is an ordinary word is removed from post text too.
	for _, l := range labels {
		out = replaceWord(out, l, "[redacted]")
	}
	return out
}

func replaceWord(s, word, with string) string {
	isWord := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	var b strings.Builder
	for {
		i := strings.Index(s, word)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		before, _ := utf8.DecodeLastRuneInString(s[:i])
		after, _ := utf8.DecodeRuneInString(s[i+len(word):])
		b.WriteString(s[:i])
		if (i > 0 && isWord(before)) || (i+len(word) < len(s) && isWord(after)) {
			b.WriteString(word)
		} else {
			b.WriteString(with)
		}
		s = s[i+len(word):]
	}
}

func handleChatTool() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		secret := chatToolSecret()
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
		if !chatToolLimiter.admit(w, r, 1) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		defer r.Body.Close()
		var args struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		if err := decodeStrictJSON(r, &args); err != nil || args.Args == nil || len(args.Args) > chatToolMaxArgs {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid arguments"})
			return
		}
		// Only a step of a chat answer this process is running may read, and
		// only so many times per answer: a leaked secret alone reads nothing.
		_, reads, ok := chatToolStepRun(r.Context(), r.Header.Get("Idempotency-Key"))
		if !ok {
			writeJSON(w, http.StatusForbidden, apiError{Error: "unknown run"})
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if reads > chatToolRunCap {
			_, _ = w.Write([]byte("Read limit for this answer reached. Answer from what you have."))
			return
		}
		// chubbyNativeArgs inside the runner enforces the command allowlist
		// and every argument rule, exactly as the direct chat does.
		out, err := chatToolRun(r.Context(), args.Command, args.Args)
		if err != nil {
			out = chatToolFailed
		}
		out = redactChatToolOutput(strings.ToValidUTF8(out, "�"), secret)
		if len(out) > chatToolMaxOutput {
			out = truncateUTF8(out, chatToolMaxOutput) + "\n[output truncated]"
		}
		_, _ = w.Write([]byte(out))
	}
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
