// Package provision performs the side-effecting setup the app drives: ensuring
// the local ssh key, installing the local sshd, and bootstrapping the server end
// over the tunnel connection (see server.go). Hosts themselves live in
// ~/.ssh/config, edited via package sshcfg — provision no longer writes host
// blocks.
package provision

import (
	"fmt"

	"github.com/papasaidfine/remote-claude/internal/keys"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/platform"
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

// proxyValue is the ProxyCommand value the app writes into a host block:
// "<self>" relay %h %p. Exposed for the config editor's "route through xray".
func ProxyCommand() string {
	return fmt.Sprintf("%q relay %%h %%p", paths.SelfExe())
}

// EnsureKey makes sure the local ssh key (local → server hop) exists.
func (c *Client) EnsureKey() error {
	if _, err := keys.Ensure(c.P, c.Plat.SetStrictPerms); err != nil {
		return fmt.Errorf("ensure ssh key: %w", err)
	}
	return nil
}

// EnsureLocalSSHD installs/enables and hardens the local sshd so the server can
// ssh back through the tunnel. It may require elevation (sudo).
func (c *Client) EnsureLocalSSHD(disablePassword bool) error {
	if err := c.Plat.RequireElevation(); err != nil {
		return err
	}
	return c.Plat.EnsureIncomingSSH(disablePassword)
}
