package provision

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	"github.com/papasaidfine/remote-claude/internal/authorize"
	"github.com/papasaidfine/remote-claude/internal/keys"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/sshbin"
)

//go:embed server_bootstrap.sh
var serverScriptBody string

//go:embed agent_claude_md.md
var agentClaudeMD string

const (
	pubBegin = "<<<RC_PUBKEY_BEGIN>>>"
	pubEnd   = "<<<RC_PUBKEY_END>>>"
)

// ServerResult reports what ServerBootstrap did.
type ServerResult struct {
	ServerPubKey string `json:"server_pubkey"` // the server's connect-back key
	Authorized   bool   `json:"authorized"`    // newly added to local authorized_keys
	Alias        string `json:"alias"`         // the server-side reverse Host name
}

type serverInput struct {
	Alias       string
	ReversePort int
	LocalUser   string
	LocalPubKey string
}

// ServerBootstrap configures the server side over the outbound connection (ssh
// remote-claude): it ensures the server's connect-back key, authorizes this
// machine's key for the tunnel login, writes the server's reverse "Host <alias>"
// block with the app's reverse port (so both ends stay in sync), installs the
// device-aware CLAUDE.md + per-device facts, and authorizes the returned
// connect-back key locally (loopback only).
//
// password is used only for first contact, when the local key isn't authorized
// on the server yet: it is fed to ssh headlessly via SSH_ASKPASS. Empty password
// means key/agent auth only (BatchMode). Once this runs, the local key is
// authorized, so later connections need no password.
func (c *Client) ServerBootstrap(alias, clientAlias string, reversePort int, password string) (ServerResult, error) {
	if alias == "" {
		return ServerResult{}, fmt.Errorf("no ssh host selected")
	}
	if clientAlias == "" {
		return ServerResult{}, fmt.Errorf("set this machine's name first — it names the server-side Host block")
	}
	if reversePort <= 0 {
		return ServerResult{}, fmt.Errorf("configure a reverse tunnel port on host %q first", alias)
	}
	res, err := keys.Ensure(c.P, c.Plat.SetStrictPerms)
	if err != nil {
		return ServerResult{}, fmt.Errorf("ensure local key: %w", err)
	}
	lu := localUser()
	if lu == "" {
		return ServerResult{}, fmt.Errorf("could not determine the local username")
	}
	script := renderServerScript(serverInput{
		Alias:       clientAlias,
		ReversePort: reversePort,
		LocalUser:   lu,
		LocalPubKey: strings.TrimSpace(res.Pub),
	}, agentClaudeMD)

	cmd := exec.Command(sshbin.SSH(), bootstrapSSHArgs(alias, password)...)
	cmd.Stdin = strings.NewReader(script)
	if password != "" {
		cmd.Env = append(os.Environ(), askpassEnv(password)...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ServerResult{}, fmt.Errorf("server setup over 'ssh %s' failed: %v\n%s",
			alias, err, tailStr(string(out), 600))
	}

	pub := extractMarked(string(out), pubBegin, pubEnd)
	if pub == "" {
		return ServerResult{}, fmt.Errorf("server setup ran but returned no connect-back key\n%s", tailStr(string(out), 600))
	}
	if err := keys.EnsureSSHDir(c.P, c.Plat.SetStrictPerms); err != nil {
		return ServerResult{}, err
	}
	added, err := authorize.Add(c.P.AuthKeys, pub, keys.ValidatePub())
	if err != nil {
		return ServerResult{}, fmt.Errorf("authorize the server's key locally: %w", err)
	}
	return ServerResult{ServerPubKey: pub, Authorized: added, Alias: clientAlias}, nil
}

// renderServerScript builds the full script piped to the server: `set -eu`, the
// shell-escaped inputs, a quoted heredoc carrying the CLAUDE.md body (so nothing
// in it is expanded here — `$LC_CLIENT_NAME` stays literal for the agent), then
// the static body.
// bootstrapSSHArgs builds the ssh args to pipe the bootstrap script. With a
// password it drops BatchMode and lets ssh use the SSH_ASKPASS helper; without,
// it stays key/agent-only (BatchMode).
func bootstrapSSHArgs(alias, password string) []string {
	args := []string{
		"-o", "ConnectTimeout=15",
		"-o", "StrictHostKeyChecking=accept-new",
		// Use the user's agent/default keys too, not only the app's key — the
		// host block forces IdentitiesOnly, but for this one-time bootstrap we
		// want to get in however the user already can.
		"-o", "IdentitiesOnly=no",
	}
	if password == "" {
		args = append(args, "-o", "BatchMode=yes")
	} else {
		args = append(args, "-o", "NumberOfPasswordPrompts=1")
	}
	return append(args, alias, "bash -s")
}

// askpassEnv makes ssh fetch the password non-interactively by re-executing this
// binary in askpass mode (see main.go RC_ASKPASS_MODE). DISPLAY is a fallback
// for OpenSSH older than the SSH_ASKPASS_REQUIRE flag.
func askpassEnv(password string) []string {
	return []string{
		"SSH_ASKPASS=" + paths.SelfExe(),
		"SSH_ASKPASS_REQUIRE=force",
		"RC_ASKPASS_MODE=1",
		"RC_ASKPASS_SECRET=" + password,
		"DISPLAY=:0",
	}
}

func renderServerScript(in serverInput, claudeMD string) string {
	var b strings.Builder
	b.WriteString("set -eu\n")
	b.WriteString("ALIAS=" + shquote(in.Alias) + "\n")
	b.WriteString("REVERSE_PORT=" + shquote(strconv.Itoa(in.ReversePort)) + "\n")
	b.WriteString("LOCAL_USER=" + shquote(in.LocalUser) + "\n")
	b.WriteString("LOCAL_PUBKEY=" + shquote(in.LocalPubKey) + "\n")
	b.WriteString("mkdir -p \"$HOME/.claude\"\n")
	b.WriteString("cat > \"$HOME/.claude/.rc-claude-md.new\" <<'RC_CLAUDE_MD_EOF'\n")
	b.WriteString(claudeMD)
	if !strings.HasSuffix(claudeMD, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("RC_CLAUDE_MD_EOF\n")
	b.WriteString(serverScriptBody)
	return b.String()
}

// shquote single-quotes s for safe interpolation into a POSIX shell.
func shquote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func extractMarked(out, begin, end string) string {
	i := strings.Index(out, begin)
	if i < 0 {
		return ""
	}
	rest := out[i+len(begin):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

func tailStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return s
}

func localUser() string {
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return ""
	}
	name := u.Username
	if i := strings.LastIndexAny(name, `\/`); i >= 0 { // strip DOMAIN\ on Windows
		name = name[i+1:]
	}
	return name
}
