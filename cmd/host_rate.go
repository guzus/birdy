package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	chatLimiter        = newRateLimiter(20, time.Minute)
	commandLimiter     = newRateLimiter(60, time.Minute)
	activeModelStreams = streamSlots{byIP: make(map[string]int), perIP: 2, totalLimit: 16}
)

type rateLimiter struct {
	mu             sync.Mutex
	requests       map[string][]time.Time
	limit, maxKeys int
	window         time.Duration
	lastSweep      time.Time
	now            func() time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{requests: make(map[string][]time.Time), limit: limit, maxKeys: 10_000, window: window, now: time.Now}
}

// retryAfter reserves the entire cost or returns the time until it can fit.
func (l *rateLimiter) retryAfter(ip string, cost int) time.Duration {
	if cost <= 0 {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	if now.Sub(l.lastSweep) >= l.window {
		for k, times := range l.requests {
			if !times[len(times)-1].After(cutoff) {
				delete(l.requests, k)
			}
		}
		l.lastSweep = now
	}
	times := l.requests[ip]
	if len(times) == 0 && len(l.requests) >= l.maxKeys {
		return l.lastSweep.Add(l.window).Sub(now)
	}
	if cost > l.limit {
		return l.window
	}
	for len(times) > 0 && !times[0].After(cutoff) {
		times = times[1:]
	}
	if over := len(times) + cost - l.limit; over > 0 {
		return times[over-1].Add(l.window).Sub(now)
	}
	for range cost {
		times = append(times, now)
	}
	l.requests[ip] = times
	return 0
}

func (l *rateLimiter) admit(w http.ResponseWriter, r *http.Request, cost int) bool {
	if wait := l.retryAfter(clientIP(r), cost); wait > 0 {
		seconds := int((wait + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusTooManyRequests, apiError{Error: "rate limit exceeded, try again shortly"})
		return false
	}
	return true
}

type streamSlots struct {
	mu                       sync.Mutex
	byIP                     map[string]int
	total, perIP, totalLimit int
}

func (s *streamSlots) acquire(w http.ResponseWriter, ip string) func() {
	s.mu.Lock()
	ipFull := s.byIP[ip] >= s.perIP
	if ipFull || s.total >= s.totalLimit {
		s.mu.Unlock()
		w.Header().Set("Retry-After", "1")
		w.Header().Set("Cache-Control", "no-store")
		if ipFull {
			writeJSON(w, http.StatusTooManyRequests, apiError{Error: "too many active model requests from this IP"})
		} else {
			writeJSON(w, http.StatusServiceUnavailable, apiError{Error: "all model streams are busy, try again shortly"})
		}
		return nil
	}
	s.byIP[ip]++
	s.total++
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.total--
		s.byIP[ip]--
		if s.byIP[ip] == 0 {
			delete(s.byIP, ip)
		}
	}
}

type hostedProxy struct {
	trusted, tunnels []netip.Prefix
}

func hostedProxyFromEnv() (hostedProxy, error) {
	var p hostedProxy
	for _, setting := range []struct {
		name string
		dest *[]netip.Prefix
	}{
		{"BIRDY_HOST_TRUSTED_PROXIES", &p.trusted},
		{"BIRDY_HOST_CLOUDFLARE_TUNNELS", &p.tunnels},
	} {
		raw := strings.TrimSpace(os.Getenv(setting.name))
		if raw == "" {
			continue
		}
		for _, item := range strings.Split(raw, ",") {
			item = strings.TrimSpace(item)
			prefix, err := netip.ParsePrefix(item)
			if err != nil {
				ip, err := netip.ParseAddr(item)
				if err != nil || ip.Zone() != "" {
					return p, fmt.Errorf("%s requires comma-separated proxy IP addresses or CIDRs", setting.name)
				}
				ip = ip.Unmap()
				prefix = netip.PrefixFrom(ip, ip.BitLen())
			}
			if prefix.Bits() == 0 || prefix.Addr().Is4In6() {
				return p, fmt.Errorf("%s requires specific IPv4 or IPv6 ranges", setting.name)
			}
			*setting.dest = append(*setting.dest, prefix.Masked())
		}
	}
	if len(p.tunnels) > 0 && len(p.trusted) == 0 {
		return p, fmt.Errorf("BIRDY_HOST_CLOUDFLARE_TUNNELS requires BIRDY_HOST_TRUSTED_PROXIES")
	}
	return p, nil
}

func parseHostIP(raw string) netip.Addr {
	raw = strings.TrimSpace(raw)
	ip, err := netip.ParseAddr(raw)
	if err != nil {
		pair, err := netip.ParseAddrPort(raw)
		if err != nil {
			return netip.Addr{}
		}
		ip = pair.Addr()
	}
	return ip.WithZone("").Unmap()
}

func inProxyRanges(ip netip.Addr, ranges []netip.Prefix) bool {
	for _, prefix := range ranges {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func (p hostedProxy) resolve(r *http.Request) string {
	ip := parseHostIP(r.RemoteAddr)
	if !ip.IsValid() {
		return "unknown"
	}
	// Proxies append the peer they observed on the right. Never walk past the
	// first untrusted hop, even if the client supplied other forwarding headers.
	lines := r.Header.Values("X-Forwarded-For")
	firstHop := true
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		for {
			if !inProxyRanges(ip, p.trusted) {
				return ip.String()
			}
			comma := strings.LastIndexByte(line, ',')
			hop := parseHostIP(line[comma+1:])
			if !hop.IsValid() {
				return ip.String()
			}
			if firstHop && inProxyRanges(hop, p.tunnels) {
				// Cloudflare supplies this header to cloudflared. The ingress must
				// append/overwrite XFF with its actual peer for this pairing.
				headers := r.Header.Values("CF-Connecting-IP")
				if len(headers) == 1 {
					if client, err := netip.ParseAddr(strings.TrimSpace(headers[0])); err == nil && client.Zone() == "" {
						return client.Unmap().String()
					}
				}
				return hop.String()
			}
			firstHop = false
			ip = hop
			if comma < 0 {
				break
			}
			line = line[:comma]
		}
	}
	return ip.String()
}

type hostedClientIPKey struct{}

func clientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(hostedClientIPKey{}).(string); ok {
		return ip
	}
	return (hostedProxy{}).resolve(r)
}

func (p hostedProxy) limitWebRequests(next http.Handler, limiter *rateLimiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/ws" && !strings.HasPrefix(path, "/api/") || path == "/api/harness/chat" {
			// The separately versioned harness has its own token and IP budgets.
			next.ServeHTTP(w, r)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), hostedClientIPKey{}, p.resolve(r)))
		setHostedSecurityHeaders(w)
		if limiter.admit(w, r, 1) {
			next.ServeHTTP(w, r)
		}
	})
}
