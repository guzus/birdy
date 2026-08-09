package tweet

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Integration tests hit the live X API using the accounts this machine has
// configured. They are skipped unless BIRDY_TWEET_INTEGRATION=1, since they
// consume real rate-limit budget:
//
//	BIRDY_TWEET_INTEGRATION=1 go test ./pkg/tweet/ -run Integration -v
//
// No Node.js or bird CLI is required — reads go straight to X over HTTP.
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("BIRDY_TWEET_INTEGRATION") != "1" {
		t.Skip("set BIRDY_TWEET_INTEGRATION=1 to run live bird integration tests")
	}
}

// A tweet whose only attachment is a video, used to prove video reads resolve
// to a playable mp4 rather than the still thumbnail.
const liveVideoTweetID = "2084912076502282341"

// A reply to the tweet above, used to prove ancestor reconstruction works.
const liveReplyTweetID = "2084912420611649788"

const liveRepliesHandle = "thsottiaux"

func TestIntegrationReadResolvesVideo(t *testing.T) {
	requireIntegration(t)

	c, err := NewClient(Options{})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	t.Logf("accounts: %v", c.Accounts())

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	tw, err := c.Read(ctx, liveVideoTweetID)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}

	if tw.ID != liveVideoTweetID {
		t.Errorf("ID = %q, want %q", tw.ID, liveVideoTweetID)
	}
	if tw.Author.Username == "" {
		t.Error("Author.Username is empty")
	}
	if len(tw.Media) == 0 {
		t.Fatal("Media is empty, want at least one attachment")
	}
	if !tw.Media[0].IsVideo() {
		t.Fatalf("Media[0] = %+v, want a video", tw.Media[0])
	}
	if got := tw.Media[0].DownloadURL(); !strings.HasPrefix(got, "https://video.twimg.com/") {
		t.Errorf("DownloadURL() = %q, want a video.twimg.com mp4", got)
	}
	if tw.IsReply() {
		t.Error("IsReply() = true, want false for a conversation root")
	}
}

func TestIntegrationThreadAncestors(t *testing.T) {
	requireIntegration(t)

	c, err := NewClient(Options{})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()

	reply, err := c.Read(ctx, liveReplyTweetID)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if !reply.IsReply() {
		t.Fatalf("IsReply() = false for %s, want true", liveReplyTweetID)
	}

	thread, err := c.Thread(ctx, reply.ConversationID)
	if err != nil {
		t.Fatalf("Thread returned error: %v", err)
	}
	if len(thread) == 0 {
		t.Fatal("Thread returned no tweets")
	}

	chain := AncestorChain(thread, reply.ID)
	if len(chain) == 0 {
		t.Fatal("AncestorChain returned nothing, want at least the parent tweet")
	}

	// The root of this conversation carries the video the reply is talking about.
	root := chain[0]
	if root.ID != reply.ConversationID {
		t.Errorf("chain[0].ID = %q, want the conversation root %q", root.ID, reply.ConversationID)
	}
	if len(root.Media) == 0 {
		t.Errorf("root tweet %s has no media; expected the parent to carry the video", root.ID)
	}
}

// A cancelled context must abort the in-flight request promptly.
func TestIntegrationContextCancellation(t *testing.T) {
	requireIntegration(t)

	c, err := NewClient(Options{})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := c.Read(ctx, liveVideoTweetID); err == nil {
		t.Error("Read with an expired context returned nil error, want cancellation error")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Read took %s after cancellation; the request was not aborted", elapsed)
	}
}

func TestIntegrationUserReplies(t *testing.T) {
	requireIntegration(t)
	c, err := NewClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	page, err := c.UserReplies(ctx, liveRepliesHandle, UserTimelineOptions{Limit: 100})
	if err != nil {
		t.Fatalf("UserReplies: %v", err)
	}
	for _, post := range page.Tweets {
		if post.IsReply() {
			t.Logf("live reply proof: %s -> %s", post.ID, post.InReplyToStatusID)
			return
		}
	}
	t.Fatalf("Tweets & Replies returned %d entries but none was a reply", len(page.Tweets))
}

func TestIntegrationMonitoringCanaries(t *testing.T) {
	requireIntegration(t)
	base, err := NewClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	accounts := base.Accounts()
	if len(accounts) < 2 {
		t.Skip("monitoring canary requires two configured accounts")
	}
	targets := []string{"thsottiaux", "guzus"}
	for index, account := range accounts[:2] {
		target := targets[index]
		t.Run(account+"/"+target, func(t *testing.T) {
			client, err := NewMonitoringClient(MonitoringOptions{Account: account})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			profile, err := client.UserProfile(ctx, target)
			if err != nil {
				t.Fatalf("UserProfile: %v", err)
			}
			if _, err := client.UserTimeline(ctx, target, UserTimelineOptions{Limit: 5, MaxPages: 1}); err != nil {
				t.Fatalf("UserTimeline: %v", err)
			}
			if _, err := client.UserReplies(ctx, target, UserTimelineOptions{Limit: 5, MaxPages: 1}); err != nil {
				t.Fatalf("UserReplies: %v", err)
			}
			if _, err := client.Following(ctx, profile.ID, FollowingOptions{PageSize: 20, MaxPages: 1}); err != nil {
				t.Fatalf("Following: %v", err)
			}
		})
	}
}
