// Package sshconfig reads and writes the managed "Host remote-claude" block in
// ~/.ssh/config. The pure functions (Render/Replace/Value/Rport/HasBlock) are
// string transforms; WriteFile orchestrates read → confirm → backup → write.
package sshconfig

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/papasaidfine/remote-claude/internal/paths"
)

// BlockOpts describes the managed block to render.
type BlockOpts struct {
	Host       string
	User       string
	Port       int
	RevPort    int    // 0 → omit RemoteForward + ExitOnForwardFailure
	Proxy      string // "" → omit ProxyCommand; else the value after "ProxyCommand "
	ClientName string // "" → omit SetEnv; else SetEnv LC_CLIENT_NAME=<name>
}

// Render returns the managed block (markers included, LF-terminated lines) for
// the given options. Mirrors the bash write_ssh_config_block body exactly.
func Render(o BlockOpts) string {
	var b strings.Builder
	w := func(s string) { b.WriteString(s); b.WriteByte('\n') }
	w(paths.BeginMark)
	w("Host " + paths.Alias)
	w("    HostName " + o.Host)
	w("    User " + o.User)
	w("    Port " + strconv.Itoa(o.Port))
	w("    IdentityFile ~/.ssh/" + paths.KeyName)
	w("    IdentitiesOnly yes")
	if o.ClientName != "" {
		// Passes this machine's name to the server; the server's reverse Host
		// block is named after it. LC_* is forwarded by ssh's default SendEnv.
		w("    SetEnv LC_CLIENT_NAME=" + o.ClientName)
	}
	if o.Proxy != "" {
		w("    ProxyCommand " + o.Proxy)
	}
	if o.RevPort > 0 {
		w(fmt.Sprintf("    RemoteForward 127.0.0.1:%d 127.0.0.1:22", o.RevPort))
		w("    ExitOnForwardFailure yes")
	}
	w("    ServerAliveInterval 30")
	w("    ServerAliveCountMax 3")
	w("    ForwardAgent no")
	b.WriteString(paths.EndMark)
	return b.String()
}

// lines splits content into lines with any trailing CR stripped, so parsing is
// insensitive to CRLF vs LF.
func lines(content string) []string {
	out := strings.Split(content, "\n")
	for i := range out {
		out[i] = strings.TrimRight(out[i], "\r")
	}
	return out
}

// HasBlock reports whether content already contains the managed block.
func HasBlock(content string) bool {
	for _, l := range lines(content) {
		if l == paths.BeginMark {
			return true
		}
	}
	return false
}

// stripBlock removes the managed block (markers inclusive) from content.
func stripBlock(content string) string {
	var kept []string
	skip := false
	for _, l := range lines(content) {
		switch l {
		case paths.BeginMark:
			skip = true
			continue
		case paths.EndMark:
			skip = false
			continue
		}
		if !skip {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// Value returns the value of key inside the managed block (first match), or "".
func Value(content, key string) string {
	inBlk := false
	for _, l := range lines(content) {
		switch l {
		case paths.BeginMark:
			inBlk = true
			continue
		case paths.EndMark:
			inBlk = false
			continue
		}
		if !inBlk {
			continue
		}
		fields := strings.Fields(l)
		if len(fields) >= 2 && fields[0] == key {
			return fields[1]
		}
	}
	return ""
}

// Rport returns the reverse port from the block's RemoteForward, or "".
func Rport(content string) string {
	rf := Value(content, "RemoteForward")
	if rf == "" {
		return ""
	}
	i := strings.LastIndex(rf, ":")
	if i < 0 {
		return rf
	}
	return rf[i+1:]
}

// ProxyOn reports whether the managed block carries a ProxyCommand line.
func ProxyOn(content string) bool {
	inBlk := false
	for _, l := range lines(content) {
		switch strings.TrimSpace(l) {
		case paths.BeginMark:
			inBlk = true
			continue
		case paths.EndMark:
			inBlk = false
			continue
		}
		if inBlk && strings.HasPrefix(strings.TrimSpace(l), "ProxyCommand ") {
			return true
		}
	}
	return false
}

var unmanagedHostRe = regexp.MustCompile(`(?m)^\s*Host\s+.*\b` + regexp.QuoteMeta(paths.Alias) + `\b`)

// HasUnmanagedHost reports whether content has a "Host …remote-claude…" line
// that lives outside the managed block (a conflict, since ssh is first-match).
func HasUnmanagedHost(content string) bool {
	return !HasBlock(content) && unmanagedHostRe.MatchString(content)
}

// Deps are the side-effecting hooks WriteFile needs, injected so the core stays
// testable.
type Deps struct {
	Confirm  func(prompt string, defYes bool) bool // interactive yes/no
	SetPerms func(path string) error               // strict perms (chmod / icacls)
	Backup   func(path string) (string, error)     // copy → returns backup path
	Force    bool                                  // skip the "update it?" confirm
}

// WriteFile writes the managed block into cfgPath, replacing any existing
// managed block. Returns false (no error) when the user declines an update.
func WriteFile(cfgPath string, o BlockOpts, d Deps) (bool, error) {
	raw, _ := os.ReadFile(cfgPath)
	content := string(raw)

	if HasBlock(content) {
		if !d.Force && d.Confirm != nil &&
			!d.Confirm("~/.ssh/config already contains a "+paths.Alias+" block, update it", true) {
			return false, nil
		}
	} else if HasUnmanagedHost(content) && d.Confirm != nil {
		if !d.Confirm("Write the block anyway (cleaning up the old block manually is recommended)", false) {
			return false, fmt.Errorf("aborted; please remove the old Host %s block and re-run", paths.Alias)
		}
	}

	if content != "" && d.Backup != nil {
		if _, err := d.Backup(cfgPath); err != nil {
			return false, err
		}
	}

	body := strings.TrimRight(stripBlock(content), "\n")
	if body != "" {
		body += "\n\n"
	}
	body += Render(o) + "\n"

	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		return false, err
	}
	if d.SetPerms != nil {
		if err := d.SetPerms(cfgPath); err != nil {
			return false, err
		}
	}
	return true, nil
}
