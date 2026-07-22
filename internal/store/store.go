// Package store persists the app's own metadata — this machine's name and which
// ssh hosts to auto-start. The hosts themselves live in ~/.ssh/config (see
// package sshcfg); this file only holds what ssh config can't express.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/papasaidfine/remote-claude/internal/paths"
)

// Config is the persisted app metadata document.
type Config struct {
	ClientAlias string   `json:"client_alias"`
	AutoStart   []string `json:"auto_start"` // ssh host aliases to start on launch
}

// Path returns the config.json location for the resolved paths.
func Path(p paths.Paths) string {
	return filepath.Join(p.RCConfigDir, "config.json")
}

// Load reads the metadata file. A missing file yields an empty (non-nil) Config.
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

// IsAutoStart reports whether alias is flagged to start on launch.
func (c *Config) IsAutoStart(alias string) bool {
	for _, a := range c.AutoStart {
		if a == alias {
			return true
		}
	}
	return false
}

// SetAutoStart adds or removes alias from the auto-start list.
func (c *Config) SetAutoStart(alias string, on bool) {
	has := c.IsAutoStart(alias)
	switch {
	case on && !has:
		c.AutoStart = append(c.AutoStart, alias)
	case !on && has:
		out := c.AutoStart[:0]
		for _, a := range c.AutoStart {
			if a != alias {
				out = append(out, a)
			}
		}
		c.AutoStart = out
	}
}
