package store

import (
	"path/filepath"
	"testing"
)

func sampleHost() Host {
	return Host{Name: "workbox", HostName: "srv.example.com", User: "dev"}
}

func TestLoadMissingReturnsEmpty(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if c == nil || len(c.Hosts) != 0 || c.ClientAlias != "" {
		t.Fatalf("want empty config, got %+v", c)
	}
}

func TestAddAppliesDefaultsAndID(t *testing.T) {
	c := &Config{}
	h, err := c.Add(sampleHost())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if h.ID == "" {
		t.Fatal("Add did not assign an ID")
	}
	if h.Port != DefaultPort || h.ReversePort != DefaultReversePort {
		t.Fatalf("defaults not applied: port=%d rport=%d", h.Port, h.ReversePort)
	}
	if len(c.Hosts) != 1 {
		t.Fatalf("host not stored")
	}
}

func TestAddValidation(t *testing.T) {
	c := &Config{}
	if _, err := c.Add(Host{Name: "x"}); err == nil {
		t.Fatal("expected validation error for missing hostname/user")
	}
}

func TestReversePortCollision(t *testing.T) {
	c := &Config{}
	if _, err := c.Add(Host{Name: "a", HostName: "h1", User: "u", ReversePort: 2222}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	_, err := c.Add(Host{Name: "b", HostName: "h2", User: "u", ReversePort: 2222})
	if err == nil {
		t.Fatal("expected reverse-port collision error")
	}
}

func TestUpdateKeepsIDAndAllowsSameReversePort(t *testing.T) {
	c := &Config{}
	h, _ := c.Add(sampleHost())
	h.HostName = "new.example.com"
	if err := c.Update(h); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := c.Find(h.ID); got == nil || got.HostName != "new.example.com" {
		t.Fatalf("update not applied: %+v", got)
	}
}

func TestUpdateUnknownID(t *testing.T) {
	c := &Config{}
	err := c.Update(Host{ID: "deadbeef", Name: "x", HostName: "h", User: "u", Port: 22, ReversePort: 2222})
	if err == nil {
		t.Fatal("expected error updating unknown id")
	}
}

func TestRemove(t *testing.T) {
	c := &Config{}
	h, _ := c.Add(sampleHost())
	if !c.Remove(h.ID) {
		t.Fatal("Remove returned false for existing host")
	}
	if c.Remove(h.ID) {
		t.Fatal("Remove returned true for already-removed host")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := &Config{ClientAlias: "lisa-laptop"}
	if _, err := c.Add(sampleHost()); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ClientAlias != "lisa-laptop" || len(got.Hosts) != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Hosts[0].ID != c.Hosts[0].ID {
		t.Fatalf("id not persisted: %q vs %q", got.Hosts[0].ID, c.Hosts[0].ID)
	}
}

func TestNormalizeFillsDefaults(t *testing.T) {
	c := &Config{Hosts: []Host{{Name: "a", HostName: "h", User: "u"}}}
	c.Normalize()
	if c.Hosts[0].Port != DefaultPort || c.Hosts[0].ReversePort != DefaultReversePort {
		t.Fatalf("Normalize did not fill defaults: %+v", c.Hosts[0])
	}
}
