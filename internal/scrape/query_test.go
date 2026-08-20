package scrape

import "testing"

func TestCompileFilters(t *testing.T) {
	got, err := (Filters{
		Content:    "AI",
		From:       "elonmusk",
		Since:      "2026-01-01",
		Until:      "2026-03-01",
		Lang:       "en",
		MinLikes:   1000,
		MinReposts: 50,
		Operators:  []string{"media", "filter:safe"},
	}).Compile()
	if err != nil {
		t.Fatal(err)
	}
	want := "AI from:elonmusk since:2026-01-01 until:2026-03-01 lang:en min_faves:1000 min_retweets:50 filter:media filter:safe"
	if got != want {
		t.Fatalf("Compile() = %q, want %q", got, want)
	}
}

func TestCompileRejectsUnknownFilterAndBadDate(t *testing.T) {
	if _, err := (Filters{Operators: []string{"nope"}}).Compile(); err == nil {
		t.Fatal("unknown filter should fail")
	}
	if _, err := (Filters{Since: "yesterday"}).Compile(); err == nil {
		t.Fatal("invalid since should fail")
	}
}

func TestPlanAutoRoutesMixedInputs(t *testing.T) {
	jobs, err := (Request{
		Positionals: []string{
			"https://x.com/nasa/status/1846987139428634858",
			"https://x.com/nasa",
			"https://x.com/search?q=moon",
		},
		Handles:  []string{"OpenAI"},
		MaxItems: 20,
	}).Plan()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 4 {
		t.Fatalf("got %d jobs, want 4", len(jobs))
	}
	want := []Kind{KindTweet, KindProfile, KindSearch, KindProfile}
	for i, job := range jobs {
		if job.Kind != want[i] {
			t.Errorf("job %d kind = %s, want %s", i, job.Kind, want[i])
		}
	}
	if jobs[2].Query != "moon" {
		t.Errorf("search query = %q, want moon", jobs[2].Query)
	}
}

func TestPlanProfileWithDatesBecomesSearch(t *testing.T) {
	jobs, err := (Request{
		Handles: []string{"nasa"},
		Filters: Filters{Since: "2026-01-01", Until: "2026-01-02"},
	}).Plan()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Kind != KindSearch {
		t.Fatalf("got %+v, want one search job", jobs)
	}
	if jobs[0].Query != "from:nasa since:2026-01-01 until:2026-01-02" {
		t.Fatalf("query = %q", jobs[0].Query)
	}
}

func TestPlanFilterOnlyBecomesSearch(t *testing.T) {
	jobs, err := (Request{Filters: Filters{From: "nasa", Content: "moon"}}).Plan()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Kind != KindSearch || jobs[0].Query != "moon from:nasa" {
		t.Fatalf("got %+v", jobs)
	}
}

func TestPlanModeRepliesRequiresTweet(t *testing.T) {
	_, err := (Request{Handles: []string{"nasa"}, Mode: ModeReplies}).Plan()
	if err == nil {
		t.Fatal("expected mode replies to reject a handle")
	}
}
