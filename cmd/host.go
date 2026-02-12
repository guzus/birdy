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
	"strings"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
)

const (
	hostWSReadLimitBytes   int64 = 32 * 1024
	hostWSMaxInputBytes          = 8 * 1024
	hostWSMaxMsgsPerSecond       = 220
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

		fmt.Fprintf(cmd.OutOrStdout(), "birdy web host starting at %s\n", hostAddrFlag)
		fmt.Fprintf(cmd.OutOrStdout(), "open: %s\n", hostedAccessURL(hostAddrFlag))
		fmt.Fprintln(cmd.OutOrStdout(), "invite code required to start session")

		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			setHostedSecurityHeaders(w)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, hostedPageHTML)
		})
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

	if ok := authenticateHostedWS(conn, inviteCode); !ok {
		return
	}

	// Wait for the browser to send its terminal size before starting the TUI.
	// This avoids a stale first frame rendered at a wrong size that xterm.js
	// may not fully overwrite when the resize arrives.
	initSize := pty.Winsize{Cols: 120, Rows: 36}
	limiter := hostWSRateLimiter{}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, payload, readErr := conn.ReadMessage()
		if readErr != nil {
			// Timed out or connection closed before we got a resize; use default.
			break
		}
		if !limiter.allow(time.Now(), hostWSMaxMsgsPerSecond) {
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "rate limit exceeded"),
				time.Now().Add(time.Second))
			return
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
	conn.SetReadDeadline(time.Time{}) // clear deadline

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

	// Debug: capture raw PTY output to /tmp/birdy-pty-debug.log
	var debugFile *os.File
	if os.Getenv("BIRDY_HOST_DEBUG") != "" {
		debugFile, _ = os.Create("/tmp/birdy-pty-debug.log")
	}
	defer func() {
		if debugFile != nil {
			_ = debugFile.Close()
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		const (
			flushInterval = 8 * time.Millisecond
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
		flush := func() bool {
			if len(pending) == 0 {
				return true
			}
			if writeErr := conn.WriteMessage(websocket.BinaryMessage, pending); writeErr != nil {
				return false
			}
			pending = pending[:0]
			return true
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

		_, payload, readErr := conn.ReadMessage()
		if readErr != nil {
			return
		}
		if !limiter.allow(time.Now(), hostWSMaxMsgsPerSecond) {
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "rate limit exceeded"),
				time.Now().Add(time.Second))
			return
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
			"base-uri 'none'; frame-ancestors 'none'; form-action 'self'; "+
			"connect-src 'self' ws: wss:; "+
			"script-src 'self' https://cdn.jsdelivr.net 'unsafe-inline'; "+
			"style-src 'self' https://cdn.jsdelivr.net 'unsafe-inline'; "+
			"img-src 'self' data:; font-src 'self' https://cdn.jsdelivr.net")
}

const hostedPageHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <title>birdy host</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/xterm@5.3.0/css/xterm.min.css" />
  <style>
    html, body {
      margin: 0;
      height: 100%;
      overflow: hidden;
      background: #000;
      color: #d8f2ff;
      font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    }
    #root {
      display: grid;
      grid-template-rows: auto minmax(0, 1fr);
      height: 100%;
      min-height: 100%;
    }
    #top {
      box-sizing: border-box;
      display: flex;
      justify-content: space-between;
      align-items: center;
      padding: 8px 12px;
      border-bottom: 1px solid #0ea5e9;
      background: #03101b;
      color: #38bdf8;
      font-weight: 700;
      letter-spacing: 0.03em;
    }
    #status { color: #cbd5e1; font-weight: 500; }
    #term {
      width: 100%;
      height: 100%;
      min-height: 0;
      box-sizing: border-box;
      overflow: hidden;
    }
    #term .xterm, #term .xterm-viewport {
      height: 100%;
    }
  </style>
</head>
<body>
  <div id="root">
    <div id="top"><span>BIRDY HOST</span><span id="status">invite code required</span></div>
    <div id="term"></div>
  </div>

  <script src="https://cdn.jsdelivr.net/npm/xterm@5.3.0/lib/xterm.min.js"></script>
  <script src="https://cdn.jsdelivr.net/npm/xterm-addon-fit@0.8.0/lib/xterm-addon-fit.min.js"></script>
  <script src="https://cdn.jsdelivr.net/npm/xterm-addon-web-links@0.9.0/lib/xterm-addon-web-links.min.js"></script>
  <script>
    const statusEl = document.getElementById("status");
    const termEl = document.getElementById("term");
    const inviteCodeKey = "birdy_host_invite_code";

    const term = new Terminal({
      cursorBlink: true,
      scrollback: 5000,
      theme: {
        background: "#000000",
        foreground: "#d8f2ff",
        cursor: "#38bdf8"
      }
    });
    const fitAddon = new FitAddon.FitAddon();
    const linksAddon = new WebLinksAddon.WebLinksAddon();
    term.loadAddon(fitAddon);
    term.loadAddon(linksAddon);
    term.open(termEl);
    fitAddon.fit();

    let ws = null;
    let inviteCode = "";
    let authed = false;
    const utf8Decoder = new TextDecoder();

    let lastCols = 0;
    let lastRows = 0;
    function sendResize() {
      if (!authed || !ws || ws.readyState !== WebSocket.OPEN) return;
      if (term.cols === lastCols && term.rows === lastRows) return;
      lastCols = term.cols;
      lastRows = term.rows;
      ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    }

    function fitAndResize() {
      fitAddon.fit();
      sendResize();
    }

    function startAuthFlow() {
      const prior = window.sessionStorage.getItem(inviteCodeKey) || "";
      const entered = window.prompt("Enter invite code", prior);
      if (entered === null) {
        statusEl.textContent = "invite code required";
        return;
      }
      inviteCode = entered.trim();
      if (!inviteCode) {
        statusEl.textContent = "invite code required";
        return;
      }
      connect();
    }

    function connect() {
      const wsProto = window.location.protocol === "https:" ? "wss" : "ws";
      ws = new WebSocket(wsProto + "://" + window.location.host + "/ws");
      ws.binaryType = "arraybuffer";
      authed = false;
      statusEl.textContent = "authenticating...";

      ws.onopen = () => {
        ws.send(JSON.stringify({ type: "auth", code: inviteCode }));
      };

      ws.onclose = () => {
        const tail = utf8Decoder.decode();
        if (tail) {
          term.write(tail);
        }
        if (!authed) {
          window.sessionStorage.removeItem(inviteCodeKey);
          statusEl.textContent = "invalid invite code";
          setTimeout(startAuthFlow, 150);
          return;
        }
        statusEl.textContent = "disconnected";
      };

      ws.onerror = () => {
        statusEl.textContent = authed ? "error" : "auth failed";
      };

      ws.onmessage = (event) => {
        if (typeof event.data === "string") {
          let control = null;
          try {
            control = JSON.parse(event.data);
          } catch (_) {
            control = null;
          }
          if (control && control.type === "auth") {
            if (!control.ok) {
              statusEl.textContent = control.error || "invalid invite code";
              return;
            }
            authed = true;
            window.sessionStorage.setItem(inviteCodeKey, inviteCode);
            statusEl.textContent = "live";
            fitAndResize();
            requestAnimationFrame(fitAndResize);
            setTimeout(fitAndResize, 100);
            setTimeout(fitAndResize, 350);
            term.focus();
            return;
          }
          if (authed) {
            term.write(event.data);
          }
          return;
        }
        if (!authed) return;
        if (event.data instanceof ArrayBuffer) {
          const text = utf8Decoder.decode(event.data, { stream: true });
          if (text) {
            term.write(text);
          }
          return;
        }
        if (event.data && event.data.arrayBuffer) {
          event.data.arrayBuffer().then((buf) => {
            const text = utf8Decoder.decode(buf, { stream: true });
            if (text) {
              term.write(text);
            }
          });
        }
      };
    }

    term.onData((data) => {
      if (authed && ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "input", data }));
      }
    });

    let resizeRAF = 0;
    function scheduleFitAndResize() {
      if (resizeRAF !== 0) return;
      resizeRAF = requestAnimationFrame(() => {
        resizeRAF = 0;
        fitAndResize();
      });
    }

    window.addEventListener("resize", scheduleFitAndResize);
    window.addEventListener("load", () => {
      scheduleFitAndResize();
      startAuthFlow();
    });
    if (typeof ResizeObserver !== "undefined") {
      const observer = new ResizeObserver(scheduleFitAndResize);
      observer.observe(termEl);
    }
  </script>
</body>
</html>`

func init() {
	hostCmd.Flags().StringVar(&hostAddrFlag, "addr", "127.0.0.1:8787", "listen address for hosted TUI")
	hostCmd.Flags().StringVar(&hostInviteCodeFlag, "invite-code", "", "invite code for web host (or set BIRDY_HOST_INVITE_CODE)")
	hostCmd.Flags().StringVar(&hostInviteCodeFlag, "token", "", "deprecated alias for --invite-code")
	_ = hostCmd.Flags().MarkHidden("token")
	rootCmd.AddCommand(hostCmd)
}
