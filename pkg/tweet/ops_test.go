package tweet

import (
	"testing"

	"github.com/guzus/birdy/internal/xapi"
)

func TestDefaultCount(t *testing.T) {
	if got := defaultCount(0, 20); got != 20 {
		t.Fatalf("got %d", got)
	}
	if got := defaultCount(-1, 20); got != 20 {
		t.Fatalf("got %d", got)
	}
	if got := defaultCount(7, 20); got != 7 {
		t.Fatalf("got %d", got)
	}
}

func TestAllDigits(t *testing.T) {
	if !allDigits("44196397") {
		t.Fatal("numeric id")
	}
	if allDigits("elonmusk") || allDigits("") {
		t.Fatal("handle")
	}
}

func TestSearchEmptyQueryErrors(t *testing.T) {
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"t","auth_token":"tok","ct0":"ct"}]`)
	c, err := NewClient(Options{Account: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(t.Context(), "  ", 5); err == nil {
		t.Fatal("expected empty query error")
	}
}

func TestMentionsRejectsABadHandle(t *testing.T) {
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"t","auth_token":"tok","ct0":"ct"}]`)
	c, err := NewClient(Options{Account: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Mentions(t.Context(), "not a handle!", 5); err == nil {
		t.Fatal("expected invalid handle error")
	}
}

func TestConvertListsPreservesNilVsEmpty(t *testing.T) {
	if convertLists(nil) != nil {
		t.Fatal("nil in must stay nil")
	}
	got := convertLists([]xapi.List{})
	if got == nil || len(got) != 0 {
		t.Fatalf("empty in = %#v", got)
	}
}
