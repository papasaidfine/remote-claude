package core

import (
	"testing"

	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/store"
)

// recordPlat is a fakePlat that keeps the URLs it was handed.
type recordPlat struct {
	fakePlat
	urls []string
}

func (p *recordPlat) OpenURL(u string) error {
	p.urls = append(p.urls, u)
	return nil
}

// The button opens the editor and stops there. A URL that named a host would be
// the Remote-SSH deep link again, which promised a connection it never made and
// charged an ssh round-trip for the folder in the bargain.
func TestOpenVSCodeOpensTheEditorAndNamesNoHost(t *testing.T) {
	p := &recordPlat{}
	a := New(&store.Config{}, "", paths.Paths{}, &fakeMgr{}, &fakeProv{}, p)

	if err := a.OpenVSCode(); err != nil {
		t.Fatalf("OpenVSCode: %v", err)
	}
	if len(p.urls) != 1 {
		t.Fatalf("opened %d URLs, want 1: %q", len(p.urls), p.urls)
	}
	if p.urls[0] != "vscode://" {
		t.Errorf("opened %q, want a bare vscode:// with no host or folder", p.urls[0])
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
