// Package provision performs the side-effecting setup the app drives: the
// client side (ssh key, the managed ssh-config block with SetEnv + optional
// xray ProxyCommand, but NO RemoteForward — the bridge owns the reverse
// forward) and, over the tunnel connection, the server side.
package provision

import (
	"fmt"
	"os"

	"github.com/papasaidfine/remote-claude/internal/fsutil"
	"github.com/papasaidfine/remote-claude/internal/keys"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/platform"
	"github.com/papasaidfine/remote-claude/internal/sshconfig"
	"github.com/papasaidfine/remote-claude/internal/store"
	"github.com/papasaidfine/remote-claude/internal/xray"
)

// Client runs provisioning against the resolved paths and platform.
type Client struct {
	P    paths.Paths
	Plat platform.Platform
}

// New builds a provisioning Client.
func New(p paths.Paths, plat platform.Platform) *Client {
	return &Client{P: p, Plat: plat}
}

// proxyValue is the ProxyCommand the tool writes: "<self>" relay %h %p.
func proxyValue() string {
	return fmt.Sprintf("%q relay %%h %%p", paths.SelfExe())
}

// EnsureClient makes the local side ready for a tunnel to h: the ssh key exists,
// and the managed "Host remote-claude" block points at h with SetEnv
// LC_CLIENT_NAME=<clientAlias>, the xray ProxyCommand when h.UseXray, and no
// RemoteForward (the bridge adds -R on its own command line).
func (c *Client) EnsureClient(h store.Host, clientAlias string) error {
	if err := h.Validate(); err != nil {
		return err
	}
	if _, err := keys.Ensure(c.P, c.Plat.SetStrictPerms); err != nil {
		return fmt.Errorf("ensure ssh key: %w", err)
	}
	if h.UseXray {
		if xray.Resolve(c.P) == "" {
			return fmt.Errorf("xray is enabled for host %q but the xray binary is not installed yet", h.Name)
		}
		if err := ensureNodesFile(c.P); err != nil {
			return err
		}
	}
	o := sshconfig.BlockOpts{
		Host:       h.HostName,
		User:       h.User,
		Port:       h.Port,
		RevPort:    0, // the bridge owns the reverse forward
		ClientName: clientAlias,
	}
	if h.UseXray {
		o.Proxy = proxyValue()
	}
	_, err := sshconfig.WriteFile(c.P.SSHConfig, o, sshconfig.Deps{
		Force:    true, // GUI context: no interactive confirm
		SetPerms: func(p string) error { return c.Plat.SetStrictPerms(p, false) },
		Backup:   func(p string) (string, error) { return fsutil.Backup(p) },
	})
	return err
}

// EnsureLocalSSHD installs/enables and hardens the local sshd so the server can
// ssh back through the tunnel. It may require elevation (sudo) and is a system
// change, so the UI calls it explicitly rather than on every start.
func (c *Client) EnsureLocalSSHD(disablePassword bool) error {
	if err := c.Plat.RequireElevation(); err != nil {
		return err
	}
	return c.Plat.EnsureIncomingSSH(disablePassword)
}

func ensureNodesFile(p paths.Paths) error {
	if _, err := os.Stat(p.VlessNodes); err == nil {
		return nil
	}
	if err := os.MkdirAll(p.RCConfigDir, 0o755); err != nil {
		return err
	}
	body := "# vless nodes for the remote-claude tunnel — one vless:// URL per line.\n" +
		"# Lines starting with # and blank lines are ignored.\n" +
		"# Every connection picks a random node; edits take effect on the next connect.\n"
	return os.WriteFile(p.VlessNodes, []byte(body), 0o600)
}
