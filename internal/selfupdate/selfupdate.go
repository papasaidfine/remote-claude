// Package selfupdate lets the desktop GUI update itself in place: it checks the
// latest GitHub release, downloads the matching asset (directly or, on failure,
// through a temporary local proxy built from the user's vless nodes), atomically
// replaces the running executable, and re-execs the new binary.
//
// The repository is private, so both the release lookup and the asset download
// are authenticated GitHub API calls. See token for how the credential gets in.
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

// token authenticates against the private repository. It is stamped in at build
// time — never committed — with:
//
//	-ldflags "-X github.com/papasaidfine/remote-claude/internal/selfupdate.token=$TOKEN"
//
// A local build leaves it empty, which makes every request anonymous; that is
// fine because a "dev" build never updates itself anyway.
var token string

// apiBase is the GitHub API root. A variable so tests can point it at a stub.
var apiBase = "https://api.github.com"

// Release is the latest GitHub release info.
type Release struct {
	Version   string // tag_name, e.g. "v0.2.0-rc.8"
	HasUpdate bool   // true if Version differs from the current version and current != "dev"
	AssetID   int64  // id of this platform's asset; 0 if the release has none
}

// Check queries the latest release. current is the running version string.
func Check(current string) (Release, error) {
	req, err := newAPIRequest(apiURL("/releases/latest"), "application/vnd.github+json")
	if err != nil {
		return Release{}, err
	}
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
		Assets  []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Release{}, err
	}
	rel := Release{
		Version:   body.TagName,
		HasUpdate: hasUpdate(body.TagName, current),
	}
	// A release missing this platform's asset still counts as an update — the
	// user should be told one exists. Apply is where that turns into an error.
	if name, err := AssetName(); err == nil {
		for _, a := range body.Assets {
			if a.Name == name {
				rel.AssetID = a.ID
				break
			}
		}
	}
	return rel, nil
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

// apiURL builds a repository-scoped API URL, e.g. "/releases/latest".
func apiURL(path string) string { return apiBase + "/repos/" + repo + path }

// newAPIRequest builds an authenticated GET against the GitHub API. With no
// built-in token the request goes out anonymous rather than with an empty
// credential, which reads better in GitHub's logs and error messages.
func newAPIRequest(rawURL, accept string) (*http.Request, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// Apply downloads rel's asset and atomically replaces the running executable.
// It tries a direct download first; on failure/timeout it retries through a
// temporary local proxy built from the user's configured vless nodes (best
// effort — if the proxy or nodes are unavailable, the direct error is returned).
// proxy, if non-empty, is an explicit http proxy URL used for the direct attempt.
func Apply(rel Release, proxy string) error {
	exe, err := selfPath()
	if err != nil {
		return err
	}
	newPath := exe + ".new"

	// 1. Direct download (optionally through an explicit http proxy).
	directErr := downloadAsset(directClient(proxy), rel.AssetID, newPath)
	if directErr != nil {
		// 2. Retry through a local proxy built from the user's nodes.
		if perr := downloadViaProxy(rel.AssetID, newPath); perr != nil {
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

// directClient builds the client for the first download attempt, optionally
// routed through an explicit http proxy URL ("" = direct connection).
func directClient(proxy string) *http.Client {
	tr := &http.Transport{}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Transport: tr, Timeout: 60 * time.Second}
}

// downloadAsset fetches release asset id to dest through client.
//
// The asset endpoint answers a 302 pointing at a pre-signed URL on
// release-assets.githubusercontent.com, which authenticates by query string. We
// read the Location and issue a fresh, unauthenticated GET rather than letting
// the client follow it, for two reasons: our credential never travels to a host
// that is not the API, and correctness does not rest on net/http's redirect
// heuristic. That heuristic compares hostnames only (see
// shouldCopyHeaderOnRedirect), so a redirect that stayed on the same host — or a
// future change of CDN arrangement — would forward the token silently.
//
// GitHub's CDN does currently accept a request that carries the token as well;
// this is credential hygiene, not a workaround for a rejection.
func downloadAsset(client *http.Client, id int64, dest string) error {
	if id == 0 {
		return fmt.Errorf("this release has no build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	req, err := newAPIRequest(apiURL("/releases/assets/"+strconv.FormatInt(id, 10)), "application/octet-stream")
	if err != nil {
		return err
	}
	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noFollow.Do(req)
	if err != nil {
		return err
	}
	if loc := resp.Header.Get("Location"); isRedirect(resp.StatusCode) && loc != "" {
		resp.Body.Close()
		return downloadUnauthenticated(client, loc, dest)
	}
	defer resp.Body.Close()
	return writeBody(resp, dest)
}

func isRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// downloadUnauthenticated GETs a pre-signed URL with no credentials of ours.
func downloadUnauthenticated(client *http.Client, rawURL, dest string) error {
	resp, err := client.Get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return writeBody(resp, dest)
}

// writeBody requires HTTP 200 and a non-empty body, and writes it to dest. dest
// is created/truncated; on any failure it is removed so a partial or empty file
// never survives to be swapped in as the new executable.
func writeBody(resp *http.Response, dest string) error {
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", resp.Request.URL, resp.StatusCode)
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
		return fmt.Errorf("download %s: empty body", resp.Request.URL)
	}
	return nil
}

// downloadViaProxy retries the download through a temporary local xray HTTP
// proxy built from a random configured vless node. Everything it spawns is
// cleaned up before it returns.
func downloadViaProxy(assetID int64, dest string) error {
	p, err := paths.Resolve()
	if err != nil {
		return err
	}
	xrayBin := xray.Resolve(p)
	if xrayBin == "" {
		return fmt.Errorf("proxy components not installed; cannot retry through the proxy")
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
		return fmt.Errorf("failed to start the proxy: %w", err)
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
	return downloadAsset(client, assetID, dest)
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

// waitForPort blocks until the proxy's inbound accepts a connection, up to ~5s.
func waitForPort(cmd *exec.Cmd, port int) error {
	addr := "127.0.0.1:" + strconv.Itoa(port)
	for i := 0; i < 50; i++ {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return fmt.Errorf("the proxy exited before its inbound came up")
		}
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("the proxy did not come up")
}

// Restart re-execs the (now-updated) binary, detached, so the caller can exit.
// extraArgs are appended to the original argument list — the updater uses this
// to pass the flag that makes the incoming process wait for this one's
// single-instance lock instead of deferring to it. Returns once the new process
// has started.
func Restart(extraArgs ...string) error {
	exe, err := selfPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, append(append([]string{}, os.Args[1:]...), extraArgs...)...)
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
