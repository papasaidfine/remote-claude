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
	if c == nil || c.ClientAlias != "" || len(c.AutoStart) != 0 {
		t.Fatalf("want empty config, got %+v", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := &Config{ClientAlias: "lisa-laptop"}
	c.SetAutoStart("remote-claude", true)
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ClientAlias != "lisa-laptop" || !got.IsAutoStart("remote-claude") {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestSetAutoStart(t *testing.T) {
	c := &Config{}
	c.SetAutoStart("a", true)
	c.SetAutoStart("a", true) // idempotent
	c.SetAutoStart("b", true)
	if len(c.AutoStart) != 2 {
		t.Fatalf("want 2 auto-start, got %v", c.AutoStart)
	}
	c.SetAutoStart("a", false)
	if c.IsAutoStart("a") || !c.IsAutoStart("b") {
		t.Fatalf("unexpected auto-start set: %v", c.AutoStart)
	}
}
