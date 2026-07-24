package provision

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/papasaidfine/remote-claude/internal/platform"
)

func TestRenderServerScript(t *testing.T) {
	s := renderServerScript(serverInput{
		Alias:       "lisa-laptop",
		ReversePort: 2222,
		LocalUser:   "dev",
		LocalPubKey: "ssh-ed25519 AAAAC3xyz dev@host",
	}, "# CLAUDE MD BODY uses $LC_CLIENT_NAME\n")
	for _, want := range []string{
		"set -eu",
		"ALIAS='lisa-laptop'",
		"REVERSE_PORT='2222'",
		"LOCAL_USER='dev'",
		"LOCAL_PUBKEY='ssh-ed25519 AAAAC3xyz dev@host'",
		"<<'RC_CLAUDE_MD_EOF'",
		"# CLAUDE MD BODY uses $LC_CLIENT_NAME",
		"$FACTS_DIR/$ALIAS.json",
		"<<<RC_PUBKEY_BEGIN>>>", // from the embedded body
		"Host %s",               // block writer from the embedded body
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered script missing %q", want)
		}
	}
}

// The embedded CLAUDE.md must be device-aware (uses $LC_CLIENT_NAME, no
// hardcoded my-device) so one global file serves multiple devices.
func TestEmbeddedClaudeMDIsDeviceAware(t *testing.T) {
	if !strings.Contains(agentClaudeMD, `ssh "$LC_CLIENT_NAME"`) {
		t.Error("CLAUDE.md should drive ssh via $LC_CLIENT_NAME")
	}
	if strings.Contains(agentClaudeMD, "ssh my-device") {
		t.Error("CLAUDE.md still hardcodes my-device")
	}
	if !strings.Contains(agentClaudeMD, "facts/$LC_CLIENT_NAME.json") {
		t.Error("CLAUDE.md should point at per-device facts")
	}
}

// The script piped to the server's bash must be LF-only — a CRLF from a Windows
// checkout would make bash fail with "$'\r': command not found".
func TestRenderServerScriptHasNoCR(t *testing.T) {
	s := renderServerScript(serverInput{Alias: "a", ReversePort: 2222, LocalUser: "u", LocalPubKey: "k"},
		"line one\r\nline two\r\n") // even CRLF input must be normalized away
	if strings.Contains(s, "\r") {
		t.Error("rendered script contains CR; the server's bash will choke on it")
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

// TestServerScriptExecutes runs the fully rendered bootstrap script through a
// real bash against a temp HOME and checks every artifact it should produce.
// This exercises the embedded server-side bash end-to-end without a server.
func TestServerScriptExecutes(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	home := t.TempDir()
	pub := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITESTBLOBxxxxxxxxxxxxxxxxxxxxxxxxx laptop@lisa"
	script := renderServerScript(serverInput{
		Alias: "lisa-laptop", ReversePort: 2222, LocalUser: "dev", LocalPubKey: pub,
	}, agentClaudeMD)

	cmd := exec.Command("bash")
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}

	if got := extractMarked(string(out), pubBegin, pubEnd); got == "" {
		t.Errorf("script emitted no connect-back pubkey:\n%s", out)
	}
	checks := []struct{ path, want string }{
		{".ssh/config", "Host lisa-laptop"},
		{".ssh/config", "Port 2222"},
		{".ssh/config", "UserKnownHostsFile ~/.ssh/known_hosts.lisa-laptop"},
		{".ssh/authorized_keys", "remote-claude-tunnel"},
		{".claude/CLAUDE.md", `ssh "$LC_CLIENT_NAME"`},
		{".claude/CLAUDE.md", "<!-- >>> remote-claude (managed) >>> -->"},
		{".config/remote-claude/facts/lisa-laptop.json", `"machine"`},
	}
	for _, c := range checks {
		b, err := os.ReadFile(filepath.Join(home, c.path))
		if err != nil {
			t.Errorf("missing artifact %s: %v", c.path, err)
			continue
		}
		if !strings.Contains(string(b), c.want) {
			t.Errorf("%s missing %q", c.path, c.want)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh/id_ed25519")); err != nil {
		t.Errorf("connect-back key not generated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/.rc-claude-md.new")); !os.IsNotExist(err) {
		t.Errorf("temp CLAUDE.md file not cleaned up")
	}

	// Idempotent: a second run must not duplicate the authorized_keys entry.
	cmd2 := exec.Command("bash")
	cmd2.Stdin = strings.NewReader(script)
	cmd2.Env = cmd.Env
	if out2, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("second run failed: %v\n%s", err, out2)
	}
	ak, _ := os.ReadFile(filepath.Join(home, ".ssh/authorized_keys"))
	if n := strings.Count(string(ak), "remote-claude-tunnel"); n != 1 {
		t.Errorf("authorized_keys not idempotent: %d entries", n)
	}
	cfg, _ := os.ReadFile(filepath.Join(home, ".ssh/config"))
	if n := strings.Count(string(cfg), "Host lisa-laptop"); n != 1 {
		t.Errorf("ssh config Host block not idempotent: %d blocks", n)
	}
}

func TestBootstrapSSHArgs(t *testing.T) {
	args := strings.Join(bootstrapSSHArgs("workbox"), " ")
	if !strings.Contains(args, "BatchMode=yes") {
		t.Errorf("bootstrap must be key/agent-only (BatchMode): %q", args)
	}
	if strings.Contains(args, "NumberOfPasswordPrompts") || strings.Contains(args, "SSH_ASKPASS") {
		t.Errorf("bootstrap must never use a password path: %q", args)
	}
	if !strings.Contains(args, "workbox") || !strings.HasSuffix(args, "bash -s") {
		t.Errorf("must ssh to the alias then run the script: %q", args)
	}
}

func TestServerBootstrapRequiresAlias(t *testing.T) {
	c := New(testPaths(t), platform.New())
	if _, err := c.ServerBootstrap("wb", "", 2222); err == nil {
		t.Fatal("expected error when client alias is empty")
	}
	if _, err := c.ServerBootstrap("wb", "me", 0); err == nil {
		t.Fatal("expected error when reverse port is missing")
	}
}
