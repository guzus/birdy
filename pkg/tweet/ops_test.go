package tweet

import "testing"

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
