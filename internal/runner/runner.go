package runner

import (
	"fmt"
	"os"
	"os/exec"

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

// findBird locates the bird binary on PATH.
func findBird() (string, error) {
	path, err := exec.LookPath("bird")
	if err != nil {
		return "", fmt.Errorf("bird CLI not found in PATH: %w\nInstall it from https://github.com/steipete/bird", err)
	}
	return path, nil
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
