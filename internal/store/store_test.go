package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if c == nil || c.ClientAlias != "" || len(c.Hosts) != 0 {
		t.Fatalf("want empty config, got %+v", c)
	}
}

func TestLoadCorruptOrOldShapeReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	// An older-version file where "hosts" was an array, not a map.
	old := filepath.Join(dir, "old.json")
	os.WriteFile(old, []byte(`{"client_alias":"x","hosts":[{"id":"a"}]}`), 0o600)
	if c, err := Load(old); err != nil || c == nil || len(c.Hosts) != 0 {
		t.Fatalf("incompatible config must load as empty, got err=%v c=%+v", err, c)
	}
	// Garbage.
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("not json at all"), 0o600)
	if c, err := Load(bad); err != nil || c == nil {
		t.Fatalf("garbage config must load as empty, got err=%v c=%+v", err, c)
	}
}

func TestReversePortAndAutoStartRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := &Config{ClientAlias: "lc-pc"}
	c.SetReversePort("remote-claude", 2222)
	c.SetAutoStart("remote-claude", true)
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ClientAlias != "lc-pc" || got.Host("remote-claude").ReversePort != 2222 || !got.IsAutoStart("remote-claude") {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestZeroMetaIsDropped(t *testing.T) {
	c := &Config{}
	c.SetReversePort("h", 2222)
	c.SetAutoStart("h", false)
	c.SetReversePort("h", 0) // now all-zero → entry removed
	if _, ok := c.Hosts["h"]; ok {
		t.Errorf("all-zero host meta should be dropped, got %+v", c.Hosts)
	}
}

func TestAutoStartAliases(t *testing.T) {
	c := &Config{}
	c.SetAutoStart("b", true)
	c.SetAutoStart("a", true)
	c.SetAutoStart("c", false)
	got := c.AutoStartAliases()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("auto-start aliases = %v, want [a b]", got)
	}
}

func TestRemoveHost(t *testing.T) {
	c := &Config{}
	c.SetReversePort("h", 2222)
	c.RemoveHost("h")
	if c.Host("h").ReversePort != 0 {
		t.Error("host meta not removed")
	}
}
