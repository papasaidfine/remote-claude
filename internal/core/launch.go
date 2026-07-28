package core

import "strings"

// vscodeURL opens VS Code. That is the whole of it — no host, no folder.
//
// This used to be a vscode://vscode-remote/ssh-remote+<alias>/<home> deep link
// meant to land the editor on the host over Remote-SSH, which never once
// connected: VS Code answers the link by opening a window and stopping there,
// and finding the folder to name cost an ssh round-trip per host on top. The one
// thing the link does reliably is bring the editor up, so that is all the button
// claims now — connecting is left to VS Code's own Remote-SSH picker, which
// reads the same ~/.ssh/config this app maintains and so already lists the hosts.
const vscodeURL = "vscode://"

// OpenVSCode brings up VS Code on this machine.
func (a *App) OpenVSCode() error {
	if err := a.plat.OpenURL(vscodeURL); err != nil {
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
