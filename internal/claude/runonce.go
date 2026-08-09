package claude

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/guzus/birdy/internal/processenv"
)

// RunOnce invokes the claude CLI synchronously with birdy tool access and
// returns the final assistant text. Suitable for one-shot agentic tasks
// where streaming is not needed (digests, summaries, classifications).
func RunOnce(ctx context.Context, prompt, systemPrompt, model, birdyCmd string) (string, error) {
	args := []string{
		"-p", prompt,
		"--max-turns", "10",
		"--allowedTools", fmt.Sprintf("Bash(%s *)", birdyCmd),
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if systemPrompt != "" {
		args = append(args, "--append-system-prompt", systemPrompt)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Env = processenv.Without(os.Environ(), "OPENCODE_API_KEY")
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		stderr := strings.TrimSpace(errBuf.String())
		if stderr == "" {
			return "", fmt.Errorf("claude: %w", err)
		}
		return "", fmt.Errorf("claude: %w (stderr: %s)", err, stderr)
	}
	return strings.TrimSpace(out.String()), nil
}
