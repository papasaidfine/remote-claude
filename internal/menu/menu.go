// Package menu wires the eight bootstrap items to the shared core and the
// platform layer, and drives the interactive numbered menu. Behavior (gates,
// env overrides, idempotency) matches the original bootstrap scripts.
package menu

import (
	"fmt"
	"os"
	"regexp"
	"strconv"

	"github.com/papasaidfine/remote-claude/internal/authorize"
	"github.com/papasaidfine/remote-claude/internal/fsutil"
	"github.com/papasaidfine/remote-claude/internal/keys"
	"github.com/papasaidfine/remote-claude/internal/nodes"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/platform"
	"github.com/papasaidfine/remote-claude/internal/sshconfig"
	"github.com/papasaidfine/remote-claude/internal/testconn"
	"github.com/papasaidfine/remote-claude/internal/ui"
	"github.com/papasaidfine/remote-claude/internal/vless"
	"github.com/papasaidfine/remote-claude/internal/xray"
)

var numeric = regexp.MustCompile(`^\d+$`)

// Menu holds the resolved paths and platform for a run.
type Menu struct {
	P    paths.Paths
	Plat platform.Platform
}

// Run draws the menu and dispatches items until the user quits.
func Run(m Menu) {
	banner(m.Plat)
	for {
		m.draw()
		choice, ok := ui.ReadChoice(m.selectPrompt())
		if !ok {
			break
		}
		if choice == "q" || choice == "Q" {
			break
		}
		fn := m.dispatch(choice)
		if fn == nil {
			ui.Warn("Unknown selection: %s", choice)
			continue
		}
		fmt.Println()
		if err := fn(); err != nil {
			ui.Errf("Item did not complete: %v", err)
			ui.Errf("Other items are unaffected.")
		}
	}
	fmt.Println()
	ui.Log("Connect as usual — VSCode Remote-SSH (host %s) or: ssh %s", paths.Alias, paths.Alias)
	ui.Log("The reverse tunnel rides on that connection (one connection at a time).")
	ui.Log("Then on the server: ssh my-device 'echo ok' should print ok")
}

func (m Menu) dispatch(choice string) func() error {
	base := map[string]func() error{
		"1": m.runConfig,
		"2": m.runKey,
		"3": m.runTest,
		"4": m.runSshd,
		"5": m.runAuthorize,
		"6": m.runRport,
	}
	if m.Plat.SupportsXray() {
		base["7"] = m.runXray
		base["8"] = m.runProxy
	}
	return base[choice]
}

func (m Menu) selectPrompt() string {
	if m.Plat.SupportsXray() {
		return "Select [1-8, q]"
	}
	return "Select [1-6, q]"
}

// ---- helpers ----

func (m Menu) readConfig() string {
	b, _ := os.ReadFile(m.P.SSHConfig)
	return string(b)
}

func (m Menu) setPerms(path string, isDir bool) error { return m.Plat.SetStrictPerms(path, isDir) }

// proxyValue is the ProxyCommand this tool writes: "<self>" relay %h %p.
func proxyValue() string {
	return fmt.Sprintf("%q relay %%h %%p", paths.SelfExe())
}

// writeBlock renders/writes the managed block with the standard deps.
func (m Menu) writeBlock(o sshconfig.BlockOpts, force bool) (bool, error) {
	return sshconfig.WriteFile(m.P.SSHConfig, o, sshconfig.Deps{
		Force:    force,
		Confirm:  ui.AskYN,
		SetPerms: func(p string) error { return m.setPerms(p, false) },
		Backup: func(p string) (string, error) {
			bak, err := fsutil.Backup(p)
			if err == nil && bak != "" {
				ui.Log("Backed up ssh config -> %s", bak)
			}
			return bak, err
		},
	})
}

func (m Menu) statusConfig() bool { return sshconfig.HasBlock(m.readConfig()) }
func (m Menu) statusKey() bool {
	_, err := os.Stat(m.P.KeyPath)
	return err == nil
}
func (m Menu) statusAuthorize() bool {
	b, _ := os.ReadFile(m.P.AuthKeys)
	return regexp.MustCompile(`from="127\.0\.0\.1,::1"`).Match(b)
}
func (m Menu) statusRport() bool { return sshconfig.Value(m.readConfig(), "RemoteForward") != "" }
func (m Menu) statusSshd() bool  { return m.Plat.StatusIncomingSSH() }
func (m Menu) statusXray() bool  { return xray.Resolve(m.P) != "" }
func (m Menu) proxyOn() bool     { return sshconfig.ProxyOn(m.readConfig()) }

// ---- item 1: base SSH config ----

func (m Menu) runConfig() error {
	if err := keys.EnsureSSHDir(m.P, m.setPerms); err != nil {
		return err
	}
	c := m.readConfig()
	host, user, port := "", "", ""
	rport, useProxy := 0, false
	if sshconfig.HasBlock(c) {
		host = sshconfig.Value(c, "HostName")
		user = sshconfig.Value(c, "User")
		port = sshconfig.Value(c, "Port")
		if rp := sshconfig.Rport(c); rp != "" {
			rport, _ = strconv.Atoi(rp)
		}
		useProxy = sshconfig.ProxyOn(c)
	}
	if v := os.Getenv("SERVER_HOST"); v != "" {
		host = v
	}
	if v := os.Getenv("SERVER_USER"); v != "" {
		user = v
	}
	if v := os.Getenv("SERVER_PORT"); v != "" {
		port = v
	}
	if port == "" {
		port = "22"
	}

	build := func() sshconfig.BlockOpts {
		p, _ := strconv.Atoi(port)
		o := sshconfig.BlockOpts{Host: host, User: user, Port: p, RevPort: rport}
		if useProxy {
			o.Proxy = proxyValue()
		}
		return o
	}

	// Non-interactive path: both host and user supplied via env.
	if os.Getenv("SERVER_HOST") != "" && os.Getenv("SERVER_USER") != "" {
		if err := checkFields(host, user, port); err != nil {
			return err
		}
		if _, err := m.writeBlock(build(), false); err != nil {
			return err
		}
		return nil
	}

	for {
		fmt.Println()
		fmt.Printf("SSH config shortcut (Host %s) — edit fields, then apply:\n", paths.Alias)
		fmt.Printf("  1) %-22s %s\n", "Server host / IP", orNotSet(host))
		fmt.Printf("  2) %-22s %s\n", "SSH user", orNotSet(user))
		fmt.Printf("  3) %-22s %s\n", "SSH port", port)
		fmt.Println("  a) Apply & write config")
		fmt.Println("  q) Cancel (no changes)")
		sel, ok := ui.ReadChoice("Select [1-3, a, q]")
		if !ok {
			return nil
		}
		switch sel {
		case "1":
			host = ui.Ask("Server host / IP", host)
		case "2":
			user = ui.Ask("SSH user", user)
		case "3":
			port = ui.Ask("SSH port", port)
		case "a", "A":
			if err := checkFields(host, user, port); err != nil {
				ui.Errf("%v", err)
				continue
			}
			if _, err := m.writeBlock(build(), true); err != nil {
				return err
			}
			return nil
		case "q", "Q":
			ui.Log("Cancelled — nothing changed")
			return nil
		default:
			ui.Warn("Unknown selection: %s", sel)
		}
	}
}

// ---- item 2: local key ----

func (m Menu) runKey() error {
	res, err := keys.Ensure(m.P, m.setPerms)
	if err != nil {
		return err
	}
	if res.Generated {
		ui.Log("Generated the default SSH key: %s", m.P.KeyPath)
	} else {
		ui.Log("Using existing SSH key: %s", m.P.KeyPath)
	}
	if res.Passphrase {
		ui.Warn("This key appears to be passphrase-protected; the tunnel will need an ssh-agent to work")
	}
	fmt.Println()
	ui.Log("Local public key — paste it into server/setup-server.sh (item 2) on the")
	ui.Log("server; that authorizes the tunnel login (ssh %s):", paths.Alias)
	fmt.Println()
	fmt.Print(res.Pub)
	if len(res.Pub) == 0 || res.Pub[len(res.Pub)-1] != '\n' {
		fmt.Println()
	}
	fmt.Println()
	return nil
}

// ---- item 3: test ----

func (m Menu) runTest() error { return testconn.Run(m.P) }

// ---- item 4: incoming sshd ----

func (m Menu) runSshd() error {
	if err := m.Plat.RequireElevation(); err != nil {
		return err
	}
	disable := ui.AskYN("Disable password login for the local sshd (recommended, public key only)", true)
	return m.Plat.EnsureIncomingSSH(disable)
}

// ---- item 5: authorize ----

func (m Menu) runAuthorize() error {
	if err := keys.EnsureSSHDir(m.P, m.setPerms); err != nil {
		return err
	}
	fmt.Println("Server-side public key: the .pub of the key that Claude / Codex on the")
	fmt.Println("server will use to SSH back into this machine (setup-server.sh item 1")
	fmt.Println("prints it, or: cat ~/.ssh/id_ed25519.pub on the server).")
	pub := os.Getenv("SERVER_PUBKEY")
	if pub == "" {
		pub = ui.Ask("Server-side public key", "")
	}
	added, err := authorize.Add(m.P.AuthKeys, pub, keys.ValidatePub())
	if err != nil {
		return err
	}
	if added {
		ui.Log("Written to authorized_keys (restricted to loopback logins only)")
	} else {
		ui.Log("This public key is already in authorized_keys, skipping")
	}
	return nil
}

// ---- item 6: reverse port ----

func (m Menu) runRport() error {
	c := m.readConfig()
	if !sshconfig.HasBlock(c) {
		return fmt.Errorf("no Host %s block yet — run item 1 first", paths.Alias)
	}
	host := sshconfig.Value(c, "HostName")
	user := sshconfig.Value(c, "User")
	port := sshconfig.Value(c, "Port")
	if host == "" || user == "" || port == "" {
		return fmt.Errorf("could not read the Host %s block — re-run item 1", paths.Alias)
	}
	cur := sshconfig.Rport(c)
	rport := os.Getenv("REVERSE_PORT")
	if rport == "" {
		def := cur
		if def == "" {
			def = "2222"
		}
		rport = ui.Ask("Reverse SSH port on the server (used by Claude/Codex to connect back)", def)
	}
	if !numeric.MatchString(rport) {
		return fmt.Errorf("reverse port must be a number")
	}
	p, _ := strconv.Atoi(port)
	rp, _ := strconv.Atoi(rport)
	o := sshconfig.BlockOpts{Host: host, User: user, Port: p, RevPort: rp}
	if sshconfig.ProxyOn(c) {
		o.Proxy = proxyValue()
	}
	if _, err := m.writeBlock(o, true); err != nil {
		return err
	}
	ui.Log("Reverse port %s set — the tunnel rides on your ssh %s connection.", rport, paths.Alias)
	return nil
}

// ---- item 7: xray client ----

func (m Menu) runXray() error {
	dlProxy := ui.Ask("Proxy for the xray download (e.g. http://127.0.0.1:7890, empty = direct)", "")
	if dlProxy != "" {
		ui.Log("Using proxy %s for this item's downloads", dlProxy)
	}
	client := xray.New(dlProxy)
	if xray.Resolve(m.P) != "" {
		if err := client.Update(m.P); err != nil {
			return err
		}
	} else {
		if err := client.Install(m.P); err != nil {
			return err
		}
	}
	if err := m.ensureNodesFile(); err != nil {
		return err
	}
	os.Remove(m.P.RCConfigDir + string(os.PathSeparator) + "xray.json") // pre-nodes-file layout
	ui.Log("Nodes file: %s — one vless:// URL per line (# comments).", m.P.VlessNodes)
	ui.Log("Each connection picks a random node; edits take effect on the next connect.")

	// Migrate a legacy ProxyCommand (old launcher/relay) to the new subcommand.
	c := m.readConfig()
	if sshconfig.HasBlock(c) && sshconfig.ProxyOn(c) {
		host := sshconfig.Value(c, "HostName")
		user := sshconfig.Value(c, "User")
		port := sshconfig.Value(c, "Port")
		if host != "" && user != "" && port != "" {
			p, _ := strconv.Atoi(port)
			rp := 0
			if r := sshconfig.Rport(c); r != "" {
				rp, _ = strconv.Atoi(r)
			}
			if _, err := m.writeBlock(sshconfig.BlockOpts{Host: host, User: user, Port: p, RevPort: rp, Proxy: proxyValue()}, true); err == nil {
				ui.Log("Migrated the ProxyCommand to the built-in relay")
			}
		}
	}
	ui.Log("Route the tunnel through xray via item 8 (ProxyCommand).")
	return nil
}

func (m Menu) ensureNodesFile() error {
	if _, err := os.Stat(m.P.VlessNodes); err == nil {
		return nil
	}
	seed := os.Getenv("VLESS_URL")
	if seed != "" {
		if err := vless.Validate(seed); err != nil {
			return fmt.Errorf("could not parse VLESS_URL: %w", err)
		}
	}
	if err := os.MkdirAll(m.P.RCConfigDir, 0o755); err != nil {
		return err
	}
	body := "# vless nodes for the remote-claude tunnel — one vless:// URL per line.\n" +
		"# Lines starting with # and blank lines are ignored.\n" +
		"# Every connection picks a random node; edits take effect on the next connect.\n"
	if seed != "" {
		body += seed + "\n"
	}
	if err := os.WriteFile(m.P.VlessNodes, []byte(body), 0o600); err != nil {
		return err
	}
	ui.Log("Created %s", m.P.VlessNodes)
	return nil
}

// ---- item 8: proxy toggle ----

func (m Menu) runProxy() error {
	c := m.readConfig()
	if !sshconfig.HasBlock(c) {
		return fmt.Errorf("no Host %s block yet — run item 1 first", paths.Alias)
	}
	host := sshconfig.Value(c, "HostName")
	user := sshconfig.Value(c, "User")
	port := sshconfig.Value(c, "Port")
	if host == "" || user == "" || port == "" {
		return fmt.Errorf("could not read the Host %s block — re-run item 1", paths.Alias)
	}
	rp := 0
	if r := sshconfig.Rport(c); r != "" {
		rp, _ = strconv.Atoi(r)
	}
	want := !sshconfig.ProxyOn(c)
	if v := os.Getenv("USE_XRAY_PROXY"); v != "" {
		want = v == "1"
	}
	p, _ := strconv.Atoi(port)
	o := sshconfig.BlockOpts{Host: host, User: user, Port: p, RevPort: rp}
	if want {
		if !m.statusXray() {
			return fmt.Errorf("xray client not set up — run item 7 first")
		}
		if nodes.Count(m.P.VlessNodes) == 0 {
			return fmt.Errorf("no nodes in %s — add a vless:// URL there first", m.P.VlessNodes)
		}
		o.Proxy = proxyValue()
		if _, err := m.writeBlock(o, true); err != nil {
			return err
		}
		ui.Log("Proxy ON — ssh %s now routes through xray", paths.Alias)
	} else {
		if _, err := m.writeBlock(o, true); err != nil {
			return err
		}
		ui.Log("Proxy OFF — ssh %s connects directly again", paths.Alias)
	}
	return nil
}

// ---- shared ----

func checkFields(host, user, port string) error {
	if host == "" {
		return fmt.Errorf("server host must not be empty")
	}
	if user == "" {
		return fmt.Errorf("SSH user must not be empty")
	}
	if !numeric.MatchString(port) {
		return fmt.Errorf("SSH port must be a number")
	}
	return nil
}

func orNotSet(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}
