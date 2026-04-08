package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionRoutesOutputToCommandWriter(t *testing.T) {
	var out bytes.Buffer
	versionCmd.SetOut(&out)

	versionCmd.Run(versionCmd, nil)

	if !strings.Contains(out.String(), "birdy ") {
		t.Fatalf("expected version output in command writer, got %q", out.String())
	}
}

func TestVersionRejectsUnexpectedArgs(t *testing.T) {
	if err := versionCmd.Args(versionCmd, []string{"extra"}); err == nil {
		t.Fatal("expected version to reject unexpected args")
	}
}
