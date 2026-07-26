// Command remote-claude is the reverse-tunnel bridge app for machines without a
// desktop. With no arguments it opens a terminal UI; it is also the process that
// keeps the reverse tunnels up. Subcommands:
//
//	relay <host> <port>   ssh ProxyCommand relay for the proxy path
//	serve                 hold the tunnels up with no UI (for systemd / nohup)
//	version               print the version
//
// The desktop front-end is cmd/remote-claude-gui. Only one of the two may run on
// a machine at a time — see internal/single for why.
package main

import (
	"errors"
	"os"
	"os/signal"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/core"
	"github.com/papasaidfine/remote-claude/internal/i18n"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/platform"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/relay"
	"github.com/papasaidfine/remote-claude/internal/single"
	"github.com/papasaidfine/remote-claude/internal/sshbin"
	"github.com/papasaidfine/remote-claude/internal/store"
	"github.com/papasaidfine/remote-claude/internal/tui"
	"github.com/papasaidfine/remote-claude/internal/ui"
)

// version is stamped at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "relay":
			// ssh runs this once per connection, so it must never touch the
			// single-instance lock.
			os.Exit(relay.Main(os.Args[2:]))
		case "version", "--version", "-v":
			ui.Plain(version)
			return
		case "serve":
			run(false)
			return
		}
	}
	run(true)
}

// run starts the app, either with the terminal UI or as a bare daemon.
func run(interactive bool) {
	lock, err := single.Acquire(single.DefaultAddr)
	if err != nil {
		if errors.Is(err, single.ErrRunning) {
			// The holder may be the desktop app, which will raise its window.
			_ = single.Signal(single.DefaultAddr)
			ui.Warn("remote-claude is already running on this machine — nothing to do.")
			return
		}
		// The guard itself is unavailable. Refusing to start over that would be a
		// worse failure than the duplicate it protects against.
		ui.Warn("single-instance guard unavailable, continuing without it: %v", err)
	}
	if lock != nil {
		defer lock.Release()
	}

	p, err := paths.Resolve()
	if err != nil {
		ui.Errf("remote-claude: %v", err)
		os.Exit(1)
	}
	cfgPath := store.Path(p)
	cfg, err := store.Load(cfgPath)
	if err != nil {
		ui.Errf("remote-claude: reading config: %v", err)
		os.Exit(1)
	}

	plat := platform.New()
	mgr := bridge.NewManager(sshbin.SSH())
	prov := provision.New(p, plat)
	app := core.New(cfg, cfgPath, p, mgr, prov, plat) // normalizes cfg
	app.RepairProxies()                               // fix a ProxyCommand pointing at a moved binary
	defer mgr.StopAll()

	app.AutoStart(func(alias string, err error) {
		ui.Warn("auto-start %s: %v", alias, err)
	})

	if !interactive {
		serve(mgr)
		return
	}
	if err := tui.New(app, i18n.Parse(app.Lang()), version).Run(); err != nil {
		ui.Errf("remote-claude: %v", err)
		os.Exit(1)
	}
}

// serve holds the tunnels up with no UI at all, for a machine where the app runs
// under systemd or nohup and nobody is watching a terminal.
func serve(mgr *bridge.Manager) {
	ui.Log("remote-claude is holding the tunnels up. Ctrl-C to stop.")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	ui.Log("Shutting down — stopping tunnels…")
	mgr.StopAll()
}
