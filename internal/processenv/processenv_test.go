package processenv

import (
	"slices"
	"testing"
)

func TestWithoutRemovesOnlyExactVariableNames(t *testing.T) {
	original := []string{"PATH=/bin", "OPENCODE_API_KEY=secret", "OPENCODE_API_KEY_SUFFIX=keep"}
	got := Without(original, "OPENCODE_API_KEY")
	want := []string{"PATH=/bin", "OPENCODE_API_KEY_SUFFIX=keep"}
	if !slices.Equal(got, want) || len(original) != 3 {
		t.Fatalf("Without() = %#v, original=%#v", got, original)
	}
}
