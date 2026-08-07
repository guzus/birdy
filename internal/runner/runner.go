package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/guzus/birdy/internal/store"
)

// Result reports the outcome of a bird invocation.
type Result struct {
	ExitCode    int
	RateLimited bool // bird's stderr contained an HTTP 429 marker
}

// Options carries optional knobs for a bird invocation. Mutually-
// exclusive callers (Run vs RunCapture) share the same struct so adding
// new options doesn't double the API surface.
type Options struct {
	// ExtraEnv is appended to the subprocess environment, after the
	// AUTH_TOKEN/CT0 vars derived from the account. Used for VPN
	// routing (HTTPS_PROXY + NODE_OPTIONS).
	ExtraEnv []string
}

// Run executes the bird CLI with the given account's credentials and args.
// It passes auth_token and ct0 as environment variables and detects 429s
// from bird's stderr so the caller can record per-account quota state.
func Run(account *store.Account, args []string) (Result, error) {
	return RunWith(account, args, Options{})
}

// RunWith is Run plus extra subprocess options (currently just ExtraEnv).
func RunWith(account *store.Account, args []string, opts Options) (Result, error) {
	return runWithIO(account, args, opts, os.Stdin, os.Stdout, os.Stderr)
}

// RunCapture executes the bird CLI and captures stdout/stderr. The Result
// reports whether bird's stderr contained an HTTP 429 marker.
func RunCapture(account *store.Account, args []string) (res Result, stdout, stderr string, err error) {
	return RunCaptureWith(account, args, Options{})
}

// RunCaptureWith is RunCapture plus extra subprocess options.
func RunCaptureWith(account *store.Account, args []string, opts Options) (res Result, stdout, stderr string, err error) {
	return RunCaptureContext(context.Background(), account, args, opts)
}

// RunCaptureContext is RunCaptureWith bound to a context. Cancelling the
// context kills the bird subprocess, so long-running or hung invocations
// cannot outlive the caller — important for servers that embed birdy and
// must bound a request's lifetime.
func RunCaptureContext(ctx context.Context, account *store.Account, args []string, opts Options) (res Result, stdout, stderr string, err error) {
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	res, err = runWithIOContext(ctx, account, args, opts, nil, &outBuf, &errBuf)
	return res, outBuf.String(), errBuf.String(), err
}

// rateLimitScanner sniffs writes for bird's 429 markers. bird (as of 0.8.0)
// formats failed HTTP responses as "HTTP 429: ..." on stderr after its
// internal retry budget is exhausted (see
// bird/dist/lib/twitter-client-timelines.js).
type rateLimitScanner struct {
	dst     io.Writer
	matched bool
}

func (s *rateLimitScanner) Write(p []byte) (int, error) {
	if !s.matched && bytes.Contains(p, []byte("HTTP 429")) {
		s.matched = true
	}
	if s.dst == nil {
		return len(p), nil
	}
	return s.dst.Write(p)
}

func adaptWriter(w any) io.Writer {
	switch v := w.(type) {
	case *os.File:
		return v
	case *bytes.Buffer:
		return v
	case io.Writer:
		return v
	}
	return nil
}

func runWithIO(account *store.Account, args []string, opts Options, stdin any, stdout any, stderr any) (Result, error) {
	return runWithIOContext(context.Background(), account, args, opts, stdin, stdout, stderr)
}

func runWithIOContext(ctx context.Context, account *store.Account, args []string, opts Options, stdin any, stdout any, stderr any) (Result, error) {
	if account == nil {
		return Result{ExitCode: 1}, fmt.Errorf("running bird: missing account")
	}

	birdBin, err := findBird()
	if err != nil {
		return Result{ExitCode: 1}, err
	}

	cmd := exec.CommandContext(ctx, birdBin, args...)
	if stdin != nil {
		if r, ok := stdin.(*os.File); ok {
			cmd.Stdin = r
		}
	}
	cmd.Stdout = adaptWriter(stdout)
	scan := &rateLimitScanner{dst: adaptWriter(stderr)}
	cmd.Stderr = scan

	cmd.Env = buildEnv(account)
	if len(opts.ExtraEnv) > 0 {
		cmd.Env = append(cmd.Env, opts.ExtraEnv...)
	}

	if err := cmd.Run(); err != nil {
		// A cancelled context surfaces as a kill signal; report the context
		// error so callers can distinguish it from a genuine bird failure.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Result{ExitCode: 1, RateLimited: scan.matched}, fmt.Errorf("running bird: %w", ctxErr)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return Result{ExitCode: exitErr.ExitCode(), RateLimited: scan.matched}, nil
		}
		return Result{ExitCode: 1, RateLimited: scan.matched}, fmt.Errorf("running bird: %w", err)
	}
	return Result{ExitCode: 0, RateLimited: scan.matched}, nil
}

// findBird locates the bird binary.
//
// Lookup order:
//   - BIRDY_BIRD_PATH (explicit override)
//   - PATH: birdy-bird, then bird
//   - next to the running birdy binary (bird / birdy-bird), which covers a
//     manual side-by-side install
//   - third_party/@steipete/bird/dist/cli.js, which exists only in a repo
//     checkout: it is the reference bird that scripts/diff-engines.sh and
//     scripts/gen-features.mjs read, and is deliberately NOT in the release
//   - bird_<goos>_<goarch> / birdy-bird_<goos>_<goarch>
func findBird() (string, error) {
	if p := strings.TrimSpace(os.Getenv("BIRDY_BIRD_PATH")); p != "" {
		if err := assertUsableBinary(p); err != nil {
			return "", fmt.Errorf("BIRDY_BIRD_PATH=%q is not usable: %w", p, err)
		}
		return p, nil
	}

	path, err := exec.LookPath("birdy-bird")
	if err == nil {
		return path, nil
	}

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		suffix := runtime.GOOS + "_" + runtime.GOARCH

		candidates := []string{
			filepath.Join(dir, "bird", "dist", "cli.js"),
			filepath.Join(dir, "third_party", "@steipete", "bird", "dist", "cli.js"),
			filepath.Join(dir, "bird"),
			filepath.Join(dir, "birdy-bird"),
			filepath.Join(dir, "bird_"+suffix),
			filepath.Join(dir, "birdy-bird_"+suffix),
		}
		if runtime.GOOS == "windows" {
			for _, c := range []string{
				filepath.Join(dir, "bird.exe"),
				filepath.Join(dir, "birdy-bird.exe"),
				filepath.Join(dir, "bird_"+suffix+".exe"),
				filepath.Join(dir, "birdy-bird_"+suffix+".exe"),
			} {
				candidates = append(candidates, c)
			}
		}

		for _, c := range candidates {
			if err := assertUsableBinary(c); err == nil {
				return c, nil
			}
		}
	}

	if wd, err := os.Getwd(); err == nil {
		c := filepath.Join(wd, "third_party", "@steipete", "bird", "dist", "cli.js")
		if err := assertUsableBinary(c); err == nil {
			return c, nil
		}
	}

	path, err = exec.LookPath("bird")
	if err == nil {
		return path, nil
	}

	return "", fmt.Errorf(
		"bird CLI not found, and --bird requires it.\n\n" +
			"birdy no longer ships bird: every command is served natively, so the " +
			"release is a single Go binary with no Node runtime.\n\n" +
			"--bird exists to run the old engine side by side with the new one, " +
			"which is how birdy's output is verified against it. If that is what " +
			"you want, install bird and re-run:\n\n" +
			"  npm install -g @steipete/bird\n\n" +
			"birdy looks for `birdy-bird` then `bird` on PATH, or BIRDY_BIRD_PATH.",
	)
}

func assertUsableBinary(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("is a directory")
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if st.Mode()&0o111 == 0 {
		return fmt.Errorf("not executable")
	}
	return nil
}

// buildEnv creates the environment for the bird subprocess.
func buildEnv(account *store.Account) []string {
	env := os.Environ()

	filtered := make([]string, 0, len(env))
	for _, e := range env {
		skip := false
		for _, prefix := range []string{
			"AUTH_TOKEN=",
			"CT0=",
			"TWITTER_AUTH_TOKEN=",
			"TWITTER_CT0=",
		} {
			if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, e)
		}
	}

	filtered = append(filtered,
		"AUTH_TOKEN="+account.AuthToken,
		"CT0="+account.CT0,
	)
	return filtered
}
