package xapi

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// NewsItem is one entry from X's Explore tabs.
//
// PostCount is a pointer because "no count reported" and "zero posts" print
// differently: bird omits the meta line entirely for the former.
type NewsItem struct {
	ID          string `json:"id"`
	Headline    string `json:"headline"`
	Category    string `json:"category"`
	TimeAgo     string `json:"timeAgo,omitempty"`
	PostCount   *int64 `json:"postCount,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

// NewsTabs are the Explore tabs, keyed by the name bird's flags use. The values
// are X's opaque base64 timeline ids.
var NewsTabs = map[string]string{
	"forYou":        "VGltZWxpbmU6DAC2CwABAAAAB2Zvcl95b3UAAA==",
	"trending":      "VGltZWxpbmU6DAC2CwABAAAACHRyZW5kaW5nAAA=",
	"news":          "VGltZWxpbmU6DAC2CwABAAAABG5ld3MAAA==",
	"sports":        "VGltZWxpbmU6DAC2CwABAAAABnNwb3J0cwAA",
	"entertainment": "VGltZWxpbmU6DAC2CwABAAAADWVudGVydGFpbm1lbnQAAA==",
}

// DefaultNewsTabs is bird's tab order when none is selected. Trending is
// excluded: it carries raw hashtags rather than headlines.
var DefaultNewsTabs = []string{"forYou", "news", "sports", "entertainment"}

var postCountPattern = regexp.MustCompile(`(?i)([\d.]+)([KMB]?)\s*posts?`)

// News returns Explore items across the given tabs, deduplicated by headline.
//
// Tabs are fetched in order and the walk stops as soon as count items are
// collected, so an early tab that fills the quota means later tabs cost
// nothing. A tab that errors is skipped rather than failing the command —
// X removes and renames these timelines regularly, and one dead tab should not
// take out the others.
func (c *Client) News(ctx context.Context, count int, aiOnly bool, tabs []string) ([]NewsItem, error) {
	if len(tabs) == 0 {
		tabs = DefaultNewsTabs
	}
	if count <= 0 {
		count = 10
	}

	var items []NewsItem
	seen := map[string]bool{}
	var lastErr error

	for _, tab := range tabs {
		timelineID, ok := NewsTabs[tab]
		if !ok {
			continue
		}

		tabItems, err := c.newsTab(ctx, timelineID, count, aiOnly)
		if err != nil {
			lastErr = err
			continue
		}
		for _, item := range tabItems {
			if seen[item.Headline] {
				continue
			}
			seen[item.Headline] = true
			items = append(items, item)
		}
		if len(items) >= count {
			break
		}
	}

	// Only report an error when every tab failed; a partial result is the
	// normal outcome and is what bird returns too.
	if len(items) == 0 && lastErr != nil {
		return nil, lastErr
	}
	if len(items) > count {
		items = items[:count]
	}
	return items, nil
}

func (c *Client) newsTab(ctx context.Context, timelineID string, count int, aiOnly bool) ([]NewsItem, error) {
	variables := map[string]any{
		"timelineId": timelineID,
		// Over-fetch, because AI filtering and headline dedup both discard.
		"count":                  count * 2,
		"includePromotedContent": false,
	}
	body, err := c.graphQL(ctx, "GenericTimelineById", variables, exploreFeatures, nil)
	if err != nil {
		return nil, err
	}
	return parseNewsTimeline(body, count, aiOnly)
}

type newsEntry struct {
	EntryID string `json:"entryId"`
	Content struct {
		ItemContent *newsItemContent `json:"itemContent"`
		// A module entry holds several items, and X nests them one of two ways.
		Items []struct {
			ItemContent *newsItemContent `json:"itemContent"`
			Item        *struct {
				ItemContent *newsItemContent `json:"itemContent"`
			} `json:"item"`
		} `json:"items"`
	} `json:"content"`
}

type newsItemContent struct {
	Name          string `json:"name"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	IsAITrend     bool   `json:"is_ai_trend"`
	SocialContext *struct {
		Text string `json:"text"`
	} `json:"social_context"`
	TrendURL *struct {
		URL string `json:"url"`
	} `json:"trend_url"`
	TrendMetadata *struct {
		MetaDescription string `json:"meta_description"`
		DomainContext   string `json:"domain_context"`
		URL             *struct {
			URL string `json:"url"`
		} `json:"url"`
	} `json:"trend_metadata"`
}

func parseNewsTimeline(body []byte, maxCount int, aiOnly bool) ([]NewsItem, error) {
	var resp struct {
		Data struct {
			Timeline struct {
				Timeline *struct {
					Instructions []struct {
						Entries []newsEntry `json:"entries"`
						// Some instructions carry a single entry instead.
						Entry *newsEntry `json:"entry"`
					} `json:"instructions"`
				} `json:"timeline"`
			} `json:"timeline"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &APIError{Message: "decoding news response: " + err.Error()}
	}
	if resp.Data.Timeline.Timeline == nil {
		if len(resp.Errors) > 0 {
			messages := make([]string, 0, len(resp.Errors))
			for _, e := range resp.Errors {
				messages = append(messages, e.Message)
			}
			return nil, &APIError{Message: joinMessages(messages)}
		}
		return nil, nil
	}

	var items []NewsItem
	seen := map[string]bool{}

	for _, instruction := range resp.Data.Timeline.Timeline.Instructions {
		entries := instruction.Entries
		if len(entries) == 0 && instruction.Entry != nil {
			entries = []newsEntry{*instruction.Entry}
		}

		for _, entry := range entries {
			if len(items) >= maxCount {
				return items, nil
			}

			if item, ok := mapNewsItem(entry.Content.ItemContent, entry.EntryID, seen, aiOnly); ok {
				items = append(items, item)
			}
			for _, nested := range entry.Content.Items {
				if len(items) >= maxCount {
					return items, nil
				}
				content := nested.ItemContent
				if content == nil && nested.Item != nil {
					content = nested.Item.ItemContent
				}
				if item, ok := mapNewsItem(content, entry.EntryID, seen, aiOnly); ok {
					items = append(items, item)
				}
			}
		}
	}
	return items, nil
}

func mapNewsItem(content *newsItemContent, entryID string, seen map[string]bool, aiOnly bool) (NewsItem, bool) {
	if content == nil {
		return NewsItem{}, false
	}
	headline := content.Name
	if headline == "" {
		headline = content.Title
	}
	if headline == "" || seen[headline] {
		return NewsItem{}, false
	}

	socialText := ""
	if content.SocialContext != nil {
		socialText = content.SocialContext.Text
	}

	// bird's heuristic, reproduced rather than improved: an item counts as AI
	// news when X says so outright, or when the headline reads as a sentence
	// (five words or more) and the social context looks like a news byline.
	// Changing the threshold would silently change what --ai-only returns.
	isFullSentence := len(strings.Fields(headline)) >= 5
	hasNewsCategory := strings.Contains(socialText, "News") || strings.Contains(socialText, "hours ago")
	isAINews := content.IsAITrend || (isFullSentence && hasNewsCategory)

	if aiOnly && !isAINews {
		return NewsItem{}, false
	}
	seen[headline] = true

	trendURL := ""
	if content.TrendURL != nil {
		trendURL = content.TrendURL.URL
	}
	if trendURL == "" && content.TrendMetadata != nil && content.TrendMetadata.URL != nil {
		trendURL = content.TrendMetadata.URL.URL
	}

	item := NewsItem{
		Headline:    headline,
		Category:    "Trending",
		Description: content.Description,
		URL:         trendURL,
	}

	// The social context packs time, post count and category into one
	// separator-joined string, in no fixed order.
	for _, part := range strings.Split(socialText, "·") {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
		case strings.Contains(part, "ago"):
			item.TimeAgo = part
		case postCountPattern.MatchString(part):
			if n, ok := parsePostCount(part); ok {
				item.PostCount = &n
			}
		default:
			item.Category = part
		}
	}

	if content.TrendMetadata != nil {
		if n, ok := parsePostCount(content.TrendMetadata.MetaDescription); ok {
			item.PostCount = &n
		}
		if content.TrendMetadata.DomainContext != "" &&
			(item.Category == "Trending" || item.Category == "News") {
			item.Category = content.TrendMetadata.DomainContext
		}
	}

	if isAINews {
		item.Category = "AI · " + item.Category
	}

	switch {
	case trendURL != "":
		item.ID = trendURL
	case entryID != "":
		item.ID = entryID + "-" + headline
	default:
		item.ID = headline
	}
	return item, true
}

// parsePostCount reads "12.3K posts" into 12300.
func parsePostCount(s string) (int64, bool) {
	m := postCountPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToUpper(m[2]) {
	case "K":
		value *= 1_000
	case "M":
		value *= 1_000_000
	case "B":
		value *= 1_000_000_000
	}
	return int64(math.Round(value)), true
}
