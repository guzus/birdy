package vpn

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestBootstrapEndToEnd is the load-bearing test: it verifies that
// installing the bootstrap into a Node process via NODE_OPTIONS actually
// causes fetch() to route through HTTPS_PROXY. Without the
// `globalThis.fetch = undici.fetch` override in bootstrap.js, Node's
// built-in fetch silently bypasses the proxy (verified empirically on
// Node 26).
//
// Skipped unless NODE_BIN is on PATH and BIRDY_TEST_UNDICI_PATH points
// at a working undici install. In CI this means seeding the cache;
// locally `birdy vpn install-deps` populates it.
func TestBootstrapEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
	undici, err := UndiciDir()
	if err != nil {
		t.Skipf("UndiciDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(undici, "package.json")); err != nil {
		t.Skipf("undici not installed at %s; run: birdy vpn install-deps", undici)
	}

	// HTTPS target the Node script will fetch.
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello via vpn"))
	}))
	defer target.Close()

	// Mock SOCKS5 server. authCalls increments only when a SOCKS5 handshake
	// completes — a Node fetch that bypasses the proxy leaves this at zero.
	var authCalls atomic.Int64
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go bootstrapTestSocks5(c, &authCalls)
		}
	}()
	socksPort := ln.Addr().(*net.TCPAddr).Port

	bridge, err := Start("127.0.0.1", socksPort, "u", "p")
	if err != nil {
		t.Fatalf("Start bridge: %v", err)
	}
	defer bridge.Stop()
	bootstrapPath, err := BootstrapPath()
	if err != nil {
		t.Fatalf("BootstrapPath: %v", err)
	}

	cmd := exec.Command("node", "-e", fmt.Sprintf(`
fetch(%q, { signal: AbortSignal.timeout(5000) })
  .then(r => r.text())
  .then(t => process.stdout.write(t))
  .catch(e => { process.stderr.write('NODE_ERR: ' + e.message); process.exit(3); });
`, target.URL))
	cmd.Env = append(os.Environ(),
		"HTTPS_PROXY=http://"+bridge.Addr(),
		"NODE_OPTIONS=--require="+bootstrapPath,
		"BIRDY_UNDICI_PATH="+undici,
		"NODE_TLS_REJECT_UNAUTHORIZED=0", // httptest cert is self-signed
	)
	out, err := cmd.Output() // stdout only — Node prints TLS warnings to stderr
	if err != nil {
		t.Fatalf("node run failed: %v\noutput: %s", err, out)
	}
	body := string(out)
	if body != "hello via vpn" {
		t.Errorf("expected target body to be 'hello via vpn', got %q", body)
	}
	// Give a moment for the goroutine's atomic add to settle.
	time.Sleep(100 * time.Millisecond)
	if authCalls.Load() < 1 {
		t.Errorf(
			"SOCKS5 saw 0 auth calls — fetch bypassed the proxy.\n"+
				"This means bootstrap.js failed to override globalThis.fetch; "+
				"check the `globalThis.fetch = undici.fetch` line.",
		)
	}
}

func bootstrapTestSocks5(client net.Conn, authCalls *atomic.Int64) {
	defer client.Close()
	buf := make([]byte, 256)
	if _, err := io.ReadFull(client, buf[:2]); err != nil {
		return
	}
	if _, err := io.ReadFull(client, buf[:int(buf[1])]); err != nil {
		return
	}
	_, _ = client.Write([]byte{0x05, 0x02})
	if _, err := io.ReadFull(client, buf[:2]); err != nil {
		return
	}
	ulen := int(buf[1])
	if _, err := io.ReadFull(client, buf[:ulen]); err != nil {
		return
	}
	if _, err := io.ReadFull(client, buf[:1]); err != nil {
		return
	}
	plen := int(buf[0])
	if _, err := io.ReadFull(client, buf[:plen]); err != nil {
		return
	}
	authCalls.Add(1)
	_, _ = client.Write([]byte{0x01, 0x00})
	if _, err := io.ReadFull(client, buf[:4]); err != nil {
		return
	}
	atyp := buf[3]
	var host string
	switch atyp {
	case 0x01:
		_, _ = io.ReadFull(client, buf[:4])
		host = net.IP(buf[:4]).String()
	case 0x03:
		_, _ = io.ReadFull(client, buf[:1])
		dl := int(buf[0])
		_, _ = io.ReadFull(client, buf[:dl])
		host = string(buf[:dl])
	}
	_, _ = io.ReadFull(client, buf[:2])
	port := binary.BigEndian.Uint16(buf[:2])
	remote, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 5*time.Second)
	if err != nil {
		_, _ = client.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()
	_, _ = client.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	go func() { _, _ = io.Copy(remote, client) }()
	_, _ = io.Copy(client, remote)
}
