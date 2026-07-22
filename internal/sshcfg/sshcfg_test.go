package sshcfg

import (
	"strings"
	"testing"
)

const sample = `# my ssh config
Global something

Host remote-claude
    HostName srv.example.com
    User dev
    Port 22
    # keep me
    RemoteForward 127.0.0.1:2222 127.0.0.1:22
    ProxyCommand "/x/rc" relay %h %p

Host bastion jump
    HostName 10.0.0.1
    User admin

Match host *.internal
    ForwardAgent yes
`

func TestRoundTrip(t *testing.T) {
	if got := Parse(sample).String(); got != sample {
		t.Fatalf("round-trip mismatch:\n---got---\n%s\n---want---\n%s", got, sample)
	}
}

func TestParseHostsAndParams(t *testing.T) {
	f := Parse(sample)
	hosts := f.Hosts()
	if len(hosts) != 2 { // Match block excluded
		t.Fatalf("expected 2 host blocks, got %d", len(hosts))
	}
	rc := f.FindHost("remote-claude")
	if rc == nil {
		t.Fatal("remote-claude not found")
	}
	if rc.Get("HostName") != "srv.example.com" || rc.Get("User") != "dev" || rc.Get("Port") != "22" {
		t.Errorf("bad params: %+v", rc.Summary())
	}
	// alias matching a second pattern
	if f.FindHost("jump") == nil {
		t.Error("secondary pattern 'jump' not matched")
	}
}

func TestSummaryReverseAndProxy(t *testing.T) {
	s := Parse(sample).FindHost("remote-claude").Summary()
	if !s.HasReverse || s.ReversePort != "2222" {
		t.Errorf("reverse tunnel not summarized: %+v", s)
	}
	if !s.HasProxy || !strings.Contains(s.ProxyCommand, "relay %h %p") {
		t.Errorf("proxy not summarized: %+v", s)
	}
	b := Parse(sample).FindHost("bastion").Summary()
	if b.HasReverse || b.HasProxy {
		t.Errorf("bastion should have neither reverse nor proxy: %+v", b)
	}
}

func TestSetPreservesOtherLines(t *testing.T) {
	f := Parse(sample)
	rc := f.FindHost("remote-claude")
	rc.Set("Port", "2200")       // change existing
	rc.Set("ForwardAgent", "no") // add new
	out := f.String()
	if !strings.Contains(out, "    Port 2200") {
		t.Errorf("Port not updated:\n%s", out)
	}
	if !strings.Contains(out, "# keep me") {
		t.Error("comment inside block was lost")
	}
	if !strings.Contains(out, `ProxyCommand "/x/rc" relay %h %p`) {
		t.Error("ProxyCommand line was lost")
	}
	if !strings.Contains(out, "    ForwardAgent no") {
		t.Error("new key not appended")
	}
}

func TestRemoveKey(t *testing.T) {
	f := Parse(sample)
	rc := f.FindHost("remote-claude")
	rc.Remove("RemoteForward")
	if rc.Has("RemoteForward") {
		t.Error("RemoteForward not removed")
	}
	// Set with empty value also removes.
	rc.Set("ProxyCommand", "")
	if rc.Has("ProxyCommand") {
		t.Error("ProxyCommand not removed via empty Set")
	}
}

func TestAddAndRemoveHost(t *testing.T) {
	f := Parse(sample)
	b := f.AddHost("newbox")
	b.Set("HostName", "new.example.com")
	b.Set("User", "me")
	if f.FindHost("newbox") == nil {
		t.Fatal("added host not found")
	}
	if !strings.Contains(f.String(), "Host newbox") {
		t.Error("added host not serialized")
	}
	if !f.RemoveHost("newbox") || f.FindHost("newbox") != nil {
		t.Error("host not removed")
	}
}

func TestEqualsSyntax(t *testing.T) {
	f := Parse("Host x\n    Port=2201\n    User = bob\n")
	b := f.FindHost("x")
	if b.Get("Port") != "2201" {
		t.Errorf("Port= not parsed: %q", b.Get("Port"))
	}
	if b.Get("User") != "bob" {
		t.Errorf("User = not parsed: %q", b.Get("User"))
	}
}

func TestEmptyAndNoHosts(t *testing.T) {
	if len(Parse("").Hosts()) != 0 {
		t.Error("empty content should have no hosts")
	}
	f := Parse("# just comments\nGlobalOption yes\n")
	if len(f.Hosts()) != 0 || len(f.Preamble) == 0 {
		t.Error("preamble-only file mishandled")
	}
}
