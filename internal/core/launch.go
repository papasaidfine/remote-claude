package core

import (
	"net/url"
	"strings"
)

// vscodeURI is the deep link that opens VS Code and connects it to a host over
// Remote-SSH.
//
// It names the ssh *alias*, not an address, so VS Code resolves it through the
// same ~/.ssh/config this app maintains — HostName, User, Port and the proxy
// ProxyCommand all apply without being repeated here.
//
// No path is appended: the intent is "connect", not "open this project". VS Code
// documents its command line only with a folder (code --remote ssh-remote+host
// /some/path), so a pathless link is the one part of this that could not be
// verified against the docs.
func vscodeURI(alias string) string {
	return "vscode://vscode-remote/ssh-remote+" + url.PathEscape(alias)
}

// OpenVSCode opens VS Code connected to the host over Remote-SSH.
func (a *App) OpenVSCode(alias string) error {
	a.mu.Lock()
	exists := a.readConfig().FindHost(alias) != nil
	a.mu.Unlock()
	if !exists {
		return errf(ErrNotFound, "no such host")
	}
	if err := a.plat.OpenURL(vscodeURI(alias)); err != nil {
		return errf(ErrUnavailable, "could not hand the link to VS Code — is it installed? (%v)", err)
	}
	return nil
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
