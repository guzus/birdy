package scrape

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/guzus/birdy/internal/xapi"
)

// Row is one scrape result. Tweet rows omit resultType; user and
// diagnostic rows set it.
type Row struct {
	ResultType     string   `json:"resultType,omitempty"`
	ID             string   `json:"id,omitempty"`
	Text           string   `json:"text,omitempty"`
	CreatedAt      string   `json:"createdAt,omitempty"`
	LikeCount      int      `json:"likeCount"`
	RetweetCount   int      `json:"retweetCount"`
	ReplyCount     int      `json:"replyCount"`
	QuoteCount     int      `json:"quoteCount"`
	ViewCount      int      `json:"viewCount"`
	BookmarkCount  int      `json:"bookmarkCount"`
	Lang           string   `json:"lang,omitempty"`
	URL            string   `json:"url,omitempty"`
	TweetURL       string   `json:"tweetUrl,omitempty"`
	TwitterURL     string   `json:"twitterUrl,omitempty"`
	Author         *Author  `json:"author,omitempty"`
	AuthorUsername string   `json:"authorUsername,omitempty"`
	AuthorName     string   `json:"authorName,omitempty"`
	Media          []Media  `json:"media,omitempty"`
	MediaURLs      []string `json:"mediaUrls,omitempty"`
	ImageURLs      []string `json:"imageUrls,omitempty"`
	VideoURLs      []string `json:"videoUrls,omitempty"`
	IsReply        bool     `json:"isReply,omitempty"`
	IsQuoteStatus  bool     `json:"isQuoteStatus,omitempty"`
	ConversationID string   `json:"conversationId,omitempty"`
	InReplyToID    string   `json:"inReplyToId,omitempty"`
	QuotedTweet    *Row     `json:"quotedTweet,omitempty"`
	SearchTerm     string   `json:"searchTerm,omitempty"`
	Source         string   `json:"source,omitempty"`
	SourceTweetID  string   `json:"sourceTweetId,omitempty"`
	EngagementMode string   `json:"engagementMode,omitempty"`
	Username       string   `json:"username,omitempty"`
	Name           string   `json:"name,omitempty"`
	Description    string   `json:"description,omitempty"`
	Followers      int      `json:"followers,omitempty"`
	Status         string   `json:"status,omitempty"`
	Message        string   `json:"message,omitempty"`
}

// Author is the public identity on a tweet row.
type Author struct {
	ID       string `json:"id,omitempty"`
	Username string `json:"username,omitempty"`
	Name     string `json:"name,omitempty"`
}

// Media is one attachment on a tweet row.
type Media struct {
	Type     string `json:"type"`
	URL      string `json:"url,omitempty"`
	VideoURL string `json:"videoUrl,omitempty"`
}

const (
	resultUser       = "user"
	resultDiagnostic = "diagnostic"
)

// TweetRow copies a parsed tweet into the scrape contract.
func TweetRow(t xapi.Tweet, source, searchTerm string, flat bool) Row {
	metrics := t.Metrics()
	url := tweetURL(t)
	row := Row{
		ID:             t.ID,
		Text:           t.Text,
		CreatedAt:      t.CreatedAt,
		LikeCount:      t.LikeCount,
		RetweetCount:   t.RetweetCount,
		ReplyCount:     t.ReplyCount,
		QuoteCount:     metrics.QuoteCount,
		ViewCount:      metrics.ViewCount,
		BookmarkCount:  metrics.BookmarkCount,
		Lang:           metrics.Lang,
		URL:            url,
		Author:         &Author{ID: t.AuthorID, Username: t.Author.Username, Name: t.Author.Name},
		IsReply:        t.IsReply(),
		IsQuoteStatus:  t.QuotedTweet != nil,
		ConversationID: t.ConversationID,
		InReplyToID:    t.InReplyToStatusID,
		SearchTerm:     searchTerm,
		Source:         source,
	}
	if t.QuotedTweet != nil {
		quoted := TweetRow(*t.QuotedTweet, source, "", false)
		row.QuotedTweet = &quoted
	}
	for _, m := range t.Media {
		row.Media = append(row.Media, Media{Type: m.Type, URL: m.URL, VideoURL: m.VideoURL})
		if m.VideoURL != "" {
			row.VideoURLs = append(row.VideoURLs, m.VideoURL)
		}
		if m.URL != "" {
			row.MediaURLs = append(row.MediaURLs, m.URL)
			if m.Type == "photo" || m.VideoURL == "" {
				row.ImageURLs = append(row.ImageURLs, m.URL)
			}
		}
	}
	if flat {
		row.TweetURL = url
		row.TwitterURL = twitterURL(t)
		if row.Author != nil {
			row.AuthorUsername = row.Author.Username
			row.AuthorName = row.Author.Name
		}
	}
	return row
}

// UserRow copies an engagement-mode account into the scrape contract.
func UserRow(user xapi.ListedUser, sourceTweetID, mode string) Row {
	row := Row{
		ResultType:     resultUser,
		ID:             user.ID,
		Username:       user.Username,
		Name:           user.Name,
		SourceTweetID:  sourceTweetID,
		EngagementMode: mode,
		URL:            userURL(user.Username),
	}
	if user.Description != nil {
		row.Description = *user.Description
	}
	if user.FollowersCount != nil {
		row.Followers = *user.FollowersCount
	}
	return row
}

// DiagnosticRow explains a no-data or invalid scrape.
func DiagnosticRow(status, message string) Row {
	return Row{ResultType: resultDiagnostic, Status: status, Message: message}
}

func tweetURL(t xapi.Tweet) string {
	if t.Author.Username != "" && t.ID != "" {
		return "https://x.com/" + t.Author.Username + "/status/" + t.ID
	}
	if t.ID != "" {
		return "https://x.com/i/status/" + t.ID
	}
	return ""
}

func twitterURL(t xapi.Tweet) string {
	if t.Author.Username != "" && t.ID != "" {
		return "https://twitter.com/" + t.Author.Username + "/status/" + t.ID
	}
	return tweetURL(t)
}

func userURL(username string) string {
	if username == "" {
		return ""
	}
	return "https://x.com/" + username
}

// WriteJSON emits the dataset as a JSON array.
func WriteJSON(w io.Writer, rows []Row) error {
	if rows == nil {
		rows = []Row{}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

var csvHeader = []string{
	"id", "text", "createdAt", "likeCount", "retweetCount", "replyCount",
	"quoteCount", "viewCount", "bookmarkCount", "lang", "url",
	"authorUsername", "authorName", "authorId", "isReply", "conversationId",
	"inReplyToId", "searchTerm", "source", "mediaUrls",
}

// WriteCSV emits spreadsheet-friendly columns.
func WriteCSV(w io.Writer, rows []Row) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	for _, row := range rows {
		if row.ResultType == resultDiagnostic {
			continue
		}
		authorUser, authorName, authorID := "", "", ""
		if row.Author != nil {
			authorUser, authorName, authorID = row.Author.Username, row.Author.Name, row.Author.ID
		}
		if authorUser == "" {
			authorUser = firstNonEmpty(row.AuthorUsername, row.Username)
		}
		if authorName == "" {
			authorName = firstNonEmpty(row.AuthorName, row.Name)
		}
		record := []string{
			row.ID,
			row.Text,
			row.CreatedAt,
			strconv.Itoa(row.LikeCount),
			strconv.Itoa(row.RetweetCount),
			strconv.Itoa(row.ReplyCount),
			strconv.Itoa(row.QuoteCount),
			strconv.Itoa(row.ViewCount),
			strconv.Itoa(row.BookmarkCount),
			row.Lang,
			firstNonEmpty(row.URL, row.TweetURL),
			authorUser,
			authorName,
			authorID,
			strconv.FormatBool(row.IsReply),
			row.ConversationID,
			row.InReplyToID,
			row.SearchTerm,
			row.Source,
			strings.Join(row.MediaURLs, " "),
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
