package vpn

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// startMockSOCKS5 stands up a minimal SOCKS5 server that accepts
// username/password auth and forwards CONNECT requests to the real host.
// Returns (host:port, stop). The boolean atomic counts how many times
// auth was negotiated, so tests can verify the bridge actually spoke
// SOCKS5 to it.
func startMockSOCKS5(t *testing.T, wantUser, wantPass string, authCalls *atomic.Int64) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				close(done)
				return
			}
			go handleMockSOCKS5(conn, wantUser, wantPass, authCalls)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close(); <-done }
}

// handleMockSOCKS5 implements the bare minimum of RFC 1928 needed to
// validate that our Go-side dialer is speaking SOCKS5 correctly.
func handleMockSOCKS5(client net.Conn, wantUser, wantPass string, authCalls *atomic.Int64) {
	defer client.Close()

	// 1. Method negotiation: VER, NMETHODS, METHODS...
	buf := make([]byte, 256)
	if _, err := io.ReadFull(client, buf[:2]); err != nil {
		return
	}
	if buf[0] != 0x05 {
		return
	}
	nm := int(buf[1])
	if _, err := io.ReadFull(client, buf[:nm]); err != nil {
		return
	}
	// Always require username/password (0x02)
	client.Write([]byte{0x05, 0x02})

	// 2. Username/password auth: VER, ULEN, UNAME, PLEN, PASSWD
	if _, err := io.ReadFull(client, buf[:2]); err != nil {
		return
	}
	if buf[0] != 0x01 {
		return
	}
	ulen := int(buf[1])
	if _, err := io.ReadFull(client, buf[:ulen]); err != nil {
		return
	}
	user := string(buf[:ulen])
	if _, err := io.ReadFull(client, buf[:1]); err != nil {
		return
	}
	plen := int(buf[0])
	if _, err := io.ReadFull(client, buf[:plen]); err != nil {
		return
	}
	pass := string(buf[:plen])
	if user != wantUser || pass != wantPass {
		client.Write([]byte{0x01, 0x01})
		return
	}
	authCalls.Add(1)
	client.Write([]byte{0x01, 0x00})

	// 3. CONNECT request: VER, CMD, RSV, ATYP, DST.ADDR, DST.PORT
	if _, err := io.ReadFull(client, buf[:4]); err != nil {
		return
	}
	if buf[1] != 0x01 { // CMD must be CONNECT
		return
	}
	atyp := buf[3]
	var host string
	switch atyp {
	case 0x01: // IPv4
		if _, err := io.ReadFull(client, buf[:4]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 0x03: // DOMAIN
		if _, err := io.ReadFull(client, buf[:1]); err != nil {
			return
		}
		dl := int(buf[0])
		if _, err := io.ReadFull(client, buf[:dl]); err != nil {
			return
		}
		host = string(buf[:dl])
	case 0x04: // IPv6
		if _, err := io.ReadFull(client, buf[:16]); err != nil {
			return
		}
		host = net.IP(buf[:16]).String()
	default:
		return
	}
	if _, err := io.ReadFull(client, buf[:2]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(buf[:2])

	// Dial out to the real target
	remote, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 5*time.Second)
	if err != nil {
		client.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()
	client.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})

	// Pipe bidirectionally
	go func() { _, _ = io.Copy(remote, client) }()
	_, _ = io.Copy(client, remote)
}

func TestBridgeEndToEndViaSOCKS5(t *testing.T) {
	var authCalls atomic.Int64

	// 1. Target HTTPS server we'll fetch through the bridge.
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from target"))
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)

	// 2. Mock SOCKS5 server
	socksAddr, stopSocks := startMockSOCKS5(t, "alice", "secret", &authCalls)
	defer stopSocks()

	host, portStr, _ := net.SplitHostPort(socksAddr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	// 3. Start the birdy bridge pointing at the mock SOCKS5
	bridge, err := Start(host, port, "alice", "secret")
	if err != nil {
		t.Fatalf("Start bridge: %v", err)
	}
	defer bridge.Stop()

	// 4. Build an HTTP client that uses the bridge as its HTTPS proxy
	bridgeURL, _ := url.Parse("http://" + bridge.Addr())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(bridgeURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // httptest cert
		},
		Timeout: 5 * time.Second,
	}

	// 5. Make the request — should traverse bridge → SOCKS5 → target
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello from target" {
		t.Errorf("unexpected body: %q", body)
	}
	if authCalls.Load() < 1 {
		t.Error("SOCKS5 server never saw an auth attempt — request bypassed proxy")
	}

	_ = targetURL
}

func TestBridgeRejectsNonCONNECT(t *testing.T) {
	var unused atomic.Int64
	socksAddr, stopSocks := startMockSOCKS5(t, "u", "p", &unused)
	defer stopSocks()
	host, portStr, _ := net.SplitHostPort(socksAddr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	bridge, err := Start(host, port, "u", "p")
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Stop()

	conn, err := net.Dial("tcp", bridge.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
	buf := make([]byte, 256)
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "405") {
		t.Errorf("expected 405 reply to non-CONNECT, got %q", string(buf[:n]))
	}
}

func TestParseSOCKS5URL(t *testing.T) {
	cases := []struct {
		in         string
		host       string
		port       int
		user, pass string
		wantErr    bool
	}{
		{"socks5://user:pass@host:1080", "host", 1080, "user", "pass", false},
		{"socks5://host:1080", "host", 1080, "", "", false},
		{"socks5://host", "host", 1080, "", "", false},
		{"http://host:1080", "", 0, "", "", true},
		{"not a url://", "", 0, "", "", true},
	}
	for _, c := range cases {
		h, p, u, pw, err := parseSOCKS5URL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseSOCKS5URL(%q) expected error, got nil", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSOCKS5URL(%q) unexpected err: %v", c.in, err)
			continue
		}
		if h != c.host || p != c.port || u != c.user || pw != c.pass {
			t.Errorf("parseSOCKS5URL(%q) = (%q,%d,%q,%q), want (%q,%d,%q,%q)",
				c.in, h, p, u, pw, c.host, c.port, c.user, c.pass)
		}
	}
}

// DialContext is what makes --vpn work on the native path. A regression here is
// silent: the command still succeeds, it just stops going through the exit.
func TestDialContextRejectsEmptyServer(t *testing.T) {
	if _, err := DialContext("", 1080, "u", "p"); err == nil {
		t.Error("an empty server must not produce a dialer that silently connects directly")
	}
}

func TestDialContextDefaultsThePort(t *testing.T) {
	// Port 0 means "unset" in the config, not "port zero".
	if _, err := DialContext("example.com", 0, "u", "p"); err != nil {
		t.Errorf("port 0 should fall back to 1080, got %v", err)
	}
}

// The dialer must actually carry traffic to the SOCKS5 endpoint rather than
// dialing the destination directly.
func TestDialContextTargetsTheProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	reached := make(chan struct{}, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		reached <- struct{}{}
		c.Close()
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	dial, err := DialContext(host, port, "", "")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// The handshake will fail — this is not a real SOCKS5 server — but the
	// connection must land on the proxy address, which is what we assert.
	_, _ = dial(ctx, "tcp", "example.com:443")

	select {
	case <-reached:
	case <-time.After(3 * time.Second):
		t.Error("dialer never connected to the SOCKS5 endpoint")
	}
}
