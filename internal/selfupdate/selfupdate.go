// Package selfupdate lets the desktop GUI update itself in place: it checks the
// latest GitHub release, downloads the matching asset (directly or, on failure,
// through a temporary local xray HTTP proxy built from the user's vless nodes),
// atomically replaces the running executable, and re-execs the new binary.
package selfupdate

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/papasaidfine/remote-claude/internal/nodes"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/sysproc"
	"github.com/papasaidfine/remote-claude/internal/vless"
	"github.com/papasaidfine/remote-claude/internal/xray"
)

const (
	// repo is the GitHub owner/name that publishes GUI releases.
	repo = "papasaidfine/remote-claude"
	// assetPrefix is the leading part of every published GUI asset filename.
	assetPrefix = "remote-claude-gui_"
)

// Release is the latest GitHub release info.
type Release struct {
	Version   string // tag_name, e.g. "v0.2.0-rc.8"
	HasUpdate bool   // true if Version differs from the current version and current != "dev"
}

// Check queries the latest release. current is the running version string.
func Check(current string) (Release, error) {
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github releases API: HTTP %d", resp.StatusCode)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Release{}, err
	}
	return Release{
		Version:   body.TagName,
		HasUpdate: hasUpdate(body.TagName, current),
	}, nil
}

// hasUpdate reports whether tag represents a newer release than current. It is
// conservative: "dev" builds and an empty/unknown tag never trigger an update.
func hasUpdate(tag, current string) bool {
	return tag != "" && current != "dev" && tag != current
}

// AssetName returns the release asset filename for the current GOOS/GOARCH,
// e.g. "remote-claude-gui_windows_amd64.exe". Errors if this platform has no GUI build.
func AssetName() (string, error) {
	return assetNameFor(runtime.GOOS, runtime.GOARCH)
}

// assetNameFor maps a GOOS/GOARCH pair to its published asset filename. It is
// the pure core of AssetName, split out so it can be unit-tested off-platform.
func assetNameFor(goos, goarch string) (string, error) {
	var suffix string
	switch {
	case goos == "linux" && goarch == "amd64":
		suffix = "linux_amd64"
	case goos == "darwin" && goarch == "arm64":
		suffix = "darwin_arm64"
	case goos == "windows" && goarch == "amd64":
		suffix = "windows_amd64"
	default:
		return "", fmt.Errorf("no GUI build published for %s/%s", goos, goarch)
	}
	name := assetPrefix + suffix
	if goos == "windows" {
		name += ".exe"
	}
	return name, nil
}

// Apply downloads the latest release asset and atomically replaces the running
// executable. It tries a direct download first; on failure/timeout it retries
// through a temporary local xray HTTP proxy built from the user's configured
// vless nodes (best effort — if xray/nodes are unavailable, the direct error is returned).
// proxy, if non-empty, is an explicit http proxy URL tried before xray.
func Apply(proxy string) error {
	exe, err := selfPath()
	if err != nil {
		return err
	}
	asset, err := AssetName()
	if err != nil {
		return err
	}
	srcURL := "https://github.com/" + repo + "/releases/latest/download/" + asset
	newPath := exe + ".new"

	// 1. Direct download (optionally through an explicit http proxy).
	directErr := downloadDirect(srcURL, newPath, proxy)
	if directErr != nil {
		// 2. Retry through a local xray proxy built from the user's nodes.
		if xerr := downloadViaXray(srcURL, newPath); xerr != nil {
			// Best effort: surface the original direct error, not the fallback's.
			return directErr
		}
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(newPath, 0o755); err != nil {
			os.Remove(newPath)
			return err
		}
	}
	return replaceExecutable(exe, newPath)
}

// selfPath returns the absolute, symlink-resolved path of the running binary.
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// replaceExecutable installs newPath over exe. On Unix a plain rename replaces
// the running binary; on Windows the running .exe cannot be overwritten but can
// be renamed aside first, with rollback if the swap-in fails.
func replaceExecutable(exe, newPath string) error {
	if runtime.GOOS == "windows" {
		oldPath := exe + ".old"
		os.Remove(oldPath) // clear any stale copy from a previous update
		if err := os.Rename(exe, oldPath); err != nil {
			os.Remove(newPath)
			return fmt.Errorf("could not move running exe aside: %w", err)
		}
		if err := os.Rename(newPath, exe); err != nil {
			os.Rename(oldPath, exe) // roll back
			os.Remove(newPath)
			return fmt.Errorf("could not install new exe: %w", err)
		}
		return nil
	}
	if err := os.Rename(newPath, exe); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("could not install new exe: %w", err)
	}
	return nil
}

// downloadDirect fetches srcURL to dest, optionally through an explicit http
// proxy URL ("" = direct connection).
func downloadDirect(srcURL, dest, proxy string) error {
	tr := &http.Transport{}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	client := &http.Client{Transport: tr, Timeout: 60 * time.Second}
	return downloadWith(client, srcURL, dest)
}

// downloadWith GETs srcURL with client and writes the body to dest, requiring
// HTTP 200 and a non-empty body. dest is created/truncated; on any failure it
// is removed so a partial file never survives.
func downloadWith(client *http.Client, srcURL, dest string) error {
	resp, err := client.Get(srcURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", srcURL, resp.StatusCode)
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, resp.Body)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dest)
		return err
	}
	if n == 0 {
		os.Remove(dest)
		return fmt.Errorf("download %s: empty body", srcURL)
	}
	return nil
}

// downloadViaXray retries the download through a temporary local xray HTTP proxy
// built from a random configured vless node. Everything it spawns is cleaned up
// before it returns.
func downloadViaXray(srcURL, dest string) error {
	p, err := paths.Resolve()
	if err != nil {
		return err
	}
	xrayBin := xray.Resolve(p)
	if xrayBin == "" {
		return fmt.Errorf("xray binary not found; cannot retry through proxy")
	}
	node, err := nodes.PickRandom(p.VlessNodes)
	if err != nil {
		return err
	}
	port, err := reservePort()
	if err != nil {
		return err
	}
	cfgJSON, err := vless.ProxyJSON(node, port)
	if err != nil {
		return err
	}

	cfgPath := filepath.Join(os.TempDir(), fmt.Sprintf("rc-gui-update-%d.json", os.Getpid()))
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		return err
	}
	defer os.Remove(cfgPath)

	cmd := exec.Command(xrayBin, "run", "-c", cfgPath)
	cmd.Stdin = nil
	cmd.Stdout = nil // xray logs to stderr per its config
	cmd.Stderr = os.Stderr
	sysproc.Hide(cmd) // no console window on Windows
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start xray: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	}()

	if err := waitForPort(cmd, port); err != nil {
		return err
	}

	proxyURL, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		return err
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   60 * time.Second,
	}
	return downloadWith(client, srcURL, dest)
}

// reservePort grabs a free loopback TCP port for the xray inbound. Copied from
// internal/relay so this package stays self-contained.
func reservePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

// waitForPort blocks until xray's inbound accepts a connection, up to ~5s.
func waitForPort(cmd *exec.Cmd, port int) error {
	addr := "127.0.0.1:" + strconv.Itoa(port)
	for i := 0; i < 50; i++ {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return fmt.Errorf("xray exited before its inbound came up")
		}
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("xray proxy did not come up")
}

// Restart re-execs the (now-updated) binary with the same args, detached, so the
// caller can exit. Returns after the new process is started.
func Restart() error {
	exe, err := selfPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.Dir, _ = os.Getwd()
	sysproc.Hide(cmd) // no console window on Windows
	if err := cmd.Start(); err != nil {
		return err
	}
	// Detach: release the child so it keeps running after the caller exits.
	return cmd.Process.Release()
}

// CleanupOldBinary removes the "<exe>.old" left behind by a Windows self-replace.
// Call once at startup. Best effort; ignores errors.
func CleanupOldBinary() {
	exe, err := selfPath()
	if err != nil {
		return
	}
	os.Remove(exe + ".old")
}
