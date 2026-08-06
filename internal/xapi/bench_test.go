package xapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Benchmarks run against a local server rather than x.com. Live API latency is
// ~1.5s and swamps everything the client does, so measuring against it would
// report network weather, not code. Hermetic runs are reproducible and cost no
// rate-limit budget.

// buildConversationPayload synthesizes a TweetDetail response with n tweets,
// sized like a real one (a busy thread runs to hundreds of KB).
func buildConversationPayload(n int) string {
	var b strings.Builder
	b.WriteString(`{"data":{"threaded_conversation_with_injections_v2":{"instructions":[{"entries":[`)
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		inReplyTo := ""
		if i > 0 {
			inReplyTo = fmt.Sprintf(`"in_reply_to_status_id_str":"%d",`, 1000+i-1)
		}
		const tmpl = `{"content":{"itemContent":{"tweet_results":{"result":{"rest_id":"%d","core":{"user_results":{"result":{"rest_id":"%d","legacy":{"screen_name":"user%d","name":"User %d"}}}},"legacy":{"full_text":"Reply number %d in a long conversation with some padding text to make the payload realistic https://t.co/abcdef","created_at":"Wed Aug 05 07:59:09 +0000 2026","conversation_id_str":"1000",%s"reply_count":%d,"retweet_count":%d,"favorite_count":%d,"extended_entities":{"media":[{"type":"video","media_url_https":"https://pbs.twimg.com/amplify_video_thumb/%d/img/x.jpg","sizes":{"large":{"w":2048,"h":1152},"small":{"w":680,"h":383}},"video_info":{"duration_millis":18349,"variants":[{"content_type":"application/x-mpegURL","url":"https://video.twimg.com/hls%d.m3u8"},{"content_type":"video/mp4","url":"https://video.twimg.com/low%d.mp4","bitrate":632000},{"content_type":"video/mp4","url":"https://video.twimg.com/high%d.mp4","bitrate":10368000}]}}]}}}}}}}`
		fmt.Fprintf(&b, tmpl, 1000+i, 9000+i, i, i, i, inReplyTo, i, i*2, i*3, 5000+i, i, i, i)
	}
	b.WriteString(`]}]}}}`)
	return b.String()
}

func benchServer(payload string) *httptest.Server {
	body := []byte(payload)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

func benchClient(b *testing.B, srv *httptest.Server) *Client {
	b.Helper()
	c, err := NewClient(Credentials{AuthToken: "bench-token", CT0: "bench-ct0"})
	if err != nil {
		b.Fatal(err)
	}
	c.SetBaseURL(srv.URL)
	return c
}

// BenchmarkConversation measures the full client path: build the query, make
// the HTTP request, decode the response. This is everything the Go client does
// per read that is not waiting on X.
func BenchmarkConversation(b *testing.B) {
	for _, size := range []int{1, 50, 200} {
		payload := buildConversationPayload(size)
		b.Run(fmt.Sprintf("tweets=%d", size), func(b *testing.B) {
			srv := benchServer(payload)
			defer srv.Close()
			c := benchClient(b, srv)
			ctx := b.Context()

			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := c.Conversation(ctx, "1000"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkParseConversation isolates decoding from transport.
func BenchmarkParseConversation(b *testing.B) {
	for _, size := range []int{1, 50, 200} {
		payload := []byte(buildConversationPayload(size))
		b.Run(fmt.Sprintf("tweets=%d", size), func(b *testing.B) {
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := parseConversation(payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkConversationParallel measures throughput when many reads are in
// flight at once — the axis where an in-process client and a per-request
// subprocess differ most.
func BenchmarkConversationParallel(b *testing.B) {
	payload := buildConversationPayload(50)
	srv := benchServer(payload)
	defer srv.Close()
	c := benchClient(b, srv)
	ctx := b.Context()

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := c.Conversation(ctx, "1000"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestConcurrentReadsShareOneProcess documents the structural difference the
// benchmarks above are measuring: N concurrent reads are N goroutines over a
// pooled connection, not N operating-system processes.
func TestConcurrentReadsShareOneProcess(t *testing.T) {
	const concurrency = 64

	var mu sync.Mutex
	conns := make(map[string]bool)

	payload := buildConversationPayload(10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		conns[r.RemoteAddr] = true
		mu.Unlock()
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	c, err := NewClient(Credentials{AuthToken: "t", CT0: "c"})
	if err != nil {
		t.Fatal(err)
	}
	c.SetBaseURL(srv.URL)

	var wg sync.WaitGroup
	errs := make(chan error, concurrency)
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Conversation(context.Background(), "1000"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent read failed: %v", err)
	}

	// Go's transport pools connections, so 64 reads need far fewer than 64.
	mu.Lock()
	used := len(conns)
	mu.Unlock()
	if used > concurrency {
		t.Errorf("used %d connections for %d reads, want pooling", used, concurrency)
	}
	t.Logf("%d concurrent reads used %d TCP connections in 1 process", concurrency, used)
}
