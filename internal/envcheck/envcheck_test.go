package envcheck

import (
	"slices"
	"testing"
)

func TestMatchesWholeNamesOnly(t *testing.T) {
	env := Lines("GH_AUTH_TOKEN=a\nPATH=/bin\nAUTH_TOKEN_FILE=x\n")
	if Has(env, "AUTH_TOKEN") || !Has(env, "PATH") {
		t.Fatal("matched a name by substring")
	}
	if v, ok := Value(env, "GH_AUTH_TOKEN"); !ok || v != "a" {
		t.Fatal("value lookup")
	}
	if got := Names(env); !slices.Equal(got, []string{"AUTH_TOKEN_FILE", "GH_AUTH_TOKEN", "PATH"}) {
		t.Fatal(got)
	}
	if got := Present(env, "PATH", "CT0"); !slices.Equal(got, []string{"PATH"}) {
		t.Fatal(got)
	}
}
