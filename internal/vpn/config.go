// Package vpn provides per-invocation SOCKS5 routing for the bird subprocess.
//
// The design is a small in-process bridge that listens locally as an HTTP
// CONNECT proxy and forwards to a remote SOCKS5 endpoint (typically a
// NordVPN service-credentialled SOCKS5 server). bird's Node fetch() is
// pointed at the local bridge via NODE_OPTIONS=--require pointing at a
// bootstrap.js that calls undici.setGlobalDispatcher with a ProxyAgent.
//
// We need the bootstrap because Node 22+ ships a built-in undici-backed
// fetch but does NOT honor HTTPS_PROXY env vars (verified empirically on
// Node 26). The bootstrap resolves undici from a birdy-managed npm install
// (~/.cache/birdy/node_modules/undici) populated by `birdy vpn install-deps`.
//
// Per-invocation control: each Start() spins up a fresh bridge on a
// random localhost port and dials a (caller-chosen) SOCKS5 server. The
// bridge dies when Stop() is called or when the parent process exits.
package vpn

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

// Config holds NordVPN (or any SOCKS5) credentials + a server pool.
//
// Stored at ~/.config/birdy/vpn.json so it stays separate from the
// accounts.json file (different concern: ops config vs auth creds).
type Config struct {
	// User / Password are NordVPN's "service credentials" (not the
	// account login). Found in the NordVPN dashboard under
	// Services → NordVPN → Manual setup.
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	// Pool is the list of SOCKS5 hostnames to rotate over. For NordVPN,
	// these look like `<country>.socks.nordhold.net` or specific server
	// hostnames published at https://support.nordvpn.com/.
	Pool []string `json:"pool,omitempty"`
	// Port is the SOCKS5 port (NordVPN's is 1080; configurable in case
	// the user routes via a different provider).
	Port int `json:"port,omitempty"`
}

func defaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "birdy", "vpn.json"), nil
}

// Load reads the VPN config from the default path. Returns a zero-value
// Config (not an error) when the file does not exist — callers should
// treat "no config" as "VPN disabled".
func Load() (*Config, error) {
	p, err := defaultPath()
	if err != nil {
		return nil, err
	}
	return LoadPath(p)
}

// LoadPath reads the VPN config from a specific path.
func LoadPath(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading vpn config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing vpn config: %w", err)
	}
	if c.Port == 0 {
		c.Port = 1080
	}
	return &c, nil
}

// Save persists the VPN config to the default path with 0600 perms
// (it contains credentials).
func (c *Config) Save() error {
	p, err := defaultPath()
	if err != nil {
		return err
	}
	return c.SavePath(p)
}

func (c *Config) SavePath(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating vpn config dir: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling vpn config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("writing vpn config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing vpn config: %w", err)
	}
	return nil
}

// PickServer returns the configured server to use. If pin is non-empty,
// it's used directly (and bypasses the pool). Otherwise a random pool
// entry is chosen. Returns error if there's nothing to pick.
func (c *Config) PickServer(pin string) (string, error) {
	if pin = strings.TrimSpace(pin); pin != "" {
		return pin, nil
	}
	if len(c.Pool) == 0 {
		return "", fmt.Errorf("no VPN server pool configured; run: birdy vpn pool add <hostname>")
	}
	return c.Pool[rand.Intn(len(c.Pool))], nil
}

// Validate checks that the config has the minimum fields needed to
// route traffic. Empty server list is OK if caller pins a server, but
// credentials must be set.
func (c *Config) Validate() error {
	if c.User == "" || c.Password == "" {
		return fmt.Errorf("VPN credentials not configured; run: birdy vpn set --user <u> --pass <p>")
	}
	return nil
}

// AddPool appends a hostname to the pool, deduplicating.
func (c *Config) AddPool(hostname string) bool {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return false
	}
	for _, h := range c.Pool {
		if strings.EqualFold(h, hostname) {
			return false
		}
	}
	c.Pool = append(c.Pool, hostname)
	return true
}

// RemovePool drops a hostname from the pool. Returns true if removed.
func (c *Config) RemovePool(hostname string) bool {
	hostname = strings.TrimSpace(hostname)
	for i, h := range c.Pool {
		if strings.EqualFold(h, hostname) {
			c.Pool = append(c.Pool[:i], c.Pool[i+1:]...)
			return true
		}
	}
	return false
}
