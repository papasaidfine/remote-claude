package sshconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderBasics(t *testing.T) {
	got := Render(BlockOpts{Host: "srv.example", User: "dev", Port: 22})
	for _, want := range []string{
		"# >>> remote-claude (managed by reverse-ssh-bootstrap) >>>",
		"Host remote-claude",
		"    HostName srv.example",
		"    User dev",
		"    Port 22",
		"    IdentityFile ~/.ssh/id_ed25519",
		"    IdentitiesOnly yes",
		"    ForwardAgent no",
		"# <<< remote-claude <<<",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered block missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "RemoteForward") {
		t.Error("no reverse port requested but RemoteForward present")
	}
	if strings.Contains(got, "ProxyCommand") {
		t.Error("no proxy requested but ProxyCommand present")
	}
}

func TestRenderRevPortAndProxy(t *testing.T) {
	got := Render(BlockOpts{Host: "h", User: "u", Port: 2200, RevPort: 2222, Proxy: `"/x/rc" relay %h %p`})
	if !strings.Contains(got, "    RemoteForward 127.0.0.1:2222 127.0.0.1:22") {
		t.Errorf("missing RemoteForward line:\n%s", got)
	}
	if !strings.Contains(got, "    ExitOnForwardFailure yes") {
		t.Errorf("missing ExitOnForwardFailure:\n%s", got)
	}
	if !strings.Contains(got, `    ProxyCommand "/x/rc" relay %h %p`) {
		t.Errorf("missing ProxyCommand:\n%s", got)
	}
}

func TestValueAndRport(t *testing.T) {
	c := Render(BlockOpts{Host: "srv", User: "dev", Port: 22, RevPort: 9001})
	if v := Value(c, "HostName"); v != "srv" {
		t.Errorf("HostName = %q, want srv", v)
	}
	if v := Value(c, "User"); v != "dev" {
		t.Errorf("User = %q, want dev", v)
	}
	if v := Value(c, "Port"); v != "22" {
		t.Errorf("Port = %q, want 22", v)
	}
	if r := Rport(c); r != "9001" {
		t.Errorf("Rport = %q, want 9001", r)
	}
}

func TestValueIgnoresLinesOutsideBlock(t *testing.T) {
	c := "Host other\n    HostName decoy\n\n" + Render(BlockOpts{Host: "real", User: "u", Port: 22})
	if v := Value(c, "HostName"); v != "real" {
		t.Errorf("HostName = %q, want real (must read inside the block only)", v)
	}
}

func TestHasBlockAndUnmanaged(t *testing.T) {
	if HasBlock("Host foo\n") {
		t.Error("HasBlock true on unrelated content")
	}
	if !HasUnmanagedHost("Host remote-claude\n    HostName x\n") {
		t.Error("expected unmanaged remote-claude host to be detected")
	}
	managed := Render(BlockOpts{Host: "h", User: "u", Port: 22})
	if HasUnmanagedHost(managed) {
		t.Error("managed block must not count as an unmanaged host")
	}
}

// WriteFile must preserve pieces other items own when rewriting only the host.
func TestWriteFilePreservesRevPortAndProxy(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")

	// Seed: a block with reverse port + proxy.
	first := Render(BlockOpts{Host: "old", User: "dev", Port: 22, RevPort: 2222, Proxy: `"/x/rc" relay %h %p`})
	if err := os.WriteFile(cfg, []byte(first+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Rewrite only the host, carrying forward the preserved fields (as menu does).
	raw, _ := os.ReadFile(cfg)
	rp := Rport(string(raw))
	proxy := "" // menu reads ProxyOn then re-supplies the proxy; simulate carry-through
	if ProxyOn(string(raw)) {
		proxy = `"/x/rc" relay %h %p`
	}
	revInt := 0
	if rp != "" {
		revInt = 2222
	}
	ok, err := WriteFile(cfg, BlockOpts{Host: "new", User: "dev", Port: 22, RevPort: revInt, Proxy: proxy},
		Deps{Force: true})
	if err != nil || !ok {
		t.Fatalf("WriteFile ok=%v err=%v", ok, err)
	}

	out, _ := os.ReadFile(cfg)
	s := string(out)
	if Value(s, "HostName") != "new" {
		t.Errorf("HostName not updated: %q", Value(s, "HostName"))
	}
	if Rport(s) != "2222" {
		t.Errorf("reverse port lost: %q", Rport(s))
	}
	if !ProxyOn(s) {
		t.Error("ProxyCommand lost on host rewrite")
	}
	if strings.Count(s, BeginMarkForTest()) != 1 {
		t.Errorf("expected exactly one managed block, got %d", strings.Count(s, BeginMarkForTest()))
	}
}

func TestWriteFileDeclineKeepsFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	seed := Render(BlockOpts{Host: "keep", User: "u", Port: 22}) + "\n"
	os.WriteFile(cfg, []byte(seed), 0o600)

	ok, err := WriteFile(cfg, BlockOpts{Host: "changed", User: "u", Port: 22},
		Deps{Confirm: func(string, bool) bool { return false }})
	if err != nil || ok {
		t.Fatalf("expected ok=false err=nil on decline, got ok=%v err=%v", ok, err)
	}
	out, _ := os.ReadFile(cfg)
	if Value(string(out), "HostName") != "keep" {
		t.Error("declined update must not modify the file")
	}
}

// BeginMarkForTest exposes the marker constant to the test without importing paths.
func BeginMarkForTest() string { return "# >>> remote-claude (managed by reverse-ssh-bootstrap) >>>" }
