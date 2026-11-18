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

// birdy scan-agent register: one-time setup of a managed briefing agent on
// the web host's own Chubby service key. It stores the tool's header secret
// in Chubby, registers the read-only tool and creates the agent, then prints
// their IDs (never the secret) for the web host's environment.

type scanAgentLimits struct {
	maxSteps, maxOutputTokens, timeoutSeconds int
	budgetUSD                                 float64
	reasoningEffort                           string
}

func scanAgentDefinitions(kind scanToolKind, endpoint, model, secretName, toolID string, limits scanAgentLimits) (tool, agent map[string]any) {
	tool = map[string]any{
		"name":        kind.name,
		"description": fmt.Sprintf("Read one Birdy briefing source (%s). Returns recent public posts with ids.", strings.Join(kind.keys(), ", ")),
		"parameters":  kind.parameters(),
		"transport":   "http", "endpoint": endpoint,
		"behavior": "read_only", "approvalRequired": false, "timeoutMs": 45_000,
		"secretBindings": []map[string]string{{"header": scanToolHeader, "secretName": secretName}},
	}
	name := "birdy-briefing-invited"
	if kind.name == publicScanTool.name {
		name = "birdy-briefing-public"
	}
	agent = map[string]any{
		"name": name, "model": model, "system": scanAgentSystemPrompt(kind), "toolIds": []string{toolID},
		"maxSteps": limits.maxSteps, "maxOutputTokens": limits.maxOutputTokens,
		"budgetUsd": limits.budgetUSD, "timeoutSeconds": limits.timeoutSeconds,
	}
	if limits.reasoningEffort != "" {
		agent["reasoningEffort"] = limits.reasoningEffort
	}
	return tool, agent
}

func newScanAgentCmd() *cobra.Command {
	var kindName, hostURL, toolID string
	limits := scanAgentLimits{}
	register := &cobra.Command{
		Use:   "register",
		Short: "Register a managed briefing tool and agent with Chubby (one-time setup)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kind := invitedScanTool
			path := "/api/scan-tools/invited"
			switch kindName {
			case "public":
				kind, path = publicScanTool, "/api/scan-tools/public"
			case "invited":
			default:
				return errors.New("--kind must be public or invited")
			}
			u, err := url.Parse(strings.TrimRight(hostURL, "/") + path)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return errors.New("--host must be the web host's public https URL")
			}
			secret := strings.TrimSpace(os.Getenv(kind.secretEnv))
			if len(secret) < scanToolMinSecret {
				return fmt.Errorf("%s must hold the tool secret (at least %d characters), the same value the web host uses", kind.secretEnv, scanToolMinSecret)
			}
			model := strings.TrimSpace(os.Getenv("CHUBBY_MODEL"))
			if model == "" || model == "auto" {
				return errors.New("CHUBBY_MODEL must name the exact model the agent runs on")
			}
			m, err := chubby.NewManaged()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			secretName := strings.ReplaceAll(kind.name, "_", "-") + "-key"
			if toolID == "" {
				if err := m.PutSecret(ctx, secretName, secret); err != nil {
					return fmt.Errorf("store tool secret: %w", err)
				}
				tool, _ := scanAgentDefinitions(kind, u.String(), model, secretName, "", limits)
				if toolID, err = m.RegisterTool(ctx, tool); err != nil || toolID == "" {
					return fmt.Errorf("register tool (list the service's tools before retrying; definitions are immutable): %v", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "tool %s\n", toolID)
			}
			_, agent := scanAgentDefinitions(kind, u.String(), model, secretName, toolID, limits)
			agentID, version, err := m.CreateAgent(ctx, agent)
			if err != nil {
				return fmt.Errorf("create agent (tool %s is registered; rerun with --tool-id %s): %w", toolID, toolID, err)
			}
			env := scanAgentInvitedEnv
			if kindName == "public" {
				env = scanAgentPublicEnv
			}
			fmt.Fprintf(cmd.OutOrStdout(), "agent %s version %d (tool %s)\nset %s=%s on the web host\n", agentID, version, toolID, env, agentID)
			return nil
		},
	}
	register.Flags().StringVar(&kindName, "kind", "", "public or invited")
	register.Flags().StringVar(&hostURL, "host", "", "the web host's public https URL")
	register.Flags().StringVar(&toolID, "tool-id", "", "an already registered tool: only create the agent")
	register.Flags().IntVar(&limits.maxSteps, "max-steps", len(scanSources)+2, "model steps per run")
	register.Flags().IntVar(&limits.maxOutputTokens, "max-output-tokens", 4096, "output tokens per model step")
	register.Flags().IntVar(&limits.timeoutSeconds, "timeout-seconds", 180, "run deadline")
	register.Flags().StringVar(&limits.reasoningEffort, "reasoning-effort", "", "model reasoning effort (none, low, medium, high); empty keeps the provider default")
	register.Flags().Float64Var(&limits.budgetUSD, "budget-usd", 5, "per-run model budget; must cover one step's reservation")
	root := &cobra.Command{Use: "scan-agent", Short: "Manage Birdy Web's managed briefing agents", Hidden: true}
	root.AddCommand(register)
	return root
}

func init() {
	rootCmd.AddCommand(newScanAgentCmd())
}
