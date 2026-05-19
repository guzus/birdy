package cmd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/guzus/birdy/internal/claude"
	"github.com/guzus/birdy/internal/rotation"
	"github.com/guzus/birdy/internal/runner"
	"github.com/guzus/birdy/internal/state"
	"github.com/guzus/birdy/internal/store"
	"github.com/guzus/birdy/internal/telegram"
	"github.com/spf13/cobra"
)

type parsedBookmark struct {
	Handle      string
	DisplayName string
	Body        string
	MediaURLs   []string
	Date        string
	URL         string
}

var (
	bmHeaderRe = regexp.MustCompile(`^@(\S+)\s+\((.+?)\):$`)
	bmSepRe    = regexp.MustCompile(`^─{10,}$`)
)

func parseBookmarkOutput(output string) []parsedBookmark {
	lines := strings.Split(output, "\n")
	var result []parsedBookmark
	var cur *parsedBookmark
	var body []string

	flush := func() {
		if cur != nil {
			cur.Body = strings.TrimSpace(strings.Join(body, "\n"))
			result = append(result, *cur)
			cur = nil
			body = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if bmSepRe.MatchString(trimmed) {
			flush()
			continue
		}

		if m := bmHeaderRe.FindStringSubmatch(trimmed); m != nil {
			flush()
			cur = &parsedBookmark{Handle: m[1], DisplayName: m[2]}
			body = nil
			continue
		}

		if cur == nil {
			continue
		}

		if v, ok := strings.CutPrefix(trimmed, "🖼️ "); ok {
			cur.MediaURLs = append(cur.MediaURLs, strings.TrimSpace(v))
			continue
		}
		if v, ok := strings.CutPrefix(trimmed, "📅 "); ok {
			cur.Date = v
			continue
		}
		if v, ok := strings.CutPrefix(trimmed, "🔗 "); ok {
			cur.URL = strings.TrimSpace(v)
			continue
		}

		body = append(body, line)
	}
	flush()
	return result
}

func formatBookmarkTelegram(b parsedBookmark) string {
	return fmt.Sprintf("<b>%s</b> · %s\n\n%s",
		telegram.EscapeHTML("@"+b.Handle),
		telegram.EscapeHTML(b.DisplayName),
		telegram.EscapeHTML(b.Body))
}

var (
	pbChatID   string
	pbBotToken string
	pbCount    string
	pbDryRun   bool
	pbDigest   bool
	pbModel    string
	pbTimeout  int
)

var processBookmarksCmd = &cobra.Command{
	Use:     "process-bookmarks",
	Short:   "Send bookmarks to Telegram with inline X link buttons",
	GroupID: "birdy",
	RunE:    runProcessBookmarks,
}

func init() {
	processBookmarksCmd.Flags().StringVar(&pbChatID, "chat-id", "", "Telegram chat ID (env: BIRDY_TG_CHAT_ID)")
	processBookmarksCmd.Flags().StringVar(&pbBotToken, "bot-token", "", "Telegram bot token (env: BIRDY_TG_BOT_TOKEN)")
	processBookmarksCmd.Flags().StringVar(&pbCount, "count", "10", "Number of bookmarks to fetch")
	processBookmarksCmd.Flags().BoolVar(&pbDryRun, "dry-run", false, "Print formatted output without sending")
	processBookmarksCmd.Flags().BoolVar(&pbDigest, "digest", false, "Have Claude agentically digest each bookmark (uses birdy as a tool)")
	processBookmarksCmd.Flags().StringVar(&pbModel, "model", "opus", "Claude model for --digest")
	processBookmarksCmd.Flags().IntVar(&pbTimeout, "digest-timeout", 120, "Per-bookmark digest timeout in seconds")
	rootCmd.AddCommand(processBookmarksCmd)
}

func birdyExecPath() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "birdy"
}

func digestSystemPrompt(birdyCmd string) string {
	return fmt.Sprintf(`You are summarizing X/Twitter bookmarks for a Telegram digest.

You have one tool: the birdy CLI. Useful commands:
  %[1]s thread <tweet-id>     Read the full thread for context
  %[1]s replies <tweet-id>    See top replies
  %[1]s about <username>      Look up the author
  %[1]s read <tweet-id-or-url>  Read a single tweet (e.g. for a quoted tweet)

Use these only when the bookmark text alone is insufficient (terse posts,
thread starters, ambiguous references, quoted tweets). Do not over-fetch.
At most 1-3 tool calls per bookmark.

Output rules — STRICT:
- Output ONLY the Telegram message body. No preamble, no closing, no headers.
- Use Telegram HTML: <b>bold</b> only. No other tags. No markdown.
- Lead with one short bold takeaway line (the insight, not "the author says X").
- Follow with 1-3 sentences of context, stakes, or implication.
- Keep total length under 500 characters. No emoji walls. No hashtags.
- Do not repeat the author handle (it appears in the message header above your text).
- Do not include the URL (an inline button handles it).`, birdyCmd)
}

func digestPrompt(b parsedBookmark) string {
	tweetID := extractTweetID(b.URL)
	var sb strings.Builder
	sb.WriteString("Digest this bookmarked tweet for a Telegram channel.\n\n")
	fmt.Fprintf(&sb, "Author: @%s (%s)\n", b.Handle, b.DisplayName)
	if tweetID != "" {
		fmt.Fprintf(&sb, "Tweet ID: %s\n", tweetID)
	}
	if b.URL != "" {
		fmt.Fprintf(&sb, "URL: %s\n", b.URL)
	}
	sb.WriteString("\nTweet text:\n")
	sb.WriteString(b.Body)
	sb.WriteString("\n\nProduce the digest now per the system prompt rules.")
	return sb.String()
}

var tweetIDRe = regexp.MustCompile(`/status/(\d+)`)

func extractTweetID(url string) string {
	if m := tweetIDRe.FindStringSubmatch(url); m != nil {
		return m[1]
	}
	return ""
}

func formatDigestTelegram(b parsedBookmark, digest string) string {
	digest = strings.TrimSpace(digest)
	header := fmt.Sprintf("<b>%s</b> · %s",
		telegram.EscapeHTML("@"+b.Handle),
		telegram.EscapeHTML(b.DisplayName))
	if digest == "" {
		return header
	}
	return header + "\n\n" + digest
}

func runProcessBookmarks(cmd *cobra.Command, args []string) error {
	chatID := pbChatID
	if chatID == "" {
		chatID = os.Getenv("BIRDY_TG_CHAT_ID")
	}
	botToken := pbBotToken
	if botToken == "" {
		botToken = os.Getenv("BIRDY_TG_BOT_TOKEN")
	}
	if !pbDryRun && (chatID == "" || botToken == "") {
		return fmt.Errorf("--chat-id / BIRDY_TG_CHAT_ID and --bot-token / BIRDY_TG_BOT_TOKEN required (or use --dry-run)")
	}

	st, err := store.Open()
	if err != nil {
		return fmt.Errorf("opening account store: %w", err)
	}
	if st.Len() == 0 {
		return fmt.Errorf("no accounts configured\nRun: birdy account add <name>")
	}

	var account *store.Account
	if accountFlag != "" {
		account, err = st.Get(accountFlag)
		if err != nil {
			return err
		}
	} else {
		strat, err := rotation.ParseStrategy(strategyFlag)
		if err != nil {
			return err
		}
		rs, err := state.Load()
		if err != nil {
			return fmt.Errorf("loading rotation state: %w", err)
		}
		account, err = rotation.Pick(st.List(), strat, rs.LastUsedName)
		if err != nil {
			return err
		}
	}

	res, stdout, stderr, err := runner.RunCapture(account, []string{"bookmarks", "--count", pbCount})
	if err != nil {
		return fmt.Errorf("running bird bookmarks: %w", err)
	}
	if res.RateLimited {
		if rlErr := st.RecordRateLimit(account.Name); rlErr == nil {
			_ = st.Save()
		}
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("bird bookmarks failed (exit %d): %s", res.ExitCode, stderr)
	}

	bookmarks := parseBookmarkOutput(stdout)
	if len(bookmarks) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No bookmarks found.")
		return nil
	}

	errOut := cmd.ErrOrStderr()
	birdyPath := birdyExecPath()
	sysPrompt := digestSystemPrompt(birdyPath)

	for i, b := range bookmarks {
		var text string
		if pbDigest {
			fmt.Fprintf(errOut, "digesting %d/%d @%s …\n", i+1, len(bookmarks), b.Handle)
			ctx, cancel := context.WithTimeout(cmd.Context(), time.Duration(pbTimeout)*time.Second)
			digest, err := claude.RunOnce(ctx, digestPrompt(b), sysPrompt, pbModel, birdyPath)
			cancel()
			if err != nil {
				fmt.Fprintf(errOut, "digest failed for @%s: %v — falling back to raw tweet\n", b.Handle, err)
				text = formatBookmarkTelegram(b)
			} else {
				text = formatDigestTelegram(b, digest)
			}
		} else {
			text = formatBookmarkTelegram(b)
		}

		if pbDryRun {
			fmt.Fprintf(cmd.OutOrStdout(), "── Bookmark %d ──\n%s\n[View on X → %s]\n\n", i+1, text, b.URL)
			continue
		}

		var markup *telegram.InlineKeyboardMarkup
		if b.URL != "" {
			markup = &telegram.InlineKeyboardMarkup{
				InlineKeyboard: [][]telegram.InlineKeyboardButton{{
					{Text: "View on X", URL: b.URL},
				}},
			}
		}

		if err := telegram.SendMessage(botToken, telegram.SendMessageRequest{
			ChatID:      chatID,
			Text:        text,
			ParseMode:   "HTML",
			ReplyMarkup: markup,
		}); err != nil {
			return fmt.Errorf("sending bookmark %d: %w", i+1, err)
		}
		fmt.Fprintf(errOut, "sent %d/%d @%s\n", i+1, len(bookmarks), b.Handle)
	}

	if !pbDryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "Sent %d bookmarks to Telegram.\n", len(bookmarks))
	}
	return nil
}
