package scrape

import "testing"

func TestClassifyTargets(t *testing.T) {
	cases := []struct {
		in   string
		kind Kind
		val  string
		sort string
	}{
		{"1846987139428634858", KindTweet, "1846987139428634858", ""},
		{"https://x.com/elonmusk/status/1846987139428634858", KindTweet, "1846987139428634858", ""},
		{"https://x.com/nasa", KindProfile, "nasa", ""},
		{"@OpenAI", KindProfile, "OpenAI", ""},
		{"AI", KindSearch, "AI", ""},
		{"https://x.com/nasa/with_replies", KindProfileReplies, "nasa", ""},
		{"https://x.com/nasa/media", KindProfileMedia, "nasa", ""},
		{"https://x.com/nasa/likes", KindProfileLikes, "nasa", ""},
		{"https://x.com/i/lists/1748648376080666720", KindList, "1748648376080666720", ""},
		{"list:123456", KindList, "123456", ""},
		{"https://x.com/search?q=AI%20lang%3Aen&f=live", KindSearch, "AI lang:en", "latest"},
		{"from:nasa moon", KindSearch, "from:nasa moon", ""},
		{"AI agents", KindSearch, "AI agents", ""},
	}
	for _, tc := range cases {
		got, err := Classify(tc.in)
		if err != nil {
			t.Fatalf("Classify(%q): %v", tc.in, err)
		}
		if got.Kind != tc.kind || got.Value != tc.val || got.Sort != tc.sort {
			t.Errorf("Classify(%q) = %+v, want kind=%s value=%q sort=%q", tc.in, got, tc.kind, tc.val, tc.sort)
		}
	}
}

func TestClassifyRejectsEmptyAndForeignURLs(t *testing.T) {
	if _, err := Classify("  "); err == nil {
		t.Fatal("empty target should fail")
	}
	if _, err := Classify("https://example.com/nasa"); err == nil {
		t.Fatal("foreign URL should fail")
	}
}
