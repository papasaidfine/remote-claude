// Package core is the front-end-agnostic application façade. It owns the
// loaded config and coordinates the engine packages (store + bridge +
// provision) behind one lock, so every front-end — the local web UI, a native
// tray shell — calls the same orchestration instead of re-implementing it.
package core

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/nodes"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/store"
	"github.com/papasaidfine/remote-claude/internal/vless"
)

// Provisioner performs the side-effecting setup the app drives: client setup
// before a tunnel starts, one-shot server bootstrap over the connection, and
// local sshd. Injected so App stays testable; may be nil (setup unavailable).
type Provisioner interface {
	EnsureClient(h store.Host, clientAlias string) error
	ServerBootstrap(h store.Host, clientAlias, password string) (provision.ServerResult, error)
	EnsureLocalSSHD(disablePassword bool) error
}

// TunnelManager is the subset of *bridge.Manager the app drives (seam for
// tests).
type TunnelManager interface {
	Start(spec bridge.Spec) error
	Stop(hostID string)
	Status(hostID string) bridge.Status
}

// PlatformInfo is the read-only platform surface the app reports.
type PlatformInfo interface {
	Name() string
	SupportsXray() bool
	StatusIncomingSSH() bool
}

// App is the live application state and its orchestration methods. All
// methods are safe for concurrent use.
type App struct {
	mu      sync.Mutex
	cfg     *store.Config
	cfgPath string

	paths paths.Paths
	mgr   TunnelManager
	prov  Provisioner
	plat  PlatformInfo
}

// New builds an App around an already-loaded config (which it normalizes).
func New(cfg *store.Config, cfgPath string, p paths.Paths, mgr TunnelManager, prov Provisioner, plat PlatformInfo) *App {
	cfg.Normalize()
	return &App{cfg: cfg, cfgPath: cfgPath, paths: p, mgr: mgr, prov: prov, plat: plat}
}

// State is a consistent snapshot of everything a front-end renders. JSON tags
// are a convenience for front-ends that serialize it verbatim (the web UI).
type State struct {
	ClientAlias   string                   `json:"client_alias"`
	Hosts         []store.Host             `json:"hosts"`
	Statuses      map[string]bridge.Status `json:"statuses"`
	Platform      string                   `json:"platform"`
	XraySupported bool                     `json:"xray_supported"`
	NodeCount     int                      `json:"node_count"`
	LocalSSHOK    bool                     `json:"local_ssh_ok"`
}

// State snapshots the config, per-host tunnel statuses, and platform facts.
func (a *App) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := State{
		ClientAlias:   a.cfg.ClientAlias,
		Hosts:         append([]store.Host(nil), a.cfg.Hosts...),
		Statuses:      map[string]bridge.Status{},
		Platform:      a.plat.Name(),
		XraySupported: a.plat.SupportsXray(),
		NodeCount:     nodes.Count(a.paths.VlessNodes),
		LocalSSHOK:    a.plat.StatusIncomingSSH(),
	}
	for _, h := range a.cfg.Hosts {
		st.Statuses[h.ID] = a.mgr.Status(h.ID)
	}
	return st
}

// SetAlias sanitizes, stores, and persists this machine's alias, returning
// the form actually stored.
func (a *App) SetAlias(alias string) (string, error) {
	alias = sanitizeAlias(alias)
	if alias == "" {
		return "", errf(ErrInvalid, "alias must be non-empty (letters, digits, -, _)")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.ClientAlias = alias
	if err := a.save(); err != nil {
		return "", wrap(ErrInternal, err)
	}
	return alias, nil
}

// AddHost validates, stores, and persists a new host, returning it with its
// assigned ID.
func (a *App) AddHost(h store.Host) (store.Host, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	stored, err := a.cfg.Add(h)
	if err != nil {
		return store.Host{}, wrap(ErrInvalid, err)
	}
	if err := a.save(); err != nil {
		return store.Host{}, wrap(ErrInternal, err)
	}
	return stored, nil
}

// UpdateHost replaces the fields of the host matching h.ID and persists.
func (a *App) UpdateHost(h store.Host) (store.Host, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.Find(h.ID) == nil {
		return store.Host{}, errf(ErrNotFound, "no such host")
	}
	if err := a.cfg.Update(h); err != nil {
		return store.Host{}, wrap(ErrInvalid, err)
	}
	if err := a.save(); err != nil {
		return store.Host{}, wrap(ErrInternal, err)
	}
	return h, nil
}

// DeleteHost stops the host's tunnel (if any), removes it, and persists.
func (a *App) DeleteHost(id string) error {
	a.mgr.Stop(id)
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.cfg.Remove(id) {
		return errf(ErrNotFound, "no such host")
	}
	return wrap(ErrInternal, a.save())
}

// StartTunnel runs client provisioning (when wired) and starts the reverse
// tunnel for the host, returning its status.
func (a *App) StartTunnel(id string) (bridge.Status, error) {
	host, alias, err := a.findHost(id)
	if err != nil {
		return bridge.Status{}, err
	}
	if a.prov != nil {
		if err := a.prov.EnsureClient(host, alias); err != nil {
			return bridge.Status{}, errf(ErrInternal, "client setup failed: %v", err)
		}
	}
	spec := bridge.Spec{HostID: host.ID, Alias: paths.Alias, ReversePort: host.ReversePort}
	if err := a.mgr.Start(spec); err != nil {
		return bridge.Status{}, wrap(ErrInvalid, err)
	}
	return a.mgr.Status(host.ID), nil
}

// StopTunnel stops the host's tunnel and returns its status.
func (a *App) StopTunnel(id string) bridge.Status {
	a.mgr.Stop(id)
	return a.mgr.Status(id)
}

// SetupServer bootstraps the server end over the outbound connection and
// authorizes its connect-back key locally. Password is optional; empty means
// key/agent auth.
func (a *App) SetupServer(id, password string) (provision.ServerResult, error) {
	if a.prov == nil {
		return provision.ServerResult{}, errf(ErrUnavailable, "provisioning unavailable")
	}
	host, alias, err := a.findHost(id)
	if err != nil {
		return provision.ServerResult{}, err
	}
	res, err := a.prov.ServerBootstrap(host, alias, password)
	return res, wrap(ErrRemote, err)
}

// EnsureLocalSSHD installs/ensures the local ssh server (may need elevation)
// and reports whether sshd is running afterwards.
func (a *App) EnsureLocalSSHD(disablePassword bool) (running bool, err error) {
	if a.prov == nil {
		return false, errf(ErrUnavailable, "provisioning unavailable")
	}
	if err := a.prov.EnsureLocalSSHD(disablePassword); err != nil {
		return a.plat.StatusIncomingSSH(), wrap(ErrInternal, err)
	}
	return a.plat.StatusIncomingSSH(), nil
}

// AutoStart brings up tunnels for every host flagged AutoStart, best-effort.
// Failures go to warn (never fatal) so the app still comes up.
func (a *App) AutoStart(warn func(host store.Host, err error)) {
	a.mu.Lock()
	hosts := append([]store.Host(nil), a.cfg.Hosts...)
	alias := a.cfg.ClientAlias
	a.mu.Unlock()
	for _, h := range hosts {
		if !h.AutoStart {
			continue
		}
		if a.prov != nil {
			if err := a.prov.EnsureClient(h, alias); err != nil {
				warn(h, fmt.Errorf("client setup failed: %w", err))
				continue
			}
		}
		if err := a.mgr.Start(bridge.Spec{HostID: h.ID, Alias: paths.Alias, ReversePort: h.ReversePort}); err != nil {
			warn(h, err)
		}
	}
}

// Nodes returns the raw vless-nodes.txt content ("" if absent) and the parsed
// node count.
func (a *App) Nodes() (raw string, count int) {
	if b, err := os.ReadFile(a.paths.VlessNodes); err == nil {
		raw = string(b)
	}
	return raw, nodes.Count(a.paths.VlessNodes)
}

// SetNodes validates and persists the raw nodes file (one vless:// URL per
// line; blank lines and #-comments allowed) and returns the new node count.
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

// findHost snapshots a host and the client alias under the lock, so slow
// provisioning/ssh work never runs while the config is locked.
func (a *App) findHost(id string) (store.Host, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.cfg.Find(id)
	if h == nil {
		return store.Host{}, "", errf(ErrNotFound, "no such host")
	}
	return *h, a.cfg.ClientAlias, nil
}

// save persists cfg; caller holds a.mu.
func (a *App) save() error { return store.Save(a.cfgPath, a.cfg) }

// writeNodesFile persists the vless nodes file (raw text, newline-terminated).
func (a *App) writeNodesFile(raw string) error {
	if err := os.MkdirAll(a.paths.RCConfigDir, 0o755); err != nil {
		return err
	}
	if raw != "" && !strings.HasSuffix(raw, "\n") {
		raw += "\n"
	}
	return os.WriteFile(a.paths.VlessNodes, []byte(raw), 0o600)
}

// sanitizeAlias keeps letters, digits, dash, underscore; lowercases the rest.
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
