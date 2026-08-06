package tweet

import (
	"strings"
	"testing"
)

// realReadStdout mirrors an actual `bird read <id> --json` payload.
const realReadStdout = `{
  "id": "2084912076502282341",
  "text": "Falcon 9's first stage has landed https://t.co/VUSaoAg08v",
  "createdAt": "Wed Aug 05 07:59:09 +0000 2026",
  "replyCount": 429,
  "likeCount": 8658,
  "conversationId": "2084912076502282341",
  "author": { "username": "SpaceX", "name": "SpaceX" },
  "authorId": "34743251",
  "media": [
    {
      "type": "video",
      "url": "https://pbs.twimg.com/amplify_video_thumb/123/img/x.jpg",
      "previewUrl": "https://pbs.twimg.com/amplify_video_thumb/123/img/x.jpg:small",
      "videoUrl": "https://video.twimg.com/amplify_video/123/vid/avc1/3840x2160/y.mp4?tag=29",
      "width": 2048,
      "height": 1152,
      "durationMs": 18349
    }
  ]
}`

func TestDecodeTweet(t *testing.T) {
	tw, err := decodeTweet(realReadStdout)
	if err != nil {
		t.Fatalf("decodeTweet returned error: %v", err)
	}

	if tw.ID != "2084912076502282341" {
		t.Errorf("ID = %q, want the tweet id", tw.ID)
	}
	if tw.Author.Username != "SpaceX" {
		t.Errorf("Author.Username = %q, want SpaceX", tw.Author.Username)
	}
	if tw.LikeCount != 8658 {
		t.Errorf("LikeCount = %d, want 8658", tw.LikeCount)
	}
	if len(tw.Media) != 1 {
		t.Fatalf("len(Media) = %d, want 1", len(tw.Media))
	}
	if !tw.Media[0].IsVideo() {
		t.Error("Media[0].IsVideo() = false, want true")
	}
	if got := tw.Media[0].DownloadURL(); !strings.HasPrefix(got, "https://video.twimg.com/") {
		t.Errorf("DownloadURL() = %q, want the mp4 rather than the thumbnail", got)
	}
}

// bird prefixes JSON with progress lines; they must not break decoding.
func TestDecodeTweetIgnoresProgressPreamble(t *testing.T) {
	stdout := "ℹ️ Looking up @SpaceX...\nℹ️ Fetching tweet...\n" + realReadStdout + "\n"
	tw, err := decodeTweet(stdout)
	if err != nil {
		t.Fatalf("decodeTweet returned error: %v", err)
	}
	if tw.ID != "2084912076502282341" {
		t.Errorf("ID = %q, want the tweet id", tw.ID)
	}
}

func TestDecodeTweetRejectsGarbage(t *testing.T) {
	for _, stdout := range []string{"", "no json here", "ℹ️ Looking up @nobody..."} {
		if _, err := decodeTweet(stdout); err == nil {
			t.Errorf("decodeTweet(%q) = nil error, want error", stdout)
		}
	}
	if _, err := decodeTweet(`{"error": "not found"}`); err == nil {
		t.Error("decodeTweet(non-tweet json) = nil error, want error")
	}
}

func TestDecodeTweets(t *testing.T) {
	stdout := `ℹ️ Fetching thread...
[
  {"id": "100", "text": "root", "conversationId": "100", "author": {"username": "a"}},
  {"id": "101", "text": "reply", "conversationId": "100", "inReplyToStatusId": "100", "author": {"username": "b"}}
]`
	tweets, err := decodeTweets(stdout)
	if err != nil {
		t.Fatalf("decodeTweets returned error: %v", err)
	}
	if len(tweets) != 2 {
		t.Fatalf("len(tweets) = %d, want 2", len(tweets))
	}
	if tweets[1].InReplyToStatusID != "100" {
		t.Errorf("InReplyToStatusID = %q, want 100", tweets[1].InReplyToStatusID)
	}
}

func TestTweetIsReply(t *testing.T) {
	root := Tweet{ID: "100", ConversationID: "100"}
	if root.IsReply() {
		t.Error("IsReply() = true for a conversation root")
	}

	reply := Tweet{ID: "101", ConversationID: "100", InReplyToStatusID: "100"}
	if !reply.IsReply() {
		t.Error("IsReply() = false for a reply")
	}

	// bird omits inReplyToStatusId on some payloads; conversationId still tells us.
	inferred := Tweet{ID: "102", ConversationID: "100"}
	if !inferred.IsReply() {
		t.Error("IsReply() = false when only conversationId marks it as a reply")
	}
}

func TestTweetURL(t *testing.T) {
	withAuthor := Tweet{ID: "123", Author: Author{Username: "SpaceX"}}
	if got, want := withAuthor.URL(), "https://x.com/SpaceX/status/123"; got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}

	// A missing handle must still yield a working permalink.
	withoutAuthor := Tweet{ID: "123"}
	if got, want := withoutAuthor.URL(), "https://x.com/i/status/123"; got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}

// thread is returned flat, so ancestry must be reconstructed via InReplyToStatusID.
func TestAncestorChain(t *testing.T) {
	thread := []Tweet{
		{ID: "100", ConversationID: "100"},
		{ID: "101", ConversationID: "100", InReplyToStatusID: "100"},
		{ID: "102", ConversationID: "100", InReplyToStatusID: "101"},
		{ID: "999", ConversationID: "100", InReplyToStatusID: "100"}, // unrelated sibling
	}

	t.Run("returns ancestors root first", func(t *testing.T) {
		chain := AncestorChain(thread, "102")
		if len(chain) != 2 {
			t.Fatalf("len(chain) = %d, want 2", len(chain))
		}
		if chain[0].ID != "100" || chain[1].ID != "101" {
			t.Errorf("chain = [%s %s], want [100 101] (root first)", chain[0].ID, chain[1].ID)
		}
	})

	t.Run("root has no ancestors", func(t *testing.T) {
		if chain := AncestorChain(thread, "100"); len(chain) != 0 {
			t.Errorf("chain = %+v, want empty for the root", chain)
		}
	})

	t.Run("unknown target yields nothing", func(t *testing.T) {
		if chain := AncestorChain(thread, "does-not-exist"); len(chain) != 0 {
			t.Errorf("chain = %+v, want empty", chain)
		}
	})

	t.Run("stops when a parent is missing from the thread", func(t *testing.T) {
		partial := []Tweet{{ID: "202", InReplyToStatusID: "201"}}
		if chain := AncestorChain(partial, "202"); len(chain) != 0 {
			t.Errorf("chain = %+v, want empty when the parent was not returned", chain)
		}
	})

	t.Run("does not loop on cyclic data", func(t *testing.T) {
		cyclic := []Tweet{
			{ID: "300", InReplyToStatusID: "301"},
			{ID: "301", InReplyToStatusID: "300"},
		}
		if chain := AncestorChain(cyclic, "300"); len(chain) > 2 {
			t.Errorf("chain = %+v, want the cycle to be cut short", chain)
		}
	})
}

const testAccounts = `[{"name":"main","auth_token":"tok1","ct0":"ct1"},{"name":"alt","auth_token":"tok2","ct0":"ct2"}]`

func TestNewClientFromAccountsJSON(t *testing.T) {
	c, err := NewClient(Options{AccountsJSON: testAccounts})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	got := c.Accounts()
	if len(got) != 2 {
		t.Fatalf("Accounts() = %v, want 2 accounts", got)
	}
	// Default strategy must be quota-aware: a server should route around 429s.
	if c.strategy != "quota-aware" {
		t.Errorf("strategy = %q, want quota-aware by default", c.strategy)
	}
}

func TestNewClientValidation(t *testing.T) {
	t.Run("rejects empty account set", func(t *testing.T) {
		if _, err := NewClient(Options{AccountsJSON: `[]`}); err == nil {
			t.Error("NewClient([]) = nil error, want error")
		}
	})

	t.Run("rejects malformed json", func(t *testing.T) {
		if _, err := NewClient(Options{AccountsJSON: `{not json`}); err == nil {
			t.Error("NewClient(bad json) = nil error, want error")
		}
	})

	t.Run("rejects unknown strategy", func(t *testing.T) {
		if _, err := NewClient(Options{AccountsJSON: testAccounts, Strategy: "nonsense"}); err == nil {
			t.Error("NewClient(bad strategy) = nil error, want error")
		}
	})

	t.Run("rejects pinning an unknown account", func(t *testing.T) {
		if _, err := NewClient(Options{AccountsJSON: testAccounts, Account: "missing"}); err == nil {
			t.Error("NewClient(unknown account) = nil error, want error")
		}
	})

	t.Run("accepts pinning a known account", func(t *testing.T) {
		c, err := NewClient(Options{AccountsJSON: testAccounts, Account: "alt"})
		if err != nil {
			t.Fatalf("NewClient returned error: %v", err)
		}
		account, err := c.pick()
		if err != nil {
			t.Fatalf("pick returned error: %v", err)
		}
		// A pinned client must never rotate away from its account.
		if account.Name != "alt" {
			t.Errorf("pick() = %q, want the pinned account alt", account.Name)
		}
	})
}

func TestClientRejectsEmptyReference(t *testing.T) {
	c, err := NewClient(Options{AccountsJSON: testAccounts})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	// Must fail before spawning bird, so these are safe without the CLI present.
	if _, err := c.Read(t.Context(), "  "); err == nil {
		t.Error("Read(empty) = nil error, want error")
	}
	if _, err := c.Thread(t.Context(), ""); err == nil {
		t.Error("Thread(empty) = nil error, want error")
	}
}

// An ephemeral store must never write to disk, so an embedding service can run
// on a read-only filesystem.
func TestEphemeralStoreDoesNotPersist(t *testing.T) {
	c, err := NewClient(Options{AccountsJSON: testAccounts})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	if !c.store.IsEphemeral() {
		t.Error("store.IsEphemeral() = false, want true for AccountsJSON-backed clients")
	}
	if err := c.store.Save(); err != nil {
		t.Errorf("Save() = %v, want nil no-op", err)
	}
}

func TestRotationAdvancesAcrossCalls(t *testing.T) {
	c, err := NewClient(Options{AccountsJSON: testAccounts, Strategy: "round-robin"})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	first, err := c.pick()
	if err != nil {
		t.Fatalf("pick returned error: %v", err)
	}
	c.setLastUsed(first.Name)

	second, err := c.pick()
	if err != nil {
		t.Fatalf("pick returned error: %v", err)
	}
	if second.Name == first.Name {
		t.Errorf("round-robin picked %q twice; want it to advance", first.Name)
	}
}
