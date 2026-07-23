// Package xray resolves, installs and version-checks the xray-core binary used
// by the optional proxy path.
package xray

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/sysproc"
	"github.com/papasaidfine/remote-claude/internal/ui"
)

// Client performs the GitHub downloads, optionally through a proxy.
type Client struct{ http *http.Client }

// New builds a Client; proxy is an optional http proxy URL ("" = direct).
func New(proxy string) *Client {
	tr := &http.Transport{}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &Client{http: &http.Client{Transport: tr, Timeout: 60 * time.Second}}
}

// Resolve returns the path to a usable xray binary, or "" when none is found.
func Resolve(p paths.Paths) string {
	if fi, err := os.Stat(p.XrayBin); err == nil && !fi.IsDir() {
		return p.XrayBin
	}
	name := "xray"
	if runtime.GOOS == "windows" {
		name = "xray.exe"
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return ""
}

func assetName() (string, error) {
	var base string
	switch runtime.GOOS {
	case "windows":
		base = "windows"
	case "darwin":
		base = "macos"
	case "linux":
		base = "linux"
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	var suffix string
	switch runtime.GOARCH {
	case "amd64":
		suffix = "64"
	case "arm64":
		suffix = "arm64-v8a"
	default:
		return "", fmt.Errorf("unsupported arch: %s", runtime.GOARCH)
	}
	return fmt.Sprintf("Xray-%s-%s.zip", base, suffix), nil
}

// Install downloads the latest release binary into the vendor path.
func (c *Client) Install(p paths.Paths) error {
	asset, err := assetName()
	if err != nil {
		return err
	}
	binDir := filepath.Dir(p.XrayBin)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	ui.Log("Downloading %s (github.com/XTLS/Xray-core)", asset)
	url := "https://github.com/XTLS/Xray-core/releases/latest/download/" + asset
	resp, err := c.http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", asset, resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "xray-dl-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	member := filepath.Base(p.XrayBin) // xray or xray.exe
	if err := extractMember(tmp.Name(), member, p.XrayBin); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(p.XrayBin, 0o755); err != nil {
			return err
		}
	}
	ui.Log("xray installed to %s", p.XrayBin)
	return nil
}

// extractMember copies the zip entry whose base name is member into dest.
func extractMember(zipPath, member, dest string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if filepath.Base(f.Name) != member {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, rc)
		return err
	}
	return fmt.Errorf("%s not found in the downloaded archive", member)
}

// LocalVersion returns the version string of bin (e.g. 25.0.0), or "".
func LocalVersion(bin string) string {
	cmd := exec.Command(bin, "version")
	sysproc.Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	fields := strings.Fields(first)
	if len(fields) >= 2 && fields[0] == "Xray" {
		return fields[1]
	}
	return ""
}

// LatestVersion returns the latest release tag with a leading v stripped, or "".
func (c *Client) LatestVersion() string {
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/XTLS/Xray-core/releases/latest", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	return strings.TrimPrefix(body.TagName, "v")
}

// Update version-checks the resolved binary and refreshes the vendor copy when
// it is stale; external binaries are left for the user's package manager.
func (c *Client) Update(p paths.Paths) error {
	bin := Resolve(p)
	if bin == "" {
		return c.Install(p)
	}
	cur := LocalVersion(bin)
	if cur == "" {
		cur = "unknown"
	}
	latest := c.LatestVersion()
	switch {
	case latest == "":
		ui.Warn("Could not check the latest xray version (GitHub unreachable); keeping %s", cur)
	case cur == latest:
		ui.Log("xray %s is up to date", cur)
	case bin == p.XrayBin:
		ui.Log("Updating xray %s -> %s", cur, latest)
		return c.Install(p)
	default:
		ui.Warn("xray at %s is %s (latest: %s) — it was installed outside this", bin, cur, latest)
		ui.Warn("script; update it with its own package manager.")
	}
	return nil
}
