package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/guzus/birdy/internal/processenv"
	"github.com/guzus/birdy/internal/vpn"
	"github.com/spf13/cobra"
)

var vpnCmd = &cobra.Command{
	Use:     "vpn",
	Short:   "Configure and test SOCKS5 VPN routing (NordVPN service credentials)",
	GroupID: "birdy",
	Long: `Routes bird's HTTPS traffic through a remote SOCKS5 endpoint so
the egress IP differs from this machine's. Useful for working around
Cloudflare bot blocks that key on IP reputation (see docs).

Configuration lives at ~/.config/birdy/vpn.json (perms 0600) and is
distinct from accounts.json.

birdy dials SOCKS5 directly, so --vpn needs no setup beyond 'vpn set' and a
server pool. 'birdy vpn install-deps' is only for the --bird path: Node's
fetch honours neither SOCKS5 nor the proxy environment variables, so routing
it needs a local bridge and a userspace undici.`,
}

var (
	vpnSetUser string
	vpnSetPass string
	vpnSetPort int
)

var vpnSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set SOCKS5 credentials (NordVPN service username/password)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := vpn.Load()
		if err != nil {
			return err
		}
		changed := false
		if vpnSetUser != "" {
			cfg.User = vpnSetUser
			changed = true
		}
		if vpnSetPass != "" {
			cfg.Password = vpnSetPass
			changed = true
		}
		if vpnSetPort != 0 {
			cfg.Port = vpnSetPort
			changed = true
		}
		if !changed {
			return fmt.Errorf("at least one of --user / --pass / --port is required")
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "✓ VPN config saved (~/.config/birdy/vpn.json)")
		return nil
	},
}

var vpnPoolCmd = &cobra.Command{
	Use:   "pool",
	Short: "Manage the SOCKS5 server pool",
}

var vpnPoolAddCmd = &cobra.Command{
	Use:   "add <hostname> [hostname ...]",
	Short: "Add server(s) to the pool (e.g. us9876.nordvpn.com)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := vpn.Load()
		if err != nil {
			return err
		}
		added := 0
		for _, h := range args {
			if cfg.AddPool(h) {
				added++
				fmt.Fprintf(cmd.OutOrStdout(), "  + %s\n", h)
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "  - %s (already present or empty)\n", h)
			}
		}
		if added == 0 {
			return nil
		}
		return cfg.Save()
	},
}

var vpnPoolRemoveCmd = &cobra.Command{
	Use:   "remove <hostname>",
	Short: "Remove a server from the pool",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := vpn.Load()
		if err != nil {
			return err
		}
		if !cfg.RemovePool(args[0]) {
			return fmt.Errorf("server %q not in pool", args[0])
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "✓ removed %s\n", args[0])
		return nil
	},
}

var vpnPoolListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured servers",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := vpn.Load()
		if err != nil {
			return err
		}
		if len(cfg.Pool) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "(pool empty — add servers with: birdy vpn pool add <hostname>)")
			return nil
		}
		for _, h := range cfg.Pool {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", h)
		}
		return nil
	},
}

var vpnStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show VPN config (password masked)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := vpn.Load()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "User:     %s\n", redact(cfg.User))
		fmt.Fprintf(out, "Password: %s\n", maskPassword(cfg.Password))
		port := cfg.Port
		if port == 0 {
			port = 1080
		}
		fmt.Fprintf(out, "Port:     %d\n", port)
		fmt.Fprintf(out, "Pool:     %d server(s)\n", len(cfg.Pool))
		for _, h := range cfg.Pool {
			fmt.Fprintf(out, "  - %s\n", h)
		}
		undiciStatus := "missing — run: birdy vpn install-deps"
		if vpn.UndiciInstalled() {
			undiciStatus = "installed"
		}
		fmt.Fprintf(out, "Undici:   %s\n", undiciStatus)
		return nil
	},
}

var vpnTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Connect through the VPN and report the egress IP",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := vpn.Load()
		if err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		server, err := cfg.PickServer(vpnServerFlag)
		if err != nil {
			return err
		}
		bridge, err := vpn.Start(server, cfg.Port, cfg.User, cfg.Password)
		if err != nil {
			return err
		}
		defer bridge.Stop()

		// Build an http.Client that points at our local bridge.
		proxyURL, perr := url.Parse("http://" + bridge.Addr())
		if perr != nil {
			return perr
		}
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		client := &http.Client{Transport: transport, Timeout: 15 * time.Second}

		ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://ifconfig.me/ip", nil)
		req.Header.Set("User-Agent", "birdy-vpn-test/1.0")

		fmt.Fprintf(cmd.OutOrStdout(), "→ routing through %s:%d ...\n", server, cfg.Port)
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("vpn test request failed: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(cmd.OutOrStdout(), "✓ HTTP %d, egress IP: %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
		return nil
	},
}

// undiciVersion pins the undici version birdy installs. Bumping this is
// a deliberate maintainer action; --latest would be a supply-chain
// vector since the install runs unattended.
const undiciVersion = "undici@7"

var vpnInstallDepsCmd = &cobra.Command{
	Use:   "install-deps",
	Short: "Install Node 'undici' into birdy's cache dir (required for VPN routing)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cacheDir, err := vpn.CacheDir()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(cacheDir, 0700); err != nil {
			return err
		}
		// Write a minimal package.json so npm installs into <cacheDir>/node_modules.
		pkgPath := cacheDir + "/package.json"
		if _, err := os.Stat(pkgPath); err != nil {
			pkg := map[string]any{
				"name":        "birdy-vpn-deps",
				"version":     "0.0.0",
				"description": "Holds undici for birdy's --vpn bootstrap.",
				"private":     true,
			}
			b, _ := json.MarshalIndent(pkg, "", "  ")
			if err := os.WriteFile(pkgPath, b, 0600); err != nil {
				return err
			}
		}

		fmt.Fprintf(cmd.OutOrStdout(), "→ installing %s into %s ...\n", undiciVersion, cacheDir)
		npm := exec.Command("npm", "install", "--silent", "--no-fund", undiciVersion)
		npm.Env = processenv.Without(os.Environ(), "OPENCODE_API_KEY")
		npm.Dir = cacheDir
		npm.Stdout = cmd.OutOrStdout()
		npm.Stderr = cmd.ErrOrStderr()
		if err := npm.Run(); err != nil {
			return fmt.Errorf("npm install failed: %w (is npm on PATH?)", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "✓ undici installed")
		return nil
	},
}

func init() {
	vpnSetCmd.Flags().StringVar(&vpnSetUser, "user", "", "SOCKS5 username (NordVPN service username)")
	vpnSetCmd.Flags().StringVar(&vpnSetPass, "pass", "", "SOCKS5 password (NordVPN service password)")
	vpnSetCmd.Flags().IntVar(&vpnSetPort, "port", 0, "SOCKS5 port (default 1080)")

	vpnPoolCmd.AddCommand(vpnPoolAddCmd, vpnPoolRemoveCmd, vpnPoolListCmd)
	vpnCmd.AddCommand(vpnSetCmd, vpnPoolCmd, vpnStatusCmd, vpnTestCmd, vpnInstallDepsCmd)
	rootCmd.AddCommand(vpnCmd)
}

func redact(s string) string {
	if s == "" {
		return "(not set)"
	}
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "***" + s[len(s)-2:]
}

func maskPassword(s string) string {
	if s == "" {
		return "(not set)"
	}
	return "****"
}
