package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/store"
)

// writeConfigFile drops a literal ~/.ssh/config in place and returns an App over it.
func appOverConfig(t *testing.T, body string) *App {
	t.Helper()
	dir := t.TempDir()
	ssh := filepath.Join(dir, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(ssh, "config")
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	p := paths.Paths{
		SSHDir:      ssh,
		SSHConfig:   cfgPath,
		RCConfigDir: filepath.Join(dir, "rc"),
		VlessNodes:  filepath.Join(dir, "rc", "vless-nodes.txt"),
	}
	return New(&store.Config{}, filepath.Join(dir, "config.json"), p,
		&fakeMgr{}, nil, fakePlat{})
}

// A repeated "Host <alias>" is one host, not two: ssh resolves the alias to a
// single destination, and every method here (FindHost, SetParam, RemoveHost)
// acts on the first block. Reporting it twice put the same card object into the
// list twice — Fyne can only draw an object in one place, so the duplicate slot
// rendered as a host-sized hole in the middle of the list.
func TestStateReportsARepeatedHostAliasOnce(t *testing.T) {
	app := appOverConfig(t, `Host alpha
  HostName 10.0.0.1
  User dev

Host beta
  HostName 10.0.0.2
  User dev

Host alpha
  HostName 10.0.0.9
  Port 2200
`)
	hosts := app.State().Hosts

	var aliases []string
	for _, h := range hosts {
		aliases = append(aliases, h.Alias)
	}
	if len(hosts) != 2 {
		t.Fatalf("got %d hosts %v, want 2 — a repeated alias is still one host", len(hosts), aliases)
	}
	seen := map[string]int{}
	for _, h := range hosts {
		seen[h.Alias]++
	}
	for alias, n := range seen {
		if n > 1 {
			t.Errorf("alias %q appears %d times in State()", alias, n)
		}
	}
}

// Collapsing duplicates must keep the block ssh itself would use — the first —
// so the card shows the address you actually connect to.
func TestStateKeepsTheFirstBlockOfARepeatedAlias(t *testing.T) {
	app := appOverConfig(t, `Host alpha
  HostName 10.0.0.1
  User dev

Host alpha
  HostName 10.0.0.9
`)
	hosts := app.State().Hosts
	if len(hosts) != 1 {
		t.Fatalf("got %d hosts, want 1", len(hosts))
	}
	if hosts[0].HostName != "10.0.0.1" {
		t.Errorf("HostName = %q, want the first block's 10.0.0.1", hosts[0].HostName)
	}
}
