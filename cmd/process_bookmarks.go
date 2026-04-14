package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strings"

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
	rootCmd.AddCommand(processBookmarksCmd)
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

	exitCode, stdout, stderr, err := runner.RunCapture(account, []string{"bookmarks", "--count", pbCount})
	if err != nil {
		return fmt.Errorf("running bird bookmarks: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("bird bookmarks failed (exit %d): %s", exitCode, stderr)
	}

	bookmarks := parseBookmarkOutput(stdout)
	if len(bookmarks) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No bookmarks found.")
		return nil
	}

	errOut := cmd.ErrOrStderr()
	for i, b := range bookmarks {
		text := formatBookmarkTelegram(b)

		if pbDryRun {
			fmt.Fprintf(cmd.OutOrStdout(), "── Bookmark %d ──\n%s\n[View on X → %s]\n\n", i+1, b.Body, b.URL)
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
