package cmd

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
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

var (
	hostAddrFlag  string
	hostTokenFlag string
)

var hostCmd = &cobra.Command{
	Use:     "host",
	Short:   "Host birdy TUI in your browser",
	Long:    "Start a browser-accessible terminal session that runs `birdy tui` over WebSocket.",
	GroupID: "birdy",
	RunE: func(cmd *cobra.Command, args []string) error {
		token, generated, err := ensureHostToken(hostTokenFlag)
		if err != nil {
			return err
		}

		accessURL := hostedAccessURL(hostAddrFlag, token)
		fmt.Fprintf(cmd.OutOrStdout(), "birdy web host starting at %s\n", hostAddrFlag)
		fmt.Fprintf(cmd.OutOrStdout(), "open: %s\n", accessURL)
		if generated {
			fmt.Fprintln(cmd.OutOrStdout(), "token was generated automatically for this run")
		}

		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			if !isHostAuthorized(r, token) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, hostedPageHTML)
		})
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "ok")
		})
		mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
			if !isHostAuthorized(r, token) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			serveHostedTTY(w, r)
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
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

var hostUpgrader = websocket.Upgrader{
	ReadBufferSize:  8 * 1024,
	WriteBufferSize: 8 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Token auth gates access; allow browser clients from arbitrary origins.
		return true
	},
}

func serveHostedTTY(w http.ResponseWriter, r *http.Request) {
	conn, err := hostUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Wait for the browser to send its terminal size before starting the TUI.
	// This avoids a stale first frame rendered at a wrong size that xterm.js
	// may not fully overwrite when the resize arrives.
	initSize := pty.Winsize{Cols: 120, Rows: 36}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, payload, readErr := conn.ReadMessage()
		if readErr != nil {
			// Timed out or connection closed before we got a resize; use default.
			break
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
			debugFile.Close()
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 8192)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				if debugFile != nil {
					fmt.Fprintf(debugFile, "--- chunk %d bytes ---\n%q\n", n, buf[:n])
				}
				if writeErr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
					return
				}
			}
			if readErr != nil {
				return
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

		var msg hostedWSMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "input":
			if msg.Data != "" {
				_, _ = ptmx.Write([]byte(msg.Data))
			}
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
			}
		}
	}
}

func ensureHostToken(flagValue string) (token string, generated bool, err error) {
	token = strings.TrimSpace(flagValue)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("BIRDY_HOST_TOKEN"))
	}
	if token != "" {
		return token, false, nil
	}

	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", false, fmt.Errorf("failed to generate host token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), true, nil
}

func isHostAuthorized(r *http.Request, expectedToken string) bool {
	if expectedToken == "" {
		return true
	}
	got := hostRequestToken(r)
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(got)) == 1
}

func hostRequestToken(r *http.Request) string {
	if q := strings.TrimSpace(r.URL.Query().Get("token")); q != "" {
		return q
	}

	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return ""
	}
	const bearer = "bearer "
	if len(auth) >= len(bearer) && strings.EqualFold(auth[:len(bearer)], bearer) {
		return strings.TrimSpace(auth[len(bearer):])
	}
	return ""
}

func hostedAccessURL(addr, token string) string {
	host := advertisedHost(addr)
	return "http://" + host + "/?token=" + url.QueryEscape(token)
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
    <div id="top"><span>BIRDY HOST</span><span id="status">connecting...</span></div>
    <div id="term"></div>
  </div>

  <script src="https://cdn.jsdelivr.net/npm/xterm@5.3.0/lib/xterm.min.js"></script>
  <script src="https://cdn.jsdelivr.net/npm/xterm-addon-fit@0.8.0/lib/xterm-addon-fit.min.js"></script>
  <script src="https://cdn.jsdelivr.net/npm/xterm-addon-web-links@0.9.0/lib/xterm-addon-web-links.min.js"></script>
  <script>
    const statusEl = document.getElementById("status");
    const termEl = document.getElementById("term");
    const params = new URLSearchParams(window.location.search);
    const token = params.get("token") || "";

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

    const wsProto = window.location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(wsProto + "://" + window.location.host + "/ws?token=" + encodeURIComponent(token));
    ws.binaryType = "arraybuffer";
    const utf8Decoder = new TextDecoder();

    function sendResize() {
      if (ws.readyState !== WebSocket.OPEN) return;
      ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    }

    function fitAndResize() {
      fitAddon.fit();
      sendResize();
    }

    ws.onopen = () => {
      statusEl.textContent = "live";
      fitAndResize();
      requestAnimationFrame(fitAndResize);
      setTimeout(fitAndResize, 100);
      setTimeout(fitAndResize, 350);
      term.focus();
    };
    ws.onclose = () => {
      // Flush any pending partial UTF-8 sequence buffered by the decoder.
      const tail = utf8Decoder.decode();
      if (tail) {
        term.write(tail);
      }
      statusEl.textContent = "disconnected";
    };
    ws.onerror = () => {
      statusEl.textContent = "error";
    };
    ws.onmessage = (event) => {
      if (typeof event.data === "string") {
        term.write(event.data);
        return;
      }
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

    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "input", data }));
      }
    });

    window.addEventListener("resize", fitAndResize);
    window.addEventListener("load", fitAndResize);
    if (typeof ResizeObserver !== "undefined") {
      const observer = new ResizeObserver(fitAndResize);
      observer.observe(termEl);
    }
  </script>
</body>
</html>`

func init() {
	hostCmd.Flags().StringVar(&hostAddrFlag, "addr", "127.0.0.1:8787", "listen address for hosted TUI")
	hostCmd.Flags().StringVar(&hostTokenFlag, "token", "", "access token for web host (or set BIRDY_HOST_TOKEN)")
	rootCmd.AddCommand(hostCmd)
}
