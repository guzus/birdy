package cmd

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
)

const (
	hostWSReadLimitBytes   int64 = 32 * 1024
	hostWSMaxInputBytes          = 8 * 1024
	hostWSMaxMsgsPerSecond       = 300
)

var (
	hostAddrFlag       string
	hostInviteCodeFlag string
)

var hostCmd = &cobra.Command{
	Use:     "host",
	Short:   "Host birdy TUI in your browser",
	Long:    "Start a browser-accessible terminal session that runs `birdy tui` over WebSocket.",
	GroupID: "birdy",
	RunE: func(cmd *cobra.Command, args []string) error {
		inviteCode, err := ensureHostInviteCode(hostInviteCodeFlag)
		if err != nil {
			return err
		}

		allowedOrigins := parseAllowedOrigins(os.Getenv("BIRDY_HOST_ALLOWED_ORIGINS"))
		webDir, _ := resolveHostWebDir()

		fmt.Fprintf(cmd.OutOrStdout(), "birdy web host starting at %s\n", hostAddrFlag)
		fmt.Fprintf(cmd.OutOrStdout(), "open: %s\n", hostedAccessURL(hostAddrFlag))
		fmt.Fprintln(cmd.OutOrStdout(), "invite code required to start session")
		if webDir != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "web client: %s\n", webDir)
		}

		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "ok")
		})
		mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
			if !isHostOriginAllowed(r, allowedOrigins) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
			serveHostedTTY(w, r, inviteCode)
		})

		mux.Handle("/", makeHostedWebHandler(webDir))

		server := &http.Server{
			Addr:              hostAddrFlag,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		return server.ListenAndServe()
	},
}

type hostedWSMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Code string `json:"code,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

type hostedWSAuthMessage struct {
	Type  string `json:"type"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

var hostUpgrader = websocket.Upgrader{
	ReadBufferSize:  8 * 1024,
	WriteBufferSize: 8 * 1024,
	CheckOrigin: func(_ *http.Request) bool {
		// Origin checks are enforced in the HTTP handler.
		return true
	},
}

type hostWSRateLimiter struct {
	windowStart time.Time
	count       int
}

func (l *hostWSRateLimiter) allow(now time.Time, limit int) bool {
	if limit <= 0 {
		return true
	}
	if l.windowStart.IsZero() || now.Sub(l.windowStart) >= time.Second {
		l.windowStart = now
		l.count = 0
	}
	l.count++
	return l.count <= limit
}

func serveHostedTTY(w http.ResponseWriter, r *http.Request, inviteCode string) {
	conn, err := hostUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	conn.SetReadLimit(hostWSReadLimitBytes)
	if tcpConn, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	if ok := authenticateHostedWS(conn, inviteCode); !ok {
		return
	}

	// Wait for the browser to send its terminal size before starting the TUI.
	initSize := pty.Winsize{Cols: 120, Rows: 36}
	limiter := hostWSRateLimiter{}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		msgType, payload, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		if !limiter.allow(time.Now(), hostWSMaxMsgsPerSecond) {
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "rate limit exceeded"),
				time.Now().Add(time.Second))
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var msg hostedWSMessage
		if json.Unmarshal(payload, &msg) != nil {
			continue
		}
		if msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
			initSize = pty.Winsize{Cols: msg.Cols, Rows: msg.Rows}
			break
		}
	}
	conn.SetReadDeadline(time.Time{})

	exePath, err := os.Executable()
	if err != nil || strings.TrimSpace(exePath) == "" {
		exePath = "birdy"
	}

	child := exec.Command(exePath, "tui")
	child.Env = append(os.Environ(), "BIRDY_TUI_MOUSE=1")

	ptmx, err := pty.StartWithSize(child, &initSize)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("\r\nfailed to start birdy tui\r\n"))
		return
	}
	defer func() {
		_ = ptmx.Close()
		if child.Process != nil {
			_ = child.Process.Kill()
		}
		_ = child.Wait()
	}()

	var debugFile *os.File
	if os.Getenv("BIRDY_HOST_DEBUG") != "" {
		debugFile, _ = os.Create("/tmp/birdy-pty-debug.log")
	}
	defer func() {
		if debugFile != nil {
			_ = debugFile.Close()
		}
	}()

	var lastInputAtUnixNano atomic.Int64

	done := make(chan struct{})
	go func() {
		defer close(done)
		const (
			flushInterval = 4 * time.Millisecond
			maxBatchBytes = 64 * 1024
		)

		chunks := make(chan []byte, 128)
		go func() {
			defer close(chunks)
			buf := make([]byte, 8192)
			for {
				n, readErr := ptmx.Read(buf)
				if n > 0 {
					chunk := append([]byte(nil), buf[:n]...)
					if debugFile != nil {
						_, _ = fmt.Fprintf(debugFile, "--- chunk %d bytes ---\\n%q\\n", n, chunk)
					}
					chunks <- chunk
				}
				if readErr != nil {
					return
				}
			}
		}()

		ticker := time.NewTicker(flushInterval)
		defer ticker.Stop()

		pending := make([]byte, 0, maxBatchBytes)
		lastFlushAt := time.Time{}
		flush := func() bool {
			if len(pending) == 0 {
				return true
			}
			if writeErr := conn.WriteMessage(websocket.BinaryMessage, pending); writeErr != nil {
				return false
			}
			pending = pending[:0]
			lastFlushAt = time.Now()
			return true
		}

		hasRecentInput := func(now time.Time) bool {
			ns := lastInputAtUnixNano.Load()
			if ns == 0 {
				return false
			}
			recentAt := time.Unix(0, ns)
			return now.Sub(recentAt) <= 140*time.Millisecond
		}

		for {
			select {
			case chunk, ok := <-chunks:
				if !ok {
					_ = flush()
					return
				}
				pending = append(pending, chunk...)
				if len(pending) >= maxBatchBytes && !flush() {
					return
				}

				now := time.Now()
				if len(pending) > 0 && hasRecentInput(now) {
					if lastFlushAt.IsZero() || now.Sub(lastFlushAt) >= time.Millisecond {
						if !flush() {
							return
						}
					}
				}
			case <-ticker.C:
				if !flush() {
					return
				}
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
		}

		msgType, payload, readErr := conn.ReadMessage()
		if readErr != nil {
			return
		}
		if !limiter.allow(time.Now(), hostWSMaxMsgsPerSecond) {
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "rate limit exceeded"),
				time.Now().Add(time.Second))
			return
		}

		if msgType == websocket.BinaryMessage {
			if len(payload) == 0 || len(payload) > hostWSMaxInputBytes {
				continue
			}
			lastInputAtUnixNano.Store(time.Now().UnixNano())
			_, _ = ptmx.Write(payload)
			continue
		}
		if msgType != websocket.TextMessage {
			continue
		}

		var msg hostedWSMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "input":
			if msg.Data == "" {
				continue
			}
			if len(msg.Data) > hostWSMaxInputBytes {
				continue
			}
			lastInputAtUnixNano.Store(time.Now().UnixNano())
			_, _ = ptmx.Write([]byte(msg.Data))
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
			}
		}
	}
}

func authenticateHostedWS(conn *websocket.Conn, inviteCode string) bool {
	conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	defer conn.SetReadDeadline(time.Time{})

	_, payload, err := conn.ReadMessage()
	if err != nil {
		return false
	}

	var msg hostedWSMessage
	if err := json.Unmarshal(payload, &msg); err != nil || msg.Type != "auth" {
		_ = conn.WriteJSON(hostedWSAuthMessage{Type: "auth", OK: false, Error: "missing auth message"})
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "missing auth"),
			time.Now().Add(time.Second))
		return false
	}

	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(msg.Code)), []byte(inviteCode)) != 1 {
		_ = conn.WriteJSON(hostedWSAuthMessage{Type: "auth", OK: false, Error: "invalid invite code"})
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid invite code"),
			time.Now().Add(time.Second))
		return false
	}

	if err := conn.WriteJSON(hostedWSAuthMessage{Type: "auth", OK: true}); err != nil {
		return false
	}
	return true
}

func ensureHostInviteCode(flagValue string) (string, error) {
	code := strings.TrimSpace(flagValue)
	if code == "" {
		code = strings.TrimSpace(os.Getenv("BIRDY_HOST_INVITE_CODE"))
	}
	if code == "" {
		code = strings.TrimSpace(os.Getenv("BIRDY_HOST_TOKEN"))
	}
	if code == "" {
		return "", fmt.Errorf("missing invite code: set --invite-code or BIRDY_HOST_INVITE_CODE")
	}
	return code, nil
}

func parseAllowedOrigins(raw string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		norm := normalizeOrigin(part)
		if norm == "" {
			continue
		}
		out[norm] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeOrigin(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func requestScheme(r *http.Request) string {
	if xf := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); xf != "" {
		if i := strings.IndexByte(xf, ','); i >= 0 {
			xf = xf[:i]
		}
		xf = strings.TrimSpace(strings.ToLower(xf))
		if xf == "https" || xf == "http" {
			return xf
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func isHostOriginAllowed(r *http.Request, allowed map[string]struct{}) bool {
	origin := normalizeOrigin(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}

	expected := requestScheme(r) + "://" + r.Host
	if subtle.ConstantTimeCompare([]byte(origin), []byte(expected)) == 1 {
		return true
	}

	if len(allowed) == 0 {
		return false
	}
	_, ok := allowed[origin]
	return ok
}

func hostedAccessURL(addr string) string {
	host := advertisedHost(addr)
	return "http://" + host
}

func advertisedHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			return "127.0.0.1" + addr
		}
		if !strings.Contains(addr, ":") {
			return "127.0.0.1:" + addr
		}
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func setHostedSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; "+
			"base-uri 'none'; frame-ancestors 'none'; form-action 'none'; "+
			"connect-src 'self' ws: wss:; "+
			"script-src 'self'; style-src 'self'; "+
			"img-src 'self' data:; font-src 'self'")
}

func resolveHostWebDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("BIRDY_HOST_WEB_DIR")); v != "" {
		idx := filepath.Join(v, "index.html")
		if st, err := os.Stat(idx); err == nil && !st.IsDir() {
			return v, nil
		}
		return "", fmt.Errorf("BIRDY_HOST_WEB_DIR=%q missing index.html", v)
	}

	candidates := []string{
		filepath.Join("web", "dist"),
		filepath.Join("app", "web", "dist"),
		filepath.Join(string(filepath.Separator), "app", "web", "dist"),
	}
	for _, c := range candidates {
		idx := filepath.Join(c, "index.html")
		if st, err := os.Stat(idx); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", nil
}

func makeHostedWebHandler(webDir string) http.Handler {
	if webDir == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setHostedSecurityHeaders(w)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "birdy host web client not built. Run: cd web && bun install && bun run build\n")
		})
	}

	root := filepath.Clean(webDir)
	indexPath := filepath.Join(root, "index.html")
	fs := http.FileServer(http.Dir(root))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setHostedSecurityHeaders(w)

		// Only serve GET/HEAD for static assets.
		switch r.Method {
		case http.MethodGet, http.MethodHead:
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		if r.URL.Path == "/" {
			http.ServeFile(w, r, indexPath)
			return
		}

		// Serve file if it exists, otherwise fall back to index.html for SPA routes.
		rel := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/")))
		candidate := filepath.Join(root, rel)
		if !strings.HasPrefix(candidate, root+string(filepath.Separator)) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}

		http.ServeFile(w, r, indexPath)
	})
}

func init() {
	hostCmd.Flags().StringVar(&hostAddrFlag, "addr", "127.0.0.1:8787", "listen address for hosted TUI")
	hostCmd.Flags().StringVar(&hostInviteCodeFlag, "invite-code", "", "invite code for web host (or set BIRDY_HOST_INVITE_CODE)")
	hostCmd.Flags().StringVar(&hostInviteCodeFlag, "token", "", "deprecated alias for --invite-code")
	_ = hostCmd.Flags().MarkHidden("token")
	rootCmd.AddCommand(hostCmd)
}
