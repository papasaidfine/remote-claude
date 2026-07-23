// Package store persists the app's own metadata: this machine's name, and the
// per-host settings that must NOT live in ~/.ssh/config (the reverse-tunnel port
// and auto-start). Those are applied as ephemeral ssh args when a tunnel starts,
// not written to the config — so an ordinary `ssh <host>` never carries them.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/papasaidfine/remote-claude/internal/paths"
)

// HostMeta is the app-managed, non-ssh-config settings for one host.
type HostMeta struct {
	ReversePort int  `json:"reverse_port,omitempty"`
	AutoStart   bool `json:"auto_start,omitempty"`
}

// Config is the persisted metadata document.
type Config struct {
	ClientAlias string              `json:"client_alias"`
	Lang        string              `json:"lang,omitempty"` // UI language code ("" = auto-detect)
	Hosts       map[string]HostMeta `json:"hosts,omitempty"`
}

// Path returns the config.json location for the resolved paths.
func Path(p paths.Paths) string {
	return filepath.Join(p.RCConfigDir, "config.json")
}

// Load reads the metadata file. It is deliberately tolerant: a missing,
// unreadable, or unparseable file (e.g. left over from an older version with a
// different shape) yields a fresh empty Config rather than an error — a bad
// config.json must never stop the app from launching. It will be replaced on
// the next save.
func Load(path string) (*Config, error) {
	fresh := &Config{Hosts: map[string]HostMeta{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fresh, nil
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return fresh, nil
	}
	if c.Hosts == nil {
		c.Hosts = map[string]HostMeta{}
	}
	return &c, nil
}

// Save writes the metadata atomically (temp file + rename) with 0600 perms.
func Save(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Host returns the metadata for alias (zero value if none).
func (c *Config) Host(alias string) HostMeta { return c.Hosts[alias] }

func (c *Config) ensure() {
	if c.Hosts == nil {
		c.Hosts = map[string]HostMeta{}
	}
}

// SetReversePort records (port>0) or clears (port<=0) the host's reverse-tunnel
// port.
func (c *Config) SetReversePort(alias string, port int) {
	c.ensure()
	m := c.Hosts[alias]
	m.ReversePort = port
	c.putOrDrop(alias, m)
}

// SetAutoStart flags whether alias starts on launch.
func (c *Config) SetAutoStart(alias string, on bool) {
	c.ensure()
	m := c.Hosts[alias]
	m.AutoStart = on
	c.putOrDrop(alias, m)
}

// RemoveHost drops all metadata for alias.
func (c *Config) RemoveHost(alias string) { delete(c.Hosts, alias) }

// IsAutoStart reports whether alias is flagged to start on launch.
func (c *Config) IsAutoStart(alias string) bool { return c.Hosts[alias].AutoStart }

// AutoStartAliases returns the aliases flagged to start on launch, sorted.
func (c *Config) AutoStartAliases() []string {
	var out []string
	for alias, m := range c.Hosts {
		if m.AutoStart {
			out = append(out, alias)
		}
	}
	sort.Strings(out)
	return out
}

// putOrDrop stores m, or deletes the entry entirely when it's all zero (keeps
// config.json tidy).
func (c *Config) putOrDrop(alias string, m HostMeta) {
	if m == (HostMeta{}) {
		delete(c.Hosts, alias)
		return
	}
	c.Hosts[alias] = m
}
