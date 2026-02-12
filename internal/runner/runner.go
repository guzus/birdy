package runner

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/guzus/birdy/internal/store"
)

// Run executes the bird CLI with the given account's credentials and args.
// It passes auth_token and ct0 as environment variables.
func Run(account *store.Account, args []string) (int, error) {
	exitCode, _, _, err := runWithIO(account, args, os.Stdin, os.Stdout, os.Stderr)
	return exitCode, err
}

// RunCapture executes the bird CLI and captures stdout/stderr.
func RunCapture(account *store.Account, args []string) (exitCode int, stdout, stderr string, err error) {
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	exitCode, _, _, err = runWithIO(account, args, nil, &outBuf, &errBuf)
	return exitCode, outBuf.String(), errBuf.String(), err
}

func runWithIO(account *store.Account, args []string, stdin any, stdout any, stderr any) (exitCode int, out string, errOut string, err error) {
	birdBin, err := findBird()
	if err != nil {
		return 1, "", "", err
	}

	cmd := exec.Command(birdBin, args...)
	if stdin != nil {
		if r, ok := stdin.(*os.File); ok {
			cmd.Stdin = r
		}
		// For capture mode, stdin is nil.
	}
	if w, ok := stdout.(*os.File); ok {
		cmd.Stdout = w
	} else if w, ok := stdout.(*bytes.Buffer); ok {
		cmd.Stdout = w
	} else if w, ok := stdout.(interface{ Write([]byte) (int, error) }); ok {
		cmd.Stdout = w
	}
	if w, ok := stderr.(*os.File); ok {
		cmd.Stderr = w
	} else if w, ok := stderr.(*bytes.Buffer); ok {
		cmd.Stderr = w
	} else if w, ok := stderr.(interface{ Write([]byte) (int, error) }); ok {
		cmd.Stderr = w
	}

	cmd.Env = buildEnv(account)

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), "", "", nil
		}
		return 1, "", "", fmt.Errorf("running bird: %w", err)
	}
	return 0, "", "", nil
}

// findBird locates the bird binary.
//
// Lookup order:
// - BIRDY_BIRD_PATH (explicit override)
// - PATH: birdy-bird, then bird
// - next to the running birdy binary:
//   - bird/dist/cli.js (bundled npm package)
//   - third_party/@steipete/bird/dist/cli.js (vendored for repo builds)
//   - bird / birdy-bird
//   - bird_<goos>_<goarch> / birdy-bird_<goos>_<goarch>
func findBird() (string, error) {
	if p := os.Getenv("BIRDY_BIRD_PATH"); p != "" {
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
		"bird CLI not found.\n\nbirdy looks for:\n- `birdy-bird` on your PATH\n- a bundled bird package next to the birdy executable at `bird/dist/cli.js`\n- `bird` on your PATH\n\nInstall bird from https://github.com/steipete/bird, or reinstall birdy using the installer which bundles bird.",
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
