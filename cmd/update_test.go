package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestVerifyChecksumRejectsMismatch(t *testing.T) {
	archive, _ := os.CreateTemp(t.TempDir(), "a-*")
	archive.WriteString("payload")
	archive.Close()

	sums, _ := os.CreateTemp(t.TempDir(), "sums-*")
	sums.WriteString("0000000000000000000000000000000000000000000000000000000000000000  a.tar.gz\n")
	sums.Close()

	err := verifyChecksumFile(archive.Name(), sums.Name(), "a.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("a wrong checksum must abort the update, got %v", err)
	}
}

func TestVerifyChecksumRejectsMissingEntry(t *testing.T) {
	archive, _ := os.CreateTemp(t.TempDir(), "a-*")
	archive.WriteString("payload")
	archive.Close()

	sums, _ := os.CreateTemp(t.TempDir(), "sums-*")
	sums.WriteString("abc  something-else.tar.gz\n")
	sums.Close()

	err := verifyChecksumFile(archive.Name(), sums.Name(), "a.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "no checksum published") {
		t.Fatalf("an unlisted asset must abort the update, got %v", err)
	}
}

func TestHomebrewPathDetection(t *testing.T) {
	brewed := []string{
		"/opt/homebrew/Cellar/birdy/1.0.2/bin/birdy",
		"/usr/local/Cellar/birdy/1.0.2/bin/birdy",
		"/home/linuxbrew/.linuxbrew/bin/birdy",
	}
	for _, p := range brewed {
		if !isHomebrewPath(p) {
			t.Errorf("%s should be detected as Homebrew-managed", p)
		}
	}
	for _, p := range []string{"/usr/local/bin/birdy", "/home/u/go/bin/birdy", "/tmp/birdy"} {
		if isHomebrewPath(p) {
			t.Errorf("%s is not Homebrew-managed", p)
		}
	}
}
