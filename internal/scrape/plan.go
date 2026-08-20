package scrape

import (
	"fmt"
	"strings"

	"github.com/guzus/birdy/internal/xapi"
)

const (
	ModeAuto           = "auto"
	ModeTweet          = "tweet"
	ModeSearch         = "search"
	ModeProfile        = "profile"
	ModeProfileReplies = "profile-replies"
	ModeProfileMedia   = "profile-media"
	ModeProfileLikes   = "profile-likes"
	ModeList           = "list"
	ModeReplies        = "replies"
	ModeQuotes         = "quotes"
	ModeThread         = "thread"
	ModeRetweeters     = "retweeters"
	ModeFavoriters     = "favoriters"
)

const (
	SortLatest = "latest"
	SortTop    = "top"
	SortBoth   = "both"
)

const (
	DefaultMaxItems = 100
	MaxItemsCap     = 10000
)

// Request is the complete scrape input after flag/URL parsing.
type Request struct {
	Positionals       []string
	Handles           []string
	Searches          []string
	TweetIDs          []string
	ListIDs           []string
	Mode              string
	Sort              string
	Filters           Filters
	MaxItems          int
	MaxItemsPerTarget int
	Output            string
	IncludeSearchTerm bool
}

// Job is one fetch the runner will execute.
type Job struct {
	Kind   Kind
	Value  string
	Source string
	Query  string
	Sort   string
	Mode   string
	Limit  int
}

// Plan classifies inputs, compiles filters, and applies the global item cap.
func (r Request) Plan() ([]Job, error) {
	mode, err := normalizeMode(r.Mode)
	if err != nil {
		return nil, err
	}
	sort, err := normalizeSort(r.Sort)
	if err != nil {
		return nil, err
	}
	if err := validateOutput(r.Output); err != nil {
		return nil, err
	}

	maxItems := r.MaxItems
	if maxItems <= 0 {
		maxItems = DefaultMaxItems
	}
	if maxItems > MaxItemsCap {
		return nil, fmt.Errorf("max items %d exceeds %d", maxItems, MaxItemsCap)
	}
	perTarget := r.MaxItemsPerTarget
	if perTarget <= 0 || perTarget > maxItems {
		perTarget = maxItems
	}

	compiled, err := r.Filters.Compile()
	if err != nil {
		return nil, err
	}

	var targets []Target
	for _, raw := range r.Positionals {
		target, err := Classify(raw)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	for _, handle := range r.Handles {
		normalized, ok := normalizeHandle(handle)
		if !ok {
			return nil, fmt.Errorf("invalid handle %q", handle)
		}
		targets = append(targets, Target{Kind: KindProfile, Value: normalized, Source: handle})
	}
	for _, query := range r.Searches {
		query = strings.TrimSpace(query)
		if query == "" {
			return nil, fmt.Errorf("empty search term")
		}
		targets = append(targets, Target{Kind: KindSearch, Value: query, Source: query})
	}
	for _, id := range r.TweetIDs {
		target, err := Classify(id)
		if err != nil {
			return nil, err
		}
		if target.Kind != KindTweet {
			return nil, fmt.Errorf("%q is not a tweet id or URL", id)
		}
		targets = append(targets, target)
	}
	for _, id := range r.ListIDs {
		id = strings.TrimSpace(strings.TrimPrefix(id, "list:"))
		if id == "" {
			return nil, fmt.Errorf("empty list id")
		}
		targets = append(targets, Target{Kind: KindList, Value: id, Source: id})
	}

	if compiled != "" && len(targets) == 0 {
		targets = append(targets, Target{Kind: KindSearch, Value: compiled, Source: compiled})
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no scrape targets: pass URLs, handles, search terms, tweet ids, or filters")
	}

	jobs := make([]Job, 0, len(targets))
	for _, target := range targets {
		job, err := jobFor(target, mode, sort, r.Filters, compiled, perTarget)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// KindThreadJob and KindRepliesJob are mode-forced tweet derivatives.
const (
	KindThreadJob  Kind = "thread"
	KindRepliesJob Kind = "replies"
	KindQuotesJob  Kind = "quotes"
	KindActorsJob  Kind = "actors"
)

func jobFor(target Target, mode, sort string, filters Filters, compiled string, limit int) (Job, error) {
	kind := target.Kind
	if mode != ModeAuto {
		forced, err := applyMode(target, mode)
		if err != nil {
			return Job{}, err
		}
		kind = forced
	}

	job := Job{
		Kind:   kind,
		Value:  target.Value,
		Source: target.Source,
		Limit:  limit,
		Sort:   firstNonEmpty(target.Sort, sort),
		Mode:   mode,
	}

	switch kind {
	case KindSearch:
		query := compiled
		switch target.Kind {
		case KindProfile, KindProfileReplies, KindProfileMedia, KindProfileLikes:
			query = strings.TrimSpace("from:" + target.Value + " " + compiled)
		case KindSearch:
			if compiled == "" || target.Value == compiled {
				query = target.Value
			} else {
				query = strings.TrimSpace(target.Value + " " + compiled)
			}
		default:
			if compiled == "" || target.Value == compiled {
				query = target.Value
			} else {
				query = strings.TrimSpace(target.Value + " " + compiled)
			}
		}
		if filters.From != "" && !strings.Contains(query, "from:") {
			query = strings.TrimSpace(query + " from:" + trimAt(filters.From))
		}
		job.Query = strings.TrimSpace(query)
		if job.Query == "" {
			return Job{}, fmt.Errorf("empty search query")
		}
	case KindProfile:
		if filters.HasConstraints() || compiled != "" {
			query := compiled
			if !strings.Contains(query, "from:") {
				query = strings.TrimSpace("from:" + target.Value + " " + query)
			}
			job.Kind = KindSearch
			job.Query = strings.TrimSpace(query)
		}
	case KindProfileReplies:
		job.Kind = KindSearch
		job.Query = strings.TrimSpace("from:" + target.Value + " filter:replies " + compiled)
	case KindProfileMedia:
		job.Kind = KindSearch
		job.Query = strings.TrimSpace("from:" + target.Value + " filter:media " + compiled)
	case KindQuotesJob:
		job.Query = "quoted_tweet_id:" + target.Value
	}
	return job, nil
}

func applyMode(target Target, mode string) (Kind, error) {
	switch mode {
	case ModeTweet:
		if target.Kind != KindTweet {
			return "", fmt.Errorf("mode tweet requires tweet ids or URLs, got %s", target.Source)
		}
		return KindTweet, nil
	case ModeSearch:
		if target.Kind == KindTweet {
			return "", fmt.Errorf("mode search cannot use tweet id %s", target.Source)
		}
		if target.Kind == KindList {
			return KindList, nil
		}
		if target.Kind == KindProfile || target.Kind == KindProfileReplies || target.Kind == KindProfileMedia {
			return KindSearch, nil
		}
		return KindSearch, nil
	case ModeProfile:
		if target.Kind != KindProfile && target.Kind != KindProfileReplies && target.Kind != KindProfileMedia && target.Kind != KindProfileLikes {
			return "", fmt.Errorf("mode profile requires handles or profile URLs, got %s", target.Source)
		}
		return KindProfile, nil
	case ModeProfileReplies:
		return requireProfile(target, KindProfileReplies)
	case ModeProfileMedia:
		return requireProfile(target, KindProfileMedia)
	case ModeProfileLikes:
		return requireProfile(target, KindProfileLikes)
	case ModeList:
		if target.Kind != KindList {
			return "", fmt.Errorf("mode list requires list ids or URLs, got %s", target.Source)
		}
		return KindList, nil
	case ModeReplies:
		if target.Kind != KindTweet {
			return "", fmt.Errorf("mode replies requires tweet ids or URLs, got %s", target.Source)
		}
		return KindRepliesJob, nil
	case ModeQuotes:
		if target.Kind != KindTweet {
			return "", fmt.Errorf("mode quotes requires tweet ids or URLs, got %s", target.Source)
		}
		return KindQuotesJob, nil
	case ModeThread:
		if target.Kind != KindTweet {
			return "", fmt.Errorf("mode thread requires tweet ids or URLs, got %s", target.Source)
		}
		return KindThreadJob, nil
	case ModeRetweeters, ModeFavoriters:
		if target.Kind != KindTweet {
			return "", fmt.Errorf("mode %s requires tweet ids or URLs, got %s", mode, target.Source)
		}
		return KindActorsJob, nil
	default:
		return "", fmt.Errorf("unsupported mode %q", mode)
	}
}

func requireProfile(target Target, kind Kind) (Kind, error) {
	switch target.Kind {
	case KindProfile, KindProfileReplies, KindProfileMedia, KindProfileLikes:
		return kind, nil
	default:
		return "", fmt.Errorf("mode %s requires handles or profile URLs, got %s", kind, target.Source)
	}
}

func normalizeMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", ModeAuto, "tweets":
		return ModeAuto, nil
	case ModeTweet, "lookup":
		return ModeTweet, nil
	case ModeSearch:
		return ModeSearch, nil
	case ModeProfile, "profiletweets", "user-tweets":
		return ModeProfile, nil
	case ModeProfileReplies, "profilereplies", "with_replies":
		return ModeProfileReplies, nil
	case ModeProfileMedia, "profilemedia":
		return ModeProfileMedia, nil
	case ModeProfileLikes, "profilelikes":
		return ModeProfileLikes, nil
	case ModeList, "listtweets":
		return ModeList, nil
	case ModeReplies:
		return ModeReplies, nil
	case ModeQuotes:
		return ModeQuotes, nil
	case ModeThread:
		return ModeThread, nil
	case ModeRetweeters:
		return ModeRetweeters, nil
	case ModeFavoriters, "likes":
		return ModeFavoriters, nil
	default:
		return "", fmt.Errorf("unknown mode %q", raw)
	}
}

func normalizeSort(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", SortLatest, "live":
		return SortLatest, nil
	case SortTop:
		return SortTop, nil
	case SortBoth, "latest+top", "top+latest":
		return SortBoth, nil
	default:
		return "", fmt.Errorf("unknown sort %q (want latest, top, or both)", raw)
	}
}

func validateOutput(raw string) error {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "json", "csv", "flat":
		return nil
	default:
		return fmt.Errorf("unknown output %q (want json, csv, or flat)", raw)
	}
}

func normalizeHandle(raw string) (string, bool) {
	return xapi.ValidHandle(raw)
}
