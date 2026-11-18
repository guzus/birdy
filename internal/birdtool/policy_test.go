package birdtool

import (
	"strings"
	"testing"
)

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

func TestChubbyCommandsAreScopedAndIndependent(t *testing.T) {
	allowed := ChubbyCommands()
	const want = "about home news read replies search thread user-tweets"
	if strings.Join(allowed, " ") != want {
		t.Fatalf("Chubby tool allowlist changed: %v", allowed)
	}
	allowed[0] = "tweet"
	if ChubbyCommands()[0] != "about" {
		t.Fatal("caller mutated shared Chubby policy")
	}
}
