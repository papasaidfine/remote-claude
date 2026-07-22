// Package core is the front-end-agnostic application façade. The host list is
// the user's ~/.ssh/config (scanned via package sshcfg); editing a host writes
// back to that file. A small metadata file (package store) holds only what ssh
// config can't express — this machine's name and which hosts auto-start. Config
// is config; starting a tunnel is a separate action that just launches
// `ssh -N <alias>` per that host's config.
package core

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/nodes"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/sshcfg"
	"github.com/papasaidfine/remote-claude/internal/store"
	"github.com/papasaidfine/remote-claude/internal/vless"
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

// HostView is one ssh-config host flattened for the UI, plus live state.
type HostView struct {
	sshcfg.Summary
	Status    bridge.Status `json:"status"`
	AutoStart bool          `json:"auto_start"`
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
	NodeCount     int        `json:"node_count"`
	LocalSSHOK    bool       `json:"local_ssh_ok"`
}

// State scans ~/.ssh/config fresh and pairs each host with its tunnel status.
func (a *App) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.readConfig()
	st := State{
		ClientAlias:   a.meta.ClientAlias,
		Platform:      a.plat.Name(),
		XraySupported: a.plat.SupportsXray(),
		NodeCount:     nodes.Count(a.paths.VlessNodes),
		LocalSSHOK:    a.plat.StatusIncomingSSH(),
	}
	for _, b := range f.Hosts() {
		alias := b.Alias()
		st.Hosts = append(st.Hosts, HostView{
			Summary:   b.Summary(),
			Status:    a.mgr.Status(alias),
			AutoStart: a.meta.IsAutoStart(alias),
		})
	}
	return st
}

// HostParams returns every config line of a host (known + unknown), for the
// full editor.
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

// AddHost writes a new "Host <alias>" block with the basics.
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
	b.Set("IdentityFile", "~/.ssh/"+paths.KeyName)
	b.Set("IdentitiesOnly", "yes")
	if a.meta.ClientAlias != "" {
		b.Set("SetEnv", "LC_CLIENT_NAME="+a.meta.ClientAlias)
	}
	return a.writeConfig(f)
}

// RemoveHost stops the host's tunnel, deletes its block, and clears its
// auto-start flag.
func (a *App) RemoveHost(alias string) error {
	a.mgr.Stop(alias)
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.readConfig()
	if !f.RemoveHost(alias) {
		return errf(ErrNotFound, "no such host")
	}
	a.meta.SetAutoStart(alias, false)
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

// SetReverseTunnel sets (port>0) or clears (port<=0) the host's RemoteForward —
// server loopback :port -> this machine's sshd. This is config, not launching.
func (a *App) SetReverseTunnel(alias string, port int) error {
	val := ""
	if port > 0 {
		val = fmt.Sprintf("127.0.0.1:%d 127.0.0.1:22", port)
	}
	return a.SetParam(alias, "RemoteForward", val)
}

// SetProxy turns the xray ProxyCommand on (relay) or off for a host.
func (a *App) SetProxy(alias string, on bool) error {
	val := ""
	if on {
		val = provision.ProxyCommand()
	}
	return a.SetParam(alias, "ProxyCommand", val)
}

// StartTunnel launches the reverse tunnel for a host per its config
// (`ssh -N <alias>`). Ensures the local key exists first.
func (a *App) StartTunnel(alias string) (bridge.Status, error) {
	a.mu.Lock()
	exists := a.readConfig().FindHost(alias) != nil
	a.mu.Unlock()
	if !exists {
		return bridge.Status{}, errf(ErrNotFound, "no such host")
	}
	if a.prov != nil {
		if err := a.prov.EnsureKey(); err != nil {
			return bridge.Status{}, errf(ErrInternal, "ssh key: %v", err)
		}
	}
	if err := a.mgr.Start(bridge.Spec{Alias: alias}); err != nil {
		return bridge.Status{}, wrap(ErrInvalid, err)
	}
	return a.mgr.Status(alias), nil
}

// StopTunnel stops the host's tunnel.
func (a *App) StopTunnel(alias string) bridge.Status {
	a.mgr.Stop(alias)
	return a.mgr.Status(alias)
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

// AutoStart brings up every auto-start host's tunnel, best-effort.
func (a *App) AutoStart(warn func(alias string, err error)) {
	a.mu.Lock()
	aliases := append([]string(nil), a.meta.AutoStart...)
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
	b := a.readConfig().FindHost(alias)
	clientAlias := a.meta.ClientAlias
	a.mu.Unlock()
	if b == nil {
		return provision.ServerResult{}, errf(ErrNotFound, "no such host")
	}
	rport, _ := strconv.Atoi(b.Summary().ReversePort)
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

func (a *App) writeNodesFile(raw string) error {
	if err := os.MkdirAll(a.paths.RCConfigDir, 0o755); err != nil {
		return err
	}
	if raw != "" && !strings.HasSuffix(raw, "\n") {
		raw += "\n"
	}
	return os.WriteFile(a.paths.VlessNodes, []byte(raw), 0o600)
}

// keyword/valueOf mirror sshcfg's line parsing for HostParams display.
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
