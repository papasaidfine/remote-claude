// Package store is the persistent settings model for the app: this machine's
// alias plus the list of configured hosts. It is a pure data + file-IO layer —
// it knows nothing about HTTP, ssh, or the tunnel, so it stays trivially
// testable and front-end-agnostic.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/papasaidfine/remote-claude/internal/paths"
)

// Defaults applied by Normalize when fields are left zero.
const (
	DefaultPort        = 22
	DefaultReversePort = 2222
)

// Host is one configured server: how to reach it and how its reverse tunnel is
// shaped. ReversePort is the server-side loopback port the bridge binds so the
// server can ssh back into this machine.
type Host struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	HostName    string `json:"hostname"`
	User        string `json:"user"`
	Port        int    `json:"port"`
	UseXray     bool   `json:"use_xray"`
	ReversePort int    `json:"reverse_port"`
	AutoStart   bool   `json:"auto_start"`
}

// Config is the whole persisted document.
type Config struct {
	ClientAlias string `json:"client_alias"`
	Hosts       []Host `json:"hosts"`
}

// Path returns the config.json location for the resolved paths.
func Path(p paths.Paths) string {
	return filepath.Join(p.RCConfigDir, "config.json")
}

// Load reads the config file. A missing file is not an error — it yields an
// empty (but non-nil) Config so first-run just works.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &c, nil
}

// Save writes the config atomically (temp file + rename) with 0600 perms.
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
	defer os.Remove(tmpName) // no-op after a successful rename
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

// Normalize fills zero-value defaults on every host. Returns the same pointer
// for convenience.
func (c *Config) Normalize() *Config {
	for i := range c.Hosts {
		h := &c.Hosts[i]
		if h.Port == 0 {
			h.Port = DefaultPort
		}
		if h.ReversePort == 0 {
			h.ReversePort = DefaultReversePort
		}
	}
	return c
}

// Find returns a pointer to the host with the given id, or nil.
func (c *Config) Find(id string) *Host {
	for i := range c.Hosts {
		if c.Hosts[i].ID == id {
			return &c.Hosts[i]
		}
	}
	return nil
}

// Validate checks the required fields of a host.
func (h *Host) Validate() error {
	if strings.TrimSpace(h.Name) == "" {
		return fmt.Errorf("name must not be empty")
	}
	if strings.TrimSpace(h.HostName) == "" {
		return fmt.Errorf("host/IP must not be empty")
	}
	if strings.TrimSpace(h.User) == "" {
		return fmt.Errorf("user must not be empty")
	}
	if h.Port <= 0 || h.Port > 65535 {
		return fmt.Errorf("ssh port must be 1..65535")
	}
	if h.ReversePort <= 0 || h.ReversePort > 65535 {
		return fmt.Errorf("reverse port must be 1..65535")
	}
	return nil
}

// Add validates and appends a host, assigning it a fresh ID and defaults.
// Returns the stored host (with its ID) on success.
func (c *Config) Add(h Host) (Host, error) {
	if h.Port == 0 {
		h.Port = DefaultPort
	}
	if h.ReversePort == 0 {
		h.ReversePort = DefaultReversePort
	}
	if err := h.Validate(); err != nil {
		return Host{}, err
	}
	if err := c.checkReversePortFree(h.ReversePort, ""); err != nil {
		return Host{}, err
	}
	h.ID = newID()
	c.Hosts = append(c.Hosts, h)
	return h, nil
}

// Update replaces the fields of an existing host (matched by in.ID), keeping the
// ID. Returns an error if the id is unknown or the new values are invalid.
func (c *Config) Update(in Host) error {
	if in.Port == 0 {
		in.Port = DefaultPort
	}
	if in.ReversePort == 0 {
		in.ReversePort = DefaultReversePort
	}
	if err := in.Validate(); err != nil {
		return err
	}
	if err := c.checkReversePortFree(in.ReversePort, in.ID); err != nil {
		return err
	}
	cur := c.Find(in.ID)
	if cur == nil {
		return fmt.Errorf("no host with id %q", in.ID)
	}
	*cur = in
	return nil
}

// Remove deletes the host with the given id. Returns whether it existed.
func (c *Config) Remove(id string) bool {
	for i := range c.Hosts {
		if c.Hosts[i].ID == id {
			c.Hosts = append(c.Hosts[:i], c.Hosts[i+1:]...)
			return true
		}
	}
	return false
}

// checkReversePortFree rejects a reverse port already used by a different host
// (exceptID is the host allowed to keep its own port during an update).
func (c *Config) checkReversePortFree(port int, exceptID string) error {
	for i := range c.Hosts {
		if c.Hosts[i].ID == exceptID {
			continue
		}
		if c.Hosts[i].ReversePort == port {
			return fmt.Errorf("reverse port %d already used by host %q", port, c.Hosts[i].Name)
		}
	}
	return nil
}

// newID returns a short random hex id.
func newID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should not fail; a fixed prefix keeps callers working.
		return "host000000"
	}
	return hex.EncodeToString(b[:])
}
