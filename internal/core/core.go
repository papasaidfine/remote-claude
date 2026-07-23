// Package core is the front-end-agnostic application façade. The host list is
// the user's ~/.ssh/config (scanned via package sshcfg); a host's identity —
// HostName/User/Port, SetEnv LC_CLIENT_NAME, and ProxyCommand — lives there.
// The reverse-tunnel port and auto-start are app metadata (package store),
// applied as ephemeral ssh args when a tunnel starts, NOT written to the config
// (so an ordinary `ssh <alias>` never carries them and never warns).
package core

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/nodes"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/sshbin"
	"github.com/papasaidfine/remote-claude/internal/sshcfg"
	"github.com/papasaidfine/remote-claude/internal/store"
	"github.com/papasaidfine/remote-claude/internal/sysproc"
	"github.com/papasaidfine/remote-claude/internal/usage"
	"github.com/papasaidfine/remote-claude/internal/vless"
	"github.com/papasaidfine/remote-claude/internal/xray"
)

// Provisioner performs the side-effecting setup the app drives. Injected so App
// stays testable; may be nil (setup capabilities disabled).
type Provisioner interface {
	EnsureKey() error
	ServerBootstrap(alias, clientAlias string, reversePort int, password string) (provision.ServerResult, error)
	EnsureLocalSSHD(disablePassword bool) error
}

// TunnelManager is the subset of *bridge.Manager the app drives (test seam).
type TunnelManager interface {
	Start(spec bridge.Spec) error
	Stop(alias string)
	Status(alias string) bridge.Status
}

// PlatformInfo is the read-only platform surface the app reports.
type PlatformInfo interface {
	Name() string
	SupportsXray() bool
	StatusIncomingSSH() bool
}

// App is the live application state and orchestration. All methods are safe for
// concurrent use.
type App struct {
	mu       sync.Mutex
	meta     *store.Config
	metaPath string
	sshPath  string

	paths paths.Paths
	mgr   TunnelManager
	prov  Provisioner
	plat  PlatformInfo
}

// New builds an App. meta is the loaded metadata; p.SSHConfig is the ssh config
// it reads/edits.
func New(meta *store.Config, metaPath string, p paths.Paths, mgr TunnelManager, prov Provisioner, plat PlatformInfo) *App {
	return &App{meta: meta, metaPath: metaPath, sshPath: p.SSHConfig, paths: p, mgr: mgr, prov: prov, plat: plat}
}

// HostView is one host flattened for the UI: its ssh-config identity plus the
// app-managed reverse-tunnel/auto-start metadata and live tunnel status.
type HostView struct {
	Alias        string        `json:"alias"`
	HostName     string        `json:"hostname"`
	User         string        `json:"user"`
	Port         string        `json:"port"`
	HasProxy     bool          `json:"has_proxy"`
	ProxyCommand string        `json:"proxy_command"`
	ReversePort  int           `json:"reverse_port"` // app metadata, not ssh config
	HasReverse   bool          `json:"has_reverse"`
	AutoStart    bool          `json:"auto_start"`
	Status       bridge.Status `json:"status"`
}

// Param is one raw config line (for the full per-host editor).
type Param struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// State is a consistent snapshot for a front-end to render.
type State struct {
	ClientAlias   string     `json:"client_alias"`
	Hosts         []HostView `json:"hosts"`
	Platform      string     `json:"platform"`
	XraySupported bool       `json:"xray_supported"`
	XrayInstalled bool       `json:"xray_installed"`
	NodeCount     int        `json:"node_count"`
	LocalSSHOK    bool       `json:"local_ssh_ok"`
}

// State scans ~/.ssh/config fresh and pairs each host with its metadata + status.
func (a *App) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.readConfig()
	st := State{
		ClientAlias:   a.meta.ClientAlias,
		Platform:      a.plat.Name(),
		XraySupported: a.plat.SupportsXray(),
		XrayInstalled: xray.Resolve(a.paths) != "",
		NodeCount:     nodes.Count(a.paths.VlessNodes),
		LocalSSHOK:    a.plat.StatusIncomingSSH(),
	}
	for _, b := range f.Hosts() {
		alias := b.Alias()
		s := b.Summary()
		m := a.meta.Host(alias)
		st.Hosts = append(st.Hosts, HostView{
			Alias:        alias,
			HostName:     s.HostName,
			User:         s.User,
			Port:         s.Port,
			HasProxy:     s.HasProxy,
			ProxyCommand: s.ProxyCommand,
			ReversePort:  m.ReversePort,
			HasReverse:   m.ReversePort > 0,
			AutoStart:    m.AutoStart,
			Status:       a.mgr.Status(alias),
		})
	}
	return st
}

// HostParams returns every config line of a host (for the full editor).
func (a *App) HostParams(alias string) ([]Param, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b := a.readConfig().FindHost(alias)
	if b == nil {
		return nil, errf(ErrNotFound, "no such host")
	}
	var out []Param
	for _, line := range b.Body {
		if k := strings.TrimSpace(line); k == "" || strings.HasPrefix(k, "#") {
			continue
		}
		out = append(out, Param{Key: keyword(line), Value: valueOf(line)})
	}
	return out, nil
}

// SetAlias sanitizes, stores, and persists this machine's name.
func (a *App) SetAlias(alias string) (string, error) {
	alias = sanitizeAlias(alias)
	if alias == "" {
		return "", errf(ErrInvalid, "name must be letters, digits, - or _")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.meta.ClientAlias = alias
	if err := store.Save(a.metaPath, a.meta); err != nil {
		return "", wrap(ErrInternal, err)
	}
	return alias, nil
}

// AddHost writes a new "Host <alias>" block with only the identity keys — no
// IdentityFile/IdentitiesOnly (id_ed25519 is an ssh default), no keepalive/
// RemoteForward (those are ephemeral).
func (a *App) AddHost(alias, hostname, user string, port int) error {
	alias = strings.TrimSpace(alias)
	if alias == "" || strings.ContainsAny(alias, " \t") {
		return errf(ErrInvalid, "host alias must be a single word")
	}
	if strings.TrimSpace(hostname) == "" {
		return errf(ErrInvalid, "host/IP must not be empty")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.readConfig()
	if f.FindHost(alias) != nil {
		return errf(ErrInvalid, "host %q already exists in ~/.ssh/config", alias)
	}
	b := f.AddHost(alias)
	b.Set("HostName", hostname)
	if user != "" {
		b.Set("User", user)
	}
	if port > 0 {
		b.Set("Port", strconv.Itoa(port))
	}
	if a.meta.ClientAlias != "" {
		b.Set("SetEnv", "LC_CLIENT_NAME="+a.meta.ClientAlias)
	}
	return a.writeConfig(f)
}

// RemoveHost stops the host's tunnel, deletes its config block and metadata.
func (a *App) RemoveHost(alias string) error {
	a.mgr.Stop(alias)
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.readConfig()
	if !f.RemoveHost(alias) {
		return errf(ErrNotFound, "no such host")
	}
	a.meta.RemoveHost(alias)
	if err := store.Save(a.metaPath, a.meta); err != nil {
		return wrap(ErrInternal, err)
	}
	return a.writeConfig(f)
}

// SetParam edits one config key on a host (empty value removes it).
func (a *App) SetParam(alias, key, value string) error {
	if strings.TrimSpace(key) == "" {
		return errf(ErrInvalid, "empty parameter key")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.readConfig()
	b := f.FindHost(alias)
	if b == nil {
		return errf(ErrNotFound, "no such host")
	}
	b.Set(key, value)
	return a.writeConfig(f)
}

// SetReverseTunnel records the host's reverse-tunnel port as metadata (port>0)
// or clears it (port<=0). It never writes RemoteForward to ssh config; it also
// strips any leftover app-managed keys from the block so an ordinary
// `ssh <alias>` stays clean.
func (a *App) SetReverseTunnel(alias string, port int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.readConfig()
	b := f.FindHost(alias)
	if b == nil {
		return errf(ErrNotFound, "no such host")
	}
	if stripManaged(b) {
		if err := a.writeConfig(f); err != nil {
			return err
		}
	}
	a.meta.SetReversePort(alias, port)
	return wrap(ErrInternal, store.Save(a.metaPath, a.meta))
}

// SetProxy turns the xray ProxyCommand on/off for a host (it must be in ssh
// config so an ordinary `ssh <alias>` also routes through xray).
func (a *App) SetProxy(alias string, on bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.readConfig()
	b := f.FindHost(alias)
	if b == nil {
		return errf(ErrNotFound, "no such host")
	}
	if on {
		b.Set("ProxyCommand", provision.ProxyCommand())
	} else {
		b.Remove("ProxyCommand")
	}
	stripManaged(b)
	return a.writeConfig(f)
}

// SetAutoStart flags whether a host's tunnel starts when the app launches.
func (a *App) SetAutoStart(alias string, on bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.readConfig().FindHost(alias) == nil {
		return errf(ErrNotFound, "no such host")
	}
	a.meta.SetAutoStart(alias, on)
	return wrap(ErrInternal, store.Save(a.metaPath, a.meta))
}

// StartTunnel launches the reverse tunnel for a host: `ssh -N -R …` with the
// port from metadata. Requires a reverse port to be set.
func (a *App) StartTunnel(alias string) (bridge.Status, error) {
	a.mu.Lock()
	exists := a.readConfig().FindHost(alias) != nil
	rport := a.meta.Host(alias).ReversePort
	a.mu.Unlock()
	if !exists {
		return bridge.Status{}, errf(ErrNotFound, "no such host")
	}
	if rport <= 0 {
		return bridge.Status{}, errf(ErrInvalid, "turn on the reverse tunnel (set a port) for %q first", alias)
	}
	if a.prov != nil {
		if err := a.prov.EnsureKey(); err != nil {
			return bridge.Status{}, errf(ErrInternal, "ssh key: %v", err)
		}
	}
	if err := a.mgr.Start(bridge.Spec{Alias: alias, ReversePort: rport}); err != nil {
		return bridge.Status{}, wrap(ErrInvalid, err)
	}
	return a.mgr.Status(alias), nil
}

// StopTunnel stops the host's tunnel.
func (a *App) StopTunnel(alias string) bridge.Status {
	a.mgr.Stop(alias)
	return a.mgr.Status(alias)
}

// AutoStart brings up every auto-start host's tunnel, best-effort.
func (a *App) AutoStart(warn func(alias string, err error)) {
	a.mu.Lock()
	aliases := a.meta.AutoStartAliases()
	a.mu.Unlock()
	for _, alias := range aliases {
		if _, err := a.StartTunnel(alias); err != nil {
			warn(alias, err)
		}
	}
}

// SetupServer bootstraps the server end of a host over its connection using the
// host's reverse-tunnel port, then authorizes the returned key locally.
func (a *App) SetupServer(alias, password string) (provision.ServerResult, error) {
	if a.prov == nil {
		return provision.ServerResult{}, errf(ErrUnavailable, "provisioning unavailable")
	}
	a.mu.Lock()
	exists := a.readConfig().FindHost(alias) != nil
	rport := a.meta.Host(alias).ReversePort
	clientAlias := a.meta.ClientAlias
	a.mu.Unlock()
	if !exists {
		return provision.ServerResult{}, errf(ErrNotFound, "no such host")
	}
	res, err := a.prov.ServerBootstrap(alias, clientAlias, rport, password)
	return res, wrap(ErrRemote, err)
}

// EnsureLocalSSHD installs/ensures the local ssh server (may need elevation).
func (a *App) EnsureLocalSSHD(disablePassword bool) (running bool, err error) {
	if a.prov == nil {
		return false, errf(ErrUnavailable, "provisioning unavailable")
	}
	if err := a.prov.EnsureLocalSSHD(disablePassword); err != nil {
		return a.plat.StatusIncomingSSH(), wrap(ErrInternal, err)
	}
	return a.plat.StatusIncomingSSH(), nil
}

// InstallXray downloads (or updates) the xray binary, optionally through a
// one-shot http proxy (not persisted).
func (a *App) InstallXray(proxy string) error {
	c := xray.New(strings.TrimSpace(proxy))
	var err error
	if xray.Resolve(a.paths) != "" {
		err = c.Update(a.paths)
	} else {
		err = c.Install(a.paths)
	}
	if err != nil {
		return wrap(ErrInternal, err)
	}
	ensureNodesFile(a.paths)
	return nil
}

// HostUsage reads Claude Code usage from the host's ~/.claude transcripts over a
// plain `ssh <alias>` (the host's ProxyCommand/xray applies automatically; no
// reverse tunnel needed) and returns a priced 1D/7D/30D report. It scans only
// files modified in the last ~31 days.
func (a *App) HostUsage(alias string) (usage.Report, error) {
	a.mu.Lock()
	exists := a.readConfig().FindHost(alias) != nil
	a.mu.Unlock()
	if !exists {
		return usage.Report{}, errf(ErrNotFound, "no such host")
	}
	const remote = `find "$HOME/.claude/projects" -type f -name '*.jsonl' -mtime -31 -exec grep -h output_tokens {} + 2>/dev/null`
	cmd := exec.Command(sshbin.SSH(),
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=20", "-o", "IdentitiesOnly=no",
		alias, remote)
	sysproc.Hide(cmd)
	out, err := cmd.Output()
	if len(out) > 0 {
		// grep may exit non-zero on a final no-match batch even with data — parse it.
		return usage.Parse(out, time.Now()), nil
	}
	if err == nil {
		return usage.Report{}, nil // connected fine, just no usage yet
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ee.ExitCode() == 1 {
			return usage.Report{}, nil // find/grep: no transcripts found
		}
		return usage.Report{}, errf(ErrRemote, "reading usage from %q failed: %s", alias, tailBytes(ee.Stderr, 300))
	}
	return usage.Report{}, errf(ErrRemote, "reading usage from %q: %v", alias, err)
}

func tailBytes(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return s
}

// Nodes returns the raw vless-nodes.txt content and the parsed count.
func (a *App) Nodes() (raw string, count int) {
	if b, err := os.ReadFile(a.paths.VlessNodes); err == nil {
		raw = string(b)
	}
	return raw, nodes.Count(a.paths.VlessNodes)
}

// SetNodes validates and persists the raw nodes file.
func (a *App) SetNodes(raw string) (int, error) {
	for i, line := range strings.Split(raw, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if err := vless.Validate(l); err != nil {
			return 0, errf(ErrInvalid, "line %d: %v", i+1, err)
		}
	}
	if err := a.writeNodesFile(raw); err != nil {
		return 0, wrap(ErrInternal, err)
	}
	return nodes.Count(a.paths.VlessNodes), nil
}

// ---- internals ----

// managedKeys are keys the app controls out-of-band and keeps OUT of ssh config:
// the reverse forward + keepalive are ephemeral start-time args, and
// IdentityFile/IdentitiesOnly just repeat ssh defaults.
var managedKeys = []string{
	"RemoteForward", "IdentityFile", "IdentitiesOnly",
	"ServerAliveInterval", "ServerAliveCountMax", "ForwardAgent",
}

// stripManaged removes managed keys from a block, reporting whether it changed.
func stripManaged(b *sshcfg.Block) bool {
	changed := false
	for _, k := range managedKeys {
		if b.Has(k) {
			b.Remove(k)
			changed = true
		}
	}
	return changed
}

func (a *App) readConfig() *sshcfg.File {
	raw, _ := os.ReadFile(a.sshPath)
	return sshcfg.Parse(string(raw))
}

func (a *App) writeConfig(f *sshcfg.File) error {
	if err := os.MkdirAll(a.paths.SSHDir, 0o700); err != nil {
		return wrap(ErrInternal, err)
	}
	if err := os.WriteFile(a.sshPath, []byte(f.String()), 0o600); err != nil {
		return wrap(ErrInternal, err)
	}
	return nil
}

func ensureNodesFile(p paths.Paths) error {
	if _, err := os.Stat(p.VlessNodes); err == nil {
		return nil
	}
	if err := os.MkdirAll(p.RCConfigDir, 0o755); err != nil {
		return err
	}
	body := "# vless nodes for the remote-claude tunnel — one vless:// URL per line.\n" +
		"# Lines starting with # and blank lines are ignored.\n"
	return os.WriteFile(p.VlessNodes, []byte(body), 0o600)
}

func (a *App) writeNodesFile(raw string) error {
	if err := os.MkdirAll(a.paths.RCConfigDir, 0o755); err != nil {
		return err
	}
	if raw != "" && !strings.HasSuffix(raw, "\n") {
		raw += "\n"
	}
	return os.WriteFile(a.paths.VlessNodes, []byte(raw), 0o600)
}

func keyword(line string) string {
	s := strings.TrimLeft(line, " \t")
	if i := strings.IndexAny(s, " \t="); i >= 0 {
		return s[:i]
	}
	return s
}

func valueOf(line string) string {
	s := strings.TrimLeft(line, " \t")
	i := strings.IndexAny(s, " \t=")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(strings.TrimLeft(s[i:], " \t="))
}

func sanitizeAlias(in string) string {
	in = strings.TrimSpace(in)
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		}
	}
	return b.String()
}
