package store

import (
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
