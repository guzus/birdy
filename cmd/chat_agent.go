package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/guzus/birdy/internal/chubby"
	"github.com/spf13/cobra"
)

// birdy chat-agent register: one-time setup of the managed chat agent (trial)
// on managed chat's own Chubby service key (BIRDY_CHAT_CHUBBY_API_KEY), run
// from where Chubby is reachable. It stores the tool's header secret in Chubby, registers the
// read-only birdy_command tool and creates the agent with the direct chat's
// system prompt, printing IDs (never the secret).

// chatAgentSystemPrompt is the direct chat's system prompt plus a search
// discipline. The direct chat may run up to 12 model turns; the agent has 8
// reads and 8 steps, and in the trial it gave up on "find a thread" after two
// reads where the direct chat searched six times.
const chatAgentSystemPrompt = chubbyChatSystemPrompt + `

When asked to find a post, thread or discussion, search thoroughly before answering:
- Run several searches (usually two to four) with different phrasings: the user's words, likely key terms or hashtags, and X search operators such as "exact phrase", from:handle, min_faves:100 or lang:en. Use -n 20.
- Compare the candidates by relevance and engagement, then read the best one with thread (or replies) before you describe it.
- Never conclude that nothing exists after a single search. If nothing fits after several, say what you searched for.
- You have at most 8 reads per answer, and each tool call is a read: plan them, stop when you have enough, and leave a read for the thread.`

func chatAgentDefinitions(endpoint, model, secretName, toolID string, limits scanAgentLimits) (tool, agent map[string]any) {
	tool = map[string]any{
		"name":        chatToolName,
		"description": "Run a public read-only Birdy X command. Pass the command name and its arguments separately.",
		"parameters":  chatToolParameters(),
		"transport":   "http", "endpoint": endpoint,
		"behavior": "read_only", "approvalRequired": false, "timeoutMs": 45_000,
		"secretBindings": []map[string]string{{"header": scanToolHeader, "secretName": secretName}},
	}
	agent = map[string]any{
		"name": "birdy-chat-invited", "model": model, "system": chatAgentSystemPrompt, "toolIds": []string{toolID},
		"maxSteps": limits.maxSteps, "maxOutputTokens": limits.maxOutputTokens,
		"budgetUsd": limits.budgetUSD, "timeoutSeconds": limits.timeoutSeconds,
	}
	if limits.reasoningEffort != "" {
		agent["reasoningEffort"] = limits.reasoningEffort
	}
	return tool, agent
}

func newChatAgentCmd() *cobra.Command {
	var hostURL, toolID, agentID string
	limits := scanAgentLimits{}
	register := &cobra.Command{
		Use:   "register",
		Short: "Register the managed chat tool and agent with Chubby (one-time setup)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if agentID != "" && toolID == "" {
				return errors.New("--agent-id needs --tool-id: a new version keeps the agent's tool")
			}
			u, err := url.Parse(strings.TrimRight(hostURL, "/") + chatToolPath)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return errors.New("--host must be the web host's public https URL")
			}
			secret := strings.TrimSpace(os.Getenv(chatToolSecretEnv))
			if len(secret) < scanToolMinSecret {
				return fmt.Errorf("%s must hold the tool secret (at least %d characters), the same value the web host uses", chatToolSecretEnv, scanToolMinSecret)
			}
			for _, other := range []string{publicScanTool.secretEnv, invitedScanTool.secretEnv} {
				if strings.TrimSpace(os.Getenv(other)) == secret {
					return fmt.Errorf("%s must differ from %s", chatToolSecretEnv, other)
				}
			}
			model := strings.TrimSpace(os.Getenv("CHUBBY_MODEL"))
			if model == "" || model == "auto" {
				return errors.New("CHUBBY_MODEL must name the exact model the agent runs on")
			}
			// Managed chat's own key: its tool, agent and budget live there.
			m, err := chubby.NewManagedKey(chatChubbyKeyEnv)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			const secretName = "birdy-command-key"
			if toolID == "" {
				if err := m.PutSecret(ctx, secretName, secret); err != nil {
					return fmt.Errorf("store tool secret: %w", err)
				}
				tool, _ := chatAgentDefinitions(u.String(), model, secretName, "", limits)
				if toolID, err = m.RegisterTool(ctx, tool); err != nil || toolID == "" {
					return fmt.Errorf("register tool (list the service's tools before retrying; definitions are immutable): %v", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "tool %s\n", toolID)
			}
			_, agent := chatAgentDefinitions(u.String(), model, secretName, toolID, limits)
			if agentID != "" {
				// A new version of the agent the web host already runs: new
				// conversations get it, running sessions keep theirs.
				delete(agent, "name")
				version, err := m.CreateVersion(ctx, agentID, agent)
				if err != nil {
					return fmt.Errorf("create version: %w", err)
				}
				if err := m.Deploy(ctx, agentID, version); err != nil {
					return fmt.Errorf("deploy version %d (created, not active): %w", version, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "agent %s version %d deployed (tool %s); %s is unchanged\n", agentID, version, toolID, chatAgentEnv)
				return nil
			}
			agentID, version, err := m.CreateAgent(ctx, agent)
			if err != nil {
				return fmt.Errorf("create agent (tool %s is registered; rerun with --tool-id %s): %w", toolID, toolID, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "agent %s version %d (tool %s)\nset %s=%s on the web host\n", agentID, version, toolID, chatAgentEnv, agentID)
			return nil
		},
	}
	register.Flags().StringVar(&hostURL, "host", "", "the web host's public https URL")
	register.Flags().StringVar(&toolID, "tool-id", "", "an already registered tool: only create the agent")
	register.Flags().StringVar(&agentID, "agent-id", "", "the existing agent: create and deploy a new version of it (needs --tool-id)")
	// The direct chat allows 12 model turns; a trial answer rarely needs more
	// than three reads.
	register.Flags().IntVar(&limits.maxSteps, "max-steps", 8, "model steps per answer")
	register.Flags().IntVar(&limits.maxOutputTokens, "max-output-tokens", 4096, "output tokens per model step")
	register.Flags().IntVar(&limits.timeoutSeconds, "timeout-seconds", 300, "answer deadline")
	register.Flags().StringVar(&limits.reasoningEffort, "reasoning-effort", "low", "model reasoning effort (none, low, medium, high); empty keeps the provider default")
	register.Flags().Float64Var(&limits.budgetUSD, "budget-usd", 1, "per-answer model budget; Chubby holds each step's estimated cost against it")
	root := &cobra.Command{Use: "chat-agent", Short: "Manage Birdy Web's managed chat agent (trial)", Hidden: true}
	root.AddCommand(register)
	return root
}

func init() {
	rootCmd.AddCommand(newChatAgentCmd())
}
