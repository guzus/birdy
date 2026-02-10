package runner

import (
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
	birdBin, err := findBird()
	if err != nil {
		return 1, err
	}

	cmd := exec.Command(birdBin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Inherit current environment and override auth tokens.
	cmd.Env = buildEnv(account)

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, fmt.Errorf("running bird: %w", err)
	}
	return 0, nil
}

// findBird locates the bird binary.
//
// Lookup order:
// - BIRDY_BIRD_PATH (explicit override)
// - PATH: bird, then birdy-bird
// - next to the running birdy binary:
//   - bird / birdy-bird
//   - bird_<goos>_<goarch> / birdy-bird_<goos>_<goarch>
func findBird() (string, error) {
	if p := os.Getenv("BIRDY_BIRD_PATH"); p != "" {
		if err := assertUsableBinary(p); err != nil {
			return "", fmt.Errorf("BIRDY_BIRD_PATH=%q is not usable: %w", p, err)
		}
		return p, nil
	}

	path, err := exec.LookPath("bird")
	if err == nil {
		return path, nil
	}

	path, err = exec.LookPath("birdy-bird")
	if err == nil {
		return path, nil
	}

	// Common local-dev location (NVM-installed bird). This is intentionally
	// best-effort and only used if present.
	if err := assertUsableBinary("/Users/alphanonce/.nvm/versions/node/v22.14.0/bin/bird"); err == nil {
		return "/Users/alphanonce/.nvm/versions/node/v22.14.0/bin/bird", nil
	}

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		suffix := runtime.GOOS + "_" + runtime.GOARCH

		candidates := []string{
			filepath.Join(dir, "bird"),
			filepath.Join(dir, "birdy-bird"),
			filepath.Join(dir, "bird_"+suffix),
			filepath.Join(dir, "birdy-bird_"+suffix),
		}
		if runtime.GOOS == "windows" {
			// exec.Command on Windows generally needs the .exe suffix if provided as a path.
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

	return "", fmt.Errorf(
		"bird CLI not found.\n\nbirdy looks for:\n- `bird` or `birdy-bird` on your PATH\n- a bundled bird binary next to the birdy executable (e.g. `bird_%s_%s`)\n\nInstall bird from https://github.com/steipete/bird, or reinstall birdy using the installer which bundles bird.",
		runtime.GOOS,
		runtime.GOARCH,
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
		// Best-effort: on Windows, existence is usually sufficient.
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

	// Remove any existing auth-related env vars to avoid conflicts.
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

	// Inject the account credentials.
	filtered = append(filtered,
		"AUTH_TOKEN="+account.AuthToken,
		"CT0="+account.CT0,
	)
	return filtered
}
