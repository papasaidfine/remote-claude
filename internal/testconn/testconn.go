// Package testconn implements the adaptive, read-only connection test (item 3):
// an outbound login check plus, when a reverse port is configured, a full-loop
// tunnel probe.
package testconn

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/sshbin"
	"github.com/papasaidfine/remote-claude/internal/sshconfig"
	"github.com/papasaidfine/remote-claude/internal/ui"
)

var numeric = regexp.MustCompile(`^\d+$`)

// Run performs the connection test. It never modifies files; it returns an
// error when any check fails (mirroring the scripts' `die`).
func Run(p paths.Paths) error {
	raw, _ := os.ReadFile(p.SSHConfig)
	content := string(raw)
	if !sshconfig.HasBlock(content) {
		return fmt.Errorf("no Host %s block yet — run item 1 first", paths.Alias)
	}
	rport := sshconfig.Rport(content)
	if rport != "" && !numeric.MatchString(rport) {
		return fmt.Errorf("RemoteForward port %q in the managed block is not numeric — re-run item 6", rport)
	}
	ssh := sshbin.SSH()
	ok := true

	ui.Log("Testing outbound hop: ssh %s ...", paths.Alias)
	out := runSSH(ssh, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		"-o", "ClearAllForwardings=yes", paths.Alias, "echo ok")
	if containsLine(out, "ok") {
		ui.Tick("outbound: ssh %s works", paths.Alias)
	} else {
		ui.Cross("outbound: could not log in to the server")
		printLastLines(out, 3)
		ui.Warn("Most common cause: the server has not authorized your local key yet —")
		ui.Warn("item 2 prints it; paste it into server/setup-server.sh (item 2) there.")
		ok = false
	}

	if ok && rport != "" {
		ui.Log("Testing reverse tunnel: server 127.0.0.1:%s -> this machine's sshd ...", rport)
		probe := fmt.Sprintf("bash -c 'exec 3<>/dev/tcp/127.0.0.1/%s' 2>/dev/null && echo tunnel-ok", rport)
		out := runSSH(ssh, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
			"-o", "ExitOnForwardFailure=no", paths.Alias, probe)
		if containsLine(out, "tunnel-ok") {
			ui.Tick("reverse tunnel: server port %s reaches this machine's sshd", rport)
		} else {
			ui.Cross("reverse tunnel: server port %s did not answer", rport)
			ui.Warn("Check phase ② — incoming sshd (item 4), authorized key (item 5), reverse port (item 6).")
			ok = false
		}
	} else if rport == "" {
		ui.Warn("No reverse tunnel port yet (item 6) — skipped the tunnel check.")
	}

	if sshconfig.ProxyOn(content) {
		ui.Log("Path: via xray (ProxyCommand) — a passing test also validates phase ③.")
	} else {
		ui.Log("Path: direct (xray proxy off).")
	}
	if !ok {
		return fmt.Errorf("some checks failed (see above)")
	}
	return nil
}

// runSSH runs ssh capturing combined output; the caller judges success by the
// output marker, so a non-zero exit is expected and ignored here.
func runSSH(ssh string, args ...string) string {
	out, _ := exec.Command(ssh, args...).CombinedOutput()
	return string(out)
}

func containsLine(out, want string) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimRight(l, "\r") == want {
			return true
		}
	}
	return false
}

func printLastLines(out string, n int) {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for _, l := range lines {
		ui.Plain("      %s", strings.TrimRight(l, "\r"))
	}
}
