package provision

import (
	"strings"
	"testing"

	"github.com/papasaidfine/remote-claude/internal/platform"
	"github.com/papasaidfine/remote-claude/internal/store"
)

func TestRenderServerScript(t *testing.T) {
	s := renderServerScript(serverInput{
		Alias:       "lisa-laptop",
		ReversePort: 2222,
		LocalUser:   "dev",
		LocalPubKey: "ssh-ed25519 AAAAC3xyz dev@host",
	})
	for _, want := range []string{
		"ALIAS='lisa-laptop'",
		"REVERSE_PORT='2222'",
		"LOCAL_USER='dev'",
		"LOCAL_PUBKEY='ssh-ed25519 AAAAC3xyz dev@host'",
		"<<<RC_PUBKEY_BEGIN>>>", // from the embedded body
		"Host %s",               // block writer from the embedded body
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered script missing %q", want)
		}
	}
}

func TestShquoteEscapesQuotes(t *testing.T) {
	got := shquote("a'b")
	if got != `'a'\''b'` {
		t.Errorf("shquote = %q", got)
	}
}

func TestExtractMarked(t *testing.T) {
	out := "noise\n" + pubBegin + "\nssh-ed25519 AAAAkey user@host\n" + pubEnd + "\nmore noise"
	got := extractMarked(out, pubBegin, pubEnd)
	if got != "ssh-ed25519 AAAAkey user@host" {
		t.Errorf("extractMarked = %q", got)
	}
	if extractMarked("no markers here", pubBegin, pubEnd) != "" {
		t.Error("expected empty for missing markers")
	}
}

func TestServerBootstrapRequiresAlias(t *testing.T) {
	c := New(testPaths(t), platform.New())
	h := store.Host{Name: "wb", HostName: "h", User: "u", Port: 22, ReversePort: 2222}
	_, err := c.ServerBootstrap(h, "")
	if err == nil {
		t.Fatal("expected error when client alias is empty")
	}
}
