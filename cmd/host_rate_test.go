package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestHostedProxyIP(t *testing.T) {
	p := hostedProxy{
		trusted: []netip.Prefix{netip.MustParsePrefix("10.0.1.19/32"), netip.MustParsePrefix("10.8.0.0/24")},
		tunnels: []netip.Prefix{netip.MustParsePrefix("10.0.1.5/32")},
	}
	for _, tc := range []struct {
		name, remote  string
		forwarded, cf []string
		want          string
	}{
		{"direct mapped IPv4", "[::ffff:192.0.2.1]:7724", []string{"10.0.1.5"}, []string{"203.0.113.1"}, "192.0.2.1"},
		{"direct IPv6", "[2001:0db8::9]:123", nil, nil, "2001:db8::9"},
		{"through Traefik", "10.0.1.19:5915", []string{"192.0.2.42"}, []string{"203.0.113.11"}, "192.0.2.42"},
		{"multiple trusted hops", "10.0.1.19:2", []string{"192.0.2.7, [2001:db8::5]:422", "10.8.0.3"}, nil, "2001:db8::5"},
		{"missing hop", "10.0.1.19:2", nil, []string{"192.0.2.7"}, "10.0.1.19"},
		{"invalid trailing hop", "10.0.1.19:2", []string{"192.0.2.7, unknown"}, nil, "10.0.1.19"},
		{"tunnel peer", "10.0.1.19:9", []string{"10.0.1.5"}, []string{"2001:db8::10"}, "2001:db8::10"},
		{"duplicate tunnel header", "10.0.1.19:9", []string{"10.0.1.5"}, []string{"192.0.2.7", "192.0.2.8"}, "10.0.1.5"},
		{"tunnel elsewhere in chain", "10.0.1.19:9", []string{"10.0.1.5, 192.0.2.90"}, []string{"203.0.113.2"}, "192.0.2.90"},
		{"missing peer", "", []string{"10.0.1.5"}, []string{"203.0.113.2"}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/chat/models", nil)
			req.RemoteAddr = tc.remote
			for _, v := range tc.forwarded {
				req.Header.Add("X-Forwarded-For", v)
			}
			for _, v := range tc.cf {
				req.Header.Add("CF-Connecting-IP", v)
			}
			if got := p.resolve(req); got != tc.want {
				t.Fatalf("client IP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHostedProxySettings(t *testing.T) {
	t.Setenv("BIRDY_HOST_TRUSTED_PROXIES", "10.0.1.19, 2001:db8::/64")
	t.Setenv("BIRDY_HOST_CLOUDFLARE_TUNNELS", "10.0.1.5/32")
	proxy, err := hostedProxyFromEnv()
	if err != nil || len(proxy.trusted) != 2 || len(proxy.tunnels) != 1 {
		t.Fatalf("proxy settings = %+v, %v", proxy, err)
	}
	for _, value := range []string{"example.org", "::/0", "10.0.1.19,", ""} {
		t.Setenv("BIRDY_HOST_TRUSTED_PROXIES", value)
		if _, err := hostedProxyFromEnv(); err == nil {
			t.Fatalf("accepted invalid config %q", value)
		}
	}
}

func TestHostedWebLimitsRespectRoutesAndClients(t *testing.T) {
	previous := chatLimiter
	chatLimiter = newRateLimiter(1, time.Minute)
	defer func() { chatLimiter = previous }()
	proxy := hostedProxy{trusted: []netip.Prefix{netip.MustParsePrefix("10.0.1.19/32")}, tunnels: []netip.Prefix{netip.MustParsePrefix("10.0.1.5/32")}}
	host := proxy.limitWebRequests(buildHostedMux("test", nil, t.TempDir()), newRateLimiter(3, time.Minute))
	do := func(path, ip, method string, want int) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader("{"))
		req.RemoteAddr = "10.0.1.19:94"
		req.Header.Set("CF-Connecting-IP", ip)
		req.Header.Set("X-Forwarded-For", "10.0.1.5")
		req.Header.Set("X-Invite-Code", "test")
		w := httptest.NewRecorder()
		host.ServeHTTP(w, req)
		if w.Code != want || want == 429 && w.Header().Get("Retry-After") != "60" {
			t.Fatalf("%s %s: got %d, retry %s, want %d; %s", ip, path, w.Code, w.Header().Get("Retry-After"), want, w.Body.String())
		}
	}
	do("/api/chat", "192.0.2.1", "POST", 400)
	do("/api/scan", "192.0.2.1", "POST", 429)
	do("/api/chat/models", "192.0.2.1", "GET", 200)
	do("/api/shares/example", "192.0.2.1", "GET", 429)
	do("/ws", "192.0.2.1", "GET", 429)
	do("/healthz", "192.0.2.1", "GET", 200)
	do("/api/scan", "192.0.2.2", "POST", 400)
	do("/api/chat", "192.0.2.2", "POST", 429)
}

func TestHostedRateQuotaRecovery(t *testing.T) {
	now := time.Now()
	l := newRateLimiter(3, time.Minute)
	l.maxKeys = 2
	l.now = func() time.Time { return now }
	if l.retryAfter("a", 1) != 0 {
		t.Fatal("first reservation")
	}
	now = now.Add(20 * time.Second)
	if l.retryAfter("a", 2) != 0 || l.retryAfter("b", 1) != 0 {
		t.Fatal("second reservation")
	}
	if got := l.retryAfter("a", 2); got != time.Minute {
		t.Fatalf("batch retry %v", got)
	}
	if got := l.retryAfter("a", 1); got != 40*time.Second {
		t.Fatalf("single retry %v", got)
	}
	if got := l.retryAfter("c", 1); got != 40*time.Second || len(l.requests) != 2 {
		t.Fatalf("capacity wait=%s entries=%d", got, len(l.requests))
	}
	now = now.Add(time.Minute)
	if l.retryAfter("c", 1) != 0 || len(l.requests) != 1 {
		t.Fatal("expired keys not recovered")
	}
}

func TestHostedConcurrentQuotaRelease(t *testing.T) {
	s := streamSlots{byIP: map[string]int{}, totalLimit: 2, perIP: 1}
	take := func(ip string, want int) func() {
		t.Helper()
		w := httptest.NewRecorder()
		release := s.acquire(w, ip)
		if w.Code != want || (want == 200) != (release != nil) || want != 200 && w.Header().Get("Retry-After") == "" {
			t.Fatalf("client %s status=%d release=%t headers=%v", ip, w.Code, release != nil, w.Header())
		}
		return release
	}
	releaseA := take("a", 200)
	take("a", 429)
	releaseB := take("b", 200)
	take("c", 503)
	releaseB()
	take("c", 200)()
	releaseA()
	take("a", 200)()
	if len(s.byIP) != 0 || s.total != 0 {
		t.Fatalf("leaked stream slot %+v", s.byIP)
	}
}
