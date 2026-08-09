package birdtool

import "testing"

func TestModelCommandsExcludeIdentityAndHostManagement(t *testing.T) {
	for _, command := range []string{"whoami", "account", "status", "host", "tui", "vpn"} {
		for _, allowed := range ModelCommands() {
			if allowed == command {
				t.Fatalf("model command allowlist includes %q", command)
			}
		}
	}
	for _, command := range []string{"home", "search", "read", "thread", "tweet"} {
		if !APIAllowed(command) {
			t.Fatalf("API command allowlist lost %q", command)
		}
	}
}
