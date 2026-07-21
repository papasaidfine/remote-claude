package provision

import (
	_ "embed"
	"fmt"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	"github.com/papasaidfine/remote-claude/internal/authorize"
	"github.com/papasaidfine/remote-claude/internal/keys"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/sshbin"
	"github.com/papasaidfine/remote-claude/internal/store"
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

// ServerBootstrap configures the server side over the existing outbound
// connection (ssh remote-claude): it ensures the server's connect-back key,
// authorizes this machine's key for the tunnel login, writes the server's
// reverse "Host <alias>" block with the app's reverse port (so both ends stay
// in sync), and authorizes the returned connect-back key locally (loopback
// only). Requires the local key to be authorized on the server already — see
// the first-contact note in the UI.
func (c *Client) ServerBootstrap(h store.Host, clientAlias string) (ServerResult, error) {
	if err := h.Validate(); err != nil {
		return ServerResult{}, err
	}
	if clientAlias == "" {
		return ServerResult{}, fmt.Errorf("set this machine's name first — it names the server-side Host block")
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
		ReversePort: h.ReversePort,
		LocalUser:   lu,
		LocalPubKey: strings.TrimSpace(res.Pub),
	}, agentClaudeMD)

	ssh := sshbin.SSH()
	cmd := exec.Command(ssh, "-o", "BatchMode=yes", "-o", "ConnectTimeout=15", paths.Alias, "bash -s")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ServerResult{}, fmt.Errorf("server setup over 'ssh %s' failed (is this machine's key authorized on the server yet? do first-contact once): %v\n%s",
			paths.Alias, err, tailStr(string(out), 600))
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
