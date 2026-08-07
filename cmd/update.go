package cmd

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Self-update, with two rules that shape the whole implementation.
//
// It refuses to touch a package-manager-owned binary. birdy installed by
// Homebrew lives in the Cellar with a symlink in bin/, and overwriting it
// leaves brew's manifest describing a file that is no longer there — brew doctor
// complains, and the next `brew upgrade` silently reverts the update. Deferring
// to the package manager is the only correct answer.
//
// It refuses to install anything it has not verified. The release publishes a
// checksums file; an updater that downloads and executes without checking it is
// a supply-chain hole with a progress bar.

const (
	updateAPILatest = "https://api.github.com/repos/guzus/birdy/releases/latest"
	updateTimeout   = 2 * time.Minute
)

var updateCheckOnly bool

var updateCmd = &cobra.Command{
	Use:     "update",
	Short:   "Update birdy to the latest release",
	GroupID: "birdy",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpdate(cmd.OutOrStdout(), updateCheckOnly)
	},
}

func init() {
	updateCmd.Flags().BoolVar(&updateCheckOnly, "check", false,
		"report whether a newer release exists without installing it")
	rootCmd.AddCommand(updateCmd)
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func runUpdate(out io.Writer, checkOnly bool) error {
	latest, err := fetchLatestRelease()
	if err != nil {
		return err
	}
	newest := strings.TrimPrefix(latest.TagName, "v")

	switch {
	case version == "dev":
		fmt.Fprintf(out, "running a dev build; latest release is %s\n", newest)
	case newest == version:
		fmt.Fprintf(out, "birdy %s is already the latest release\n", version)
		return nil
	default:
		fmt.Fprintf(out, "birdy %s -> %s\n", version, newest)
	}

	if checkOnly {
		fmt.Fprintf(out, "%s\n", latest.HTMLURL)
		return nil
	}

	// Before downloading anything, find out whether we are allowed to replace
	// this binary at all.
	target, manager, err := updateTarget()
	if err != nil {
		return err
	}
	if manager != "" {
		return fmt.Errorf(
			"birdy was installed by %s, which owns this binary.\n\n"+
				"Update it the same way it was installed:\n\n  %s\n\n"+
				"Replacing the file directly would leave %s describing a binary that is "+
				"no longer there, and its next upgrade would silently undo this one",
			manager, updateCommandFor(manager), manager)
	}

	asset, sums := assetFor(latest)
	if asset == "" {
		return fmt.Errorf("no release asset for %s/%s in %s", runtime.GOOS, runtime.GOARCH, latest.TagName)
	}

	fmt.Fprintf(out, "downloading %s\n", filepath.Base(asset))
	archive, err := download(asset)
	if err != nil {
		return err
	}
	defer os.Remove(archive)

	if sums == "" {
		return fmt.Errorf("release %s publishes no checksums file; refusing to install unverified", latest.TagName)
	}
	if err := verifyChecksum(archive, sums, filepath.Base(asset)); err != nil {
		return err
	}
	fmt.Fprintln(out, "checksum verified")

	binary, err := extractBinary(archive)
	if err != nil {
		return err
	}
	defer os.Remove(binary)

	if err := replaceBinary(target, binary); err != nil {
		return err
	}

	fmt.Fprintf(out, "updated %s to %s\n", target, newest)
	return nil
}

// updateTarget resolves the binary to replace, and reports the package manager
// that owns it when one does.
func updateTarget() (path, manager string, err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("locating the running binary: %w", err)
	}
	// Resolve symlinks: Homebrew puts a link in bin/ pointing into the Cellar,
	// and the Cellar copy is the one a naive updater would clobber.
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}

	if isHomebrewPath(resolved) {
		return resolved, "Homebrew", nil
	}
	return resolved, "", nil
}

func isHomebrewPath(path string) bool {
	// Cellar is the reliable marker; a linked binary always resolves into it.
	if strings.Contains(path, string(filepath.Separator)+"Cellar"+string(filepath.Separator)) {
		return true
	}
	for _, prefix := range []string{"/opt/homebrew", "/usr/local/Homebrew", "/home/linuxbrew/.linuxbrew"} {
		if strings.HasPrefix(path, prefix+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func updateCommandFor(manager string) string {
	if manager == "Homebrew" {
		return "brew upgrade birdy"
	}
	return "your package manager's upgrade command"
}

func fetchLatestRelease() (*ghRelease, error) {
	client := &http.Client{Timeout: updateTimeout}
	req, err := http.NewRequest(http.MethodGet, updateAPILatest, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("checking for updates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("checking for updates: GitHub returned HTTP %d", resp.StatusCode)
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("no published release found")
	}
	return &release, nil
}

// assetFor picks the archive for this platform and the checksums file beside it.
func assetFor(r *ghRelease) (asset, checksums string) {
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	want := fmt.Sprintf("birdy_%s_%s%s", runtime.GOOS, runtime.GOARCH, ext)

	for _, a := range r.Assets {
		switch {
		case a.Name == want:
			asset = a.URL
		case strings.HasSuffix(a.Name, "checksums.txt"):
			checksums = a.URL
		}
	}
	return asset, checksums
}

func download(url string) (string, error) {
	client := &http.Client{Timeout: updateTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("downloading: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading: HTTP %d", resp.StatusCode)
	}

	f, err := os.CreateTemp("", "birdy-update-*")
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("downloading: %w", err)
	}
	return f.Name(), nil
}

// verifyChecksum fails closed: an unreadable checksums file, a missing entry, or
// a mismatch all abort the update.
func verifyChecksum(archivePath, checksumsURL, assetName string) error {
	sums, err := download(checksumsURL)
	if err != nil {
		return fmt.Errorf("fetching checksums: %w", err)
	}
	defer os.Remove(sums)
	return verifyChecksumFile(archivePath, sums, assetName)
}

// verifyChecksumFile is the half that does not touch the network, so the
// failure paths that matter — a mismatch, an asset with no published sum — are
// testable without one.
func verifyChecksumFile(archivePath, sumsPath, assetName string) error {
	body, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}

	var want string
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("no checksum published for %s; refusing to install unverified", assetName)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("checksum mismatch for %s:\n  expected %s\n  got      %s", assetName, want, got)
	}
	return nil
}

// extractBinary pulls just the birdy executable out of the archive.
func extractBinary(archivePath string) (string, error) {
	if runtime.GOOS == "windows" {
		return extractFromZip(archivePath)
	}
	return extractFromTarGz(archivePath)
}

func extractFromTarGz(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("reading archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading archive: %w", err)
		}
		if filepath.Base(hdr.Name) != "birdy" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		return writeTemp(tr)
	}
	return "", fmt.Errorf("no birdy binary inside the archive")
}

func extractFromZip(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("reading archive: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if filepath.Base(f.Name) != "birdy.exe" && filepath.Base(f.Name) != "birdy" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		return writeTemp(rc)
	}
	return "", fmt.Errorf("no birdy binary inside the archive")
}

func writeTemp(r io.Reader) (string, error) {
	out, err := os.CreateTemp("", "birdy-bin-*")
	if err != nil {
		return "", err
	}
	defer out.Close()

	if _, err := io.Copy(out, r); err != nil {
		os.Remove(out.Name())
		return "", err
	}
	if err := out.Chmod(0o755); err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// replaceBinary swaps the new binary in atomically.
//
// The staging copy is written beside the target rather than in TMPDIR, because
// os.Rename cannot cross filesystems and /tmp frequently is one. On Windows a
// running executable cannot be overwritten, so the old one is moved aside first
// and cleaned up on the next run.
func replaceBinary(target, newBinary string) error {
	dir := filepath.Dir(target)
	staged := filepath.Join(dir, ".birdy-update-"+strconv.Itoa(os.Getpid()))

	if err := copyFile(newBinary, staged, 0o755); err != nil {
		return fmt.Errorf("staging the new binary in %s: %w (is it writable?)", dir, err)
	}

	if runtime.GOOS == "windows" {
		old := target + ".old"
		os.Remove(old)
		if err := os.Rename(target, old); err != nil {
			os.Remove(staged)
			return fmt.Errorf("moving the running binary aside: %w", err)
		}
	}

	if err := os.Rename(staged, target); err != nil {
		os.Remove(staged)
		return fmt.Errorf("replacing %s: %w", target, err)
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Chmod(mode)
}
