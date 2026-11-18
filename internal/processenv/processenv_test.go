package processenv

import (
	"github.com/guzus/birdy/internal/envcheck"
	"slices"
	"testing"
)

func TestWithoutRemovesOnlyExactVariableNames(t *testing.T) {
	original := []string{"PATH=/bin", "OPENCODE_API_KEY=secret", "OPENCODE_API_KEY_SUFFIX=keep"}
	got := Without(original, "OPENCODE_API_KEY")
	want := []string{"PATH=/bin", "OPENCODE_API_KEY_SUFFIX=keep"}
	if !slices.Equal(got, want) || len(original) != 3 {
		t.Fatalf("Without() kept %v from %v", envcheck.Names(got), envcheck.Names(original))
	}
}
