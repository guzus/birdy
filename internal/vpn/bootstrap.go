package vpn

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed bootstrap.js
var bootstrapJS []byte

// CacheDir is birdy's per-user cache directory. Resolves via the
// platform-idiomatic os.UserCacheDir() — on macOS this is
// $HOME/Library/Caches/birdy, on Linux $HOME/.cache/birdy (honoring
// XDG_CACHE_HOME).
//
// Exported so that cmd/vpn.go's install-deps writes undici to the
// SAME path that BootstrapPath()/UndiciDir() read from — divergence
// here causes a confusing "run install-deps" loop on macOS.
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "birdy"), nil
}

// BootstrapPath writes the embedded bootstrap.js to CacheDir() and
// returns the path. Idempotent: overwrites each call so a binary
// upgrade lands.
//
// Callers point Node at it via `NODE_OPTIONS=--require=<path>`.
func BootstrapPath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}
	p := filepath.Join(dir, "bootstrap.js")
	if err := os.WriteFile(p, bootstrapJS, 0600); err != nil {
		return "", fmt.Errorf("writing bootstrap.js: %w", err)
	}
	return p, nil
}

// UndiciDir returns the absolute path where birdy expects to find
// undici (<CacheDir>/node_modules/undici). The bootstrap.js receives
// this path via BIRDY_UNDICI_PATH so it never has to guess.
func UndiciDir() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "node_modules", "undici"), nil
}

// UndiciInstalled reports whether undici is present at UndiciDir().
func UndiciInstalled() bool {
	p, err := UndiciDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(p, "package.json"))
	return err == nil
}
