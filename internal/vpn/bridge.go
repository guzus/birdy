package vpn

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// Bridge is an in-process HTTP CONNECT proxy that forwards to a remote
// SOCKS5 endpoint. It exists because bird's Node fetch() can only talk
// to an HTTP(S) proxy (via undici ProxyAgent), not SOCKS5 directly.
//
// Lifetime is tied to one birdy invocation: Start spawns a goroutine
// that accepts connections; Stop closes the listener which unblocks
// Accept() and lets the goroutine exit.
type Bridge struct {
	listener net.Listener
	dialer   proxy.Dialer
	server   string // hostname of upstream SOCKS5 (for log messages)

	mu     sync.Mutex
	closed bool
}

// Start opens a local TCP listener on 127.0.0.1:<random> and wires it
// to a SOCKS5 dialer pointing at server:port with the given credentials.
// Returns immediately; serving runs in a goroutine.
func Start(server string, port int, user, password string) (*Bridge, error) {
	if server == "" {
		return nil, fmt.Errorf("vpn: empty SOCKS5 server")
	}
	if port == 0 {
		port = 1080
	}
	target := fmt.Sprintf("%s:%d", server, port)

	auth := &proxy.Auth{User: user, Password: password}
	dialer, err := proxy.SOCKS5("tcp", target, auth, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("vpn: building SOCKS5 dialer: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("vpn: opening local listener: %w", err)
	}

	b := &Bridge{listener: ln, dialer: dialer, server: server}
	go b.serve()
	return b, nil
}

// Addr returns the local "host:port" the bridge is listening on.
// Set this as HTTPS_PROXY=http://<addr> for the subprocess.
func (b *Bridge) Addr() string {
	return b.listener.Addr().String()
}

// Server returns the upstream SOCKS5 hostname (for log/UI).
func (b *Bridge) Server() string {
	return b.server
}

// Stop closes the listener; in-flight CONNECTs continue until their
// remote side closes or until the underlying dialer's idle timeout.
func (b *Bridge) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	_ = b.listener.Close()
}

func (b *Bridge) serve() {
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			// Listener closed via Stop().
			return
		}
		go b.handleConn(conn)
	}
}

// handleConn implements a minimal HTTP CONNECT proxy. Only the CONNECT
// verb is supported — bird issues only HTTPS requests, so plain GET/POST
// proxying is not needed.
func (b *Bridge) handleConn(client net.Conn) {
	defer client.Close()
	_ = client.SetReadDeadline(time.Now().Add(30 * time.Second))

	reader := bufio.NewReader(client)
	requestLine, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	requestLine = strings.TrimRight(requestLine, "\r\n")

	parts := strings.Fields(requestLine)
	if len(parts) < 3 || strings.ToUpper(parts[0]) != "CONNECT" {
		_, _ = client.Write([]byte("HTTP/1.1 405 Method Not Allowed\r\n\r\n"))
		return
	}
	target := parts[1]

	// Drain the rest of the headers (until blank line) so the CONNECT
	// request is fully consumed before we hijack the socket for tunneling.
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	remote, err := b.dialer.Dial("tcp", target)
	if err != nil {
		_, _ = client.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer remote.Close()

	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	_ = client.SetReadDeadline(time.Time{})

	// Bidirectional pipe until either side closes.
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, reader); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, remote); done <- struct{}{} }()
	<-done
}

// validateSOCKS5URL is a small helper for callers that already have a
// URL form (socks5://user:pass@host:port) — returns the parts.
func parseSOCKS5URL(raw string) (host string, port int, user, password string, err error) {
	u, perr := url.Parse(raw)
	if perr != nil {
		return "", 0, "", "", fmt.Errorf("invalid SOCKS5 URL %q: %w", raw, perr)
	}
	if u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return "", 0, "", "", fmt.Errorf("unsupported scheme %q (want socks5)", u.Scheme)
	}
	host = u.Hostname()
	port = 1080
	if p := u.Port(); p != "" {
		_, perr = fmt.Sscanf(p, "%d", &port)
		if perr != nil {
			return "", 0, "", "", fmt.Errorf("invalid port %q", p)
		}
	}
	if u.User != nil {
		user = u.User.Username()
		password, _ = u.User.Password()
	}
	return host, port, user, password, nil
}
