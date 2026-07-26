package core

import (
	"net/url"
	"os/exec"
	"strings"

	"github.com/papasaidfine/remote-claude/internal/sshbin"
	"github.com/papasaidfine/remote-claude/internal/store"
	"github.com/papasaidfine/remote-claude/internal/sysproc"
)

// vscodeURI is the deep link that opens VS Code and connects it to a host over
// Remote-SSH, showing folder home.
//
// It names the ssh *alias*, not an address, so VS Code resolves it through the
// same ~/.ssh/config this app maintains — HostName, User, Port and the proxy
// ProxyCommand all apply without being repeated here.
//
// The folder is not optional. Handed a bare authority VS Code opens an empty
// editor and does nothing else, which is what "just connect to this host" looked
// like when tried. Anything that is not an absolute path falls back to the root:
// a bad path produces a link VS Code cannot resolve, and "/" at least connects.
func vscodeURI(alias, home string) string {
	home = strings.TrimRight(strings.TrimSpace(home), "/")
	if !strings.HasPrefix(home, "/") {
		home = ""
	}
	return "vscode://vscode-remote/ssh-remote+" + url.PathEscape(alias) + "/" + strings.TrimPrefix(home, "/")
}

// OpenVSCode opens VS Code connected to the host over Remote-SSH, at its home
// directory. The first call for a host asks it where that is and remembers the
// answer; later ones are immediate.
func (a *App) OpenVSCode(alias string) error {
	a.mu.Lock()
	exists := a.readConfig().FindHost(alias) != nil
	home := a.meta.Host(alias).RemoteHome
	a.mu.Unlock()
	if !exists {
		return errf(ErrNotFound, "no such host")
	}

	if home == "" {
		resolved, err := a.resolveRemoteHome(alias)
		if err != nil {
			return err
		}
		home = resolved
	}
	if err := a.plat.OpenURL(vscodeURI(alias, home)); err != nil {
		return errf(ErrUnavailable, "could not hand the link to VS Code — is it installed? (%v)", err)
	}
	return nil
}

// resolveRemoteHome asks the host for its home directory and caches it.
func (a *App) resolveRemoteHome(alias string) (string, error) {
	cmd := exec.Command(sshbin.SSH(),
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=20", "-o", "IdentitiesOnly=no",
		alias, "echo $HOME")
	sysproc.Hide(cmd)
	out, err := cmd.Output()
	home := parseRemoteHome(out)
	if home == "" {
		if err != nil {
			return "", errf(ErrRemote, "could not reach %q to find its home directory: %v", alias, err)
		}
		return "", errf(ErrRemote, "%q did not report a home directory", alias)
	}

	a.mu.Lock()
	a.meta.SetRemoteHome(alias, home)
	saveErr := store.Save(a.metaPath, a.meta)
	a.mu.Unlock()
	if saveErr != nil {
		// Not fatal: the link still works, it just gets looked up again next time.
		return home, nil
	}
	return home, nil
}

// parseRemoteHome picks the home directory out of the ssh output. A login shell
// with a chatty profile prints a banner around it, so the last absolute path
// wins rather than the whole of stdout.
func parseRemoteHome(out []byte) string {
	var home string
	for _, line := range strings.Split(string(out), "\n") {
		if l := strings.TrimSpace(line); strings.HasPrefix(l, "/") {
			home = l
		}
	}
	return home
}

// ClaudeCommand is the shell command that starts Claude on a host.
//
// The app hands this over for the user to paste into whatever terminal they
// prefer rather than launching one: there is no portable way to open "the user's
// terminal", and guessing wrongly means either the wrong terminal or, on Linux,
// nothing happening at all.
//
// -t matters. ssh allocates no TTY when it is given a command to run, and Claude
// is interactive — without it the session starts and immediately gives up.
func ClaudeCommand(alias string) string {
	return "ssh " + shellQuote(alias) + " -t claude"
}

// shellQuote wraps a word in single quotes if it needs them.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\"'\\$`&|;<>()*?[]{}!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
