package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRailwayEntrypointRequiresRemoteExecutionConfiguration(t *testing.T) {
	baseEnv := []string{
		"HOME=" + t.TempDir(),
		"PATH=" + os.Getenv("PATH"),
	}

	tests := []struct {
		name    string
		env     []string
		wantErr string
	}{
		{
			name:    "invite code",
			wantErr: "BIRDY_HOST_INVITE_CODE is required",
		},
		{
			name:    "template",
			env:     []string{"BIRDY_HOST_INVITE_CODE=invite"},
			wantErr: "BIRDY_E2B_TEMPLATE is required",
		},
		{
			name: "E2B API key",
			env: []string{
				"BIRDY_HOST_INVITE_CODE=invite",
				"BIRDY_E2B_TEMPLATE=birdy-claude:production",
			},
			wantErr: "E2B_API_KEY is required",
		},
		{
			name: "birdy accounts",
			env: []string{
				"BIRDY_HOST_INVITE_CODE=invite",
				"BIRDY_E2B_TEMPLATE=birdy-claude:production",
				"E2B_API_KEY=e2b-test-key",
			},
			wantErr: "BIRDY_ACCOUNTS is required",
		},
		{
			name: "Claude authentication",
			env: []string{
				"BIRDY_HOST_INVITE_CODE=invite",
				"BIRDY_E2B_TEMPLATE=birdy-claude:production",
				"E2B_API_KEY=e2b-test-key",
				`BIRDY_ACCOUNTS=[{"name":"test","auth_token":"token","ct0":"ct0"}]`,
			},
			wantErr: "Claude authentication is required",
		},
	}

	entrypoint := filepath.Join("..", "scripts", "entrypoint-railway.sh")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("sh", entrypoint)
			cmd.Env = append(append([]string(nil), baseEnv...), tt.env...)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected entrypoint to fail, output=%q", output)
			}
			if !strings.Contains(string(output), tt.wantErr) {
				t.Fatalf("expected %q, got %q", tt.wantErr, output)
			}
		})
	}
}
