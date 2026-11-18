package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/guzus/birdy/internal/birdtool"
	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/store"
	"github.com/guzus/birdy/internal/xapi"
)

const chubbyOutputLimit = 12 << 10

var hostedChubbyAccounts chubbyAccounts

type chubbyAccounts struct {
	mu        sync.Mutex
	ephemeral *store.Store
}

// Env-only pools have no usage file, so keep their counters across tool calls.
// Reopen disk-backed pools under the lock before each update to see account edits.
func (p *chubbyAccounts) open() (*store.Store, error) {
	if p.ephemeral != nil {
		return p.ephemeral, nil
	}
	st, err := store.Open()
	if err == nil && st.IsEphemeral() {
		p.ephemeral = st
	}
	return st, err
}

func (p *chubbyAccounts) reserve() (*store.Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, err := p.open()
	if err != nil {
		return nil, err
	}
	acc, err := rotation.Pick(st.List(), rotation.QuotaAware, "")
	if err != nil {
		return nil, err
	}
	if err = st.RecordUsage(acc.Name); err != nil {
		return nil, err
	}
	if err = st.Save(); err != nil {
		return nil, err
	}
	return acc, nil
}

func (p *chubbyAccounts) rateLimited(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, err := p.open(); err == nil && st.RecordRateLimit(name) == nil {
		_ = st.Save()
	}
}

type chubbyOutput struct {
	buf       bytes.Buffer
	truncated bool
}

func (o *chubbyOutput) Write(data []byte) (int, error) {
	n := min(len(data), chubbyOutputLimit-o.buf.Len())
	_, _ = o.buf.Write(data[:n])
	if n < len(data) {
		o.truncated = true
	}
	return len(data), nil
}

func (o *chubbyOutput) String() string {
	if !o.truncated {
		return o.buf.String()
	}
	const suffix = "\n[output truncated]"
	b := o.buf.Bytes()
	n := chubbyOutputLimit - len(suffix)
	for n > 0 && !utf8.RuneStart(b[n]) {
		n--
	}
	return string(b[:n]) + suffix
}

func chubbyNativeArgs(command string, args []string) ([]string, error) {
	if !slices.Contains(birdtool.ChubbyCommands(), command) {
		return nil, errors.New("unsupported public X tool")
	}
	invalid := errors.New("invalid public X tool arguments")
	if len(args) > 12 || helpRequested(args) || !nativeAcceptsFlags(command, args) {
		return nil, invalid
	}
	for _, arg := range args {
		if len(arg) > 512 || strings.ContainsRune(arg, 0) {
			return nil, invalid
		}
	}
	parsed := parseNativeArgs(args)
	if parsed.countErr != nil || parsed.filterErr != nil || parsed.count > 20 {
		return nil, invalid
	}
	switch command {
	case "home":
		if len(parsed.positionals) != 0 {
			return nil, invalid
		}
	case "news":
		if len(parsed.positionals) > 0 || len(parsed.newsTabs) > 1 || slices.Contains(parsed.newsTabs, "forYou") {
			return nil, invalid
		}
		// The normal CLI default includes a tab personalized to the reading account.
		if len(parsed.newsTabs) == 0 {
			args = append([]string{"--news-only"}, args...)
		}
	default:
		if len(parsed.positionals) != 1 || strings.TrimSpace(parsed.positional) == "" {
			return nil, invalid
		}
		switch command {
		case "about", "user-tweets":
			if _, ok := cliHandle(parsed.positional); !ok {
				return nil, invalid
			}
		case "read", "thread", "replies":
			if _, err := resolveTweetID(parsed.positional); err != nil {
				return nil, invalid
			}
		}
	}
	return append([]string{command}, args...), nil
}

func runChubbyTool(ctx context.Context, command string, args []string) (string, error) {
	argv, err := chubbyNativeArgs(command, args)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case apiCommandSem <- struct{}{}:
		defer func() { <-apiCommandSem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	account, err := hostedChubbyAccounts.reserve()
	if err != nil {
		return "", errors.New("X account pool unavailable")
	}
	var out chubbyOutput
	// Pass the approved command directly. CLI fallback and global flags are not model tools.
	err = (commandCapture{}).runNative(ctx, account, argv, &out, io.Discard)
	if xapi.IsRateLimited(err) {
		hostedChubbyAccounts.rateLimited(account.Name)
		return "", errors.New("X rate limit reached")
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", errors.New("public X read failed")
	}
	return out.String(), nil
}
