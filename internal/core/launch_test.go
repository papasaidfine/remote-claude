package core

import "testing"

func TestVSCodeURITargetsTheHostsSSHAlias(t *testing.T) {
	// The alias is what ~/.ssh/config resolves, so VS Code's Remote-SSH picks up
	// the same HostName, User, Port and ProxyCommand the app manages.
	if got, want := vscodeURI("prod-gpu"), "vscode://vscode-remote/ssh-remote+prod-gpu"; got != want {
		t.Errorf("vscodeURI = %q, want %q", got, want)
	}
}

func TestVSCodeURIEscapesAnAwkwardAlias(t *testing.T) {
	got := vscodeURI("my host")
	if got == "vscode://vscode-remote/ssh-remote+my host" {
		t.Errorf("vscodeURI = %q; a space must not go into a URI raw", got)
	}
}

func TestClaudeCommandForcesATTY(t *testing.T) {
	// claude is interactive, and ssh allocates no TTY when it is given a command
	// to run — without -t it starts and immediately gives up.
	if got, want := ClaudeCommand("prod-gpu"), `ssh prod-gpu -t claude`; got != want {
		t.Errorf("ClaudeCommand = %q, want %q", got, want)
	}
}

func TestClaudeCommandQuotesAnAwkwardAlias(t *testing.T) {
	got := ClaudeCommand("my host")
	if got == `ssh my host -t claude` {
		t.Errorf("ClaudeCommand = %q; an alias with a space must be quoted", got)
	}
}
