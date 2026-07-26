package core

import "testing"

// VS Code will not act on a bare authority — handed one it opens an empty editor
// and stops there. The link has to name a folder on the host.
func TestVSCodeURICarriesTheRemoteFolder(t *testing.T) {
	got := vscodeURI("prod-gpu", "/home/ubuntu")
	want := "vscode://vscode-remote/ssh-remote+prod-gpu/home/ubuntu"
	if got != want {
		t.Errorf("vscodeURI = %q, want %q", got, want)
	}
}

func TestVSCodeURIEscapesAnAwkwardAlias(t *testing.T) {
	if got := vscodeURI("my host", "/root"); got == "vscode://vscode-remote/ssh-remote+my host/root" {
		t.Errorf("vscodeURI = %q; a space must not go into a URI raw", got)
	}
}

// Anything that is not an absolute path would produce a link VS Code cannot
// resolve, so the root is the safe stand-in.
func TestVSCodeURIFallsBackToTheRootForAnUnusableHome(t *testing.T) {
	for _, home := range []string{"", "~", "relative/path"} {
		got := vscodeURI("h", home)
		if want := "vscode://vscode-remote/ssh-remote+h/"; got != want {
			t.Errorf("vscodeURI with home %q = %q, want %q", home, got, want)
		}
	}
}

func TestVSCodeURIDropsATrailingSlash(t *testing.T) {
	got := vscodeURI("h", "/home/me/")
	if want := "vscode://vscode-remote/ssh-remote+h/home/me"; got != want {
		t.Errorf("vscodeURI = %q, want %q", got, want)
	}
}

// `ssh <alias> echo $HOME` prints the home directory, but a host with a chatty
// profile can print a banner around it. The last non-empty absolute path is the
// answer.
func TestParseRemoteHomeIgnoresBannerNoise(t *testing.T) {
	out := "Welcome to Ubuntu 24.04\n\n/home/ubuntu\n"
	if got := parseRemoteHome([]byte(out)); got != "/home/ubuntu" {
		t.Errorf("parseRemoteHome = %q, want /home/ubuntu", got)
	}
}

func TestParseRemoteHomeRejectsOutputWithNoPath(t *testing.T) {
	if got := parseRemoteHome([]byte("bash: warning: setlocale failed\n")); got != "" {
		t.Errorf("parseRemoteHome = %q, want empty", got)
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
	if got := ClaudeCommand("my host"); got == `ssh my host -t claude` {
		t.Errorf("ClaudeCommand = %q; an alias with a space must be quoted", got)
	}
}
