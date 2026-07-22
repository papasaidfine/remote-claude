// Command remote-claude is the reverse-tunnel bridge app. With no arguments it
// launches the local web UI (and opens the browser); the app is also the daemon
// that keeps the reverse tunnels up. Subcommands:
//
//	relay <host> <port>   ssh ProxyCommand relay for the xray path (unchanged)
//	serve [addr]          run the web UI without opening a browser (headless)
//	menu                  the legacy interactive numbered menu (fallback)
package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/menu"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/platform"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/relay"
	"github.com/papasaidfine/remote-claude/internal/sshbin"
	"github.com/papasaidfine/remote-claude/internal/store"
	"github.com/papasaidfine/remote-claude/internal/ui"
	"github.com/papasaidfine/remote-claude/internal/webui"
)

// defaultAddr is the fixed local address the app binds. A failed bind is taken
// as "an instance is already running" — we just open that URL and exit, which
// makes a second launch focus the existing app instead of starting a rival.
const defaultAddr = "127.0.0.1:8765"

func main() {
	// SSH_ASKPASS hook: when the bridge/provision runs ssh with a UI-supplied
	// password, ssh execs this same binary to fetch it. Gated by an env flag so
	// a normal launch never triggers it.
	if os.Getenv("RC_ASKPASS_MODE") == "1" {
		fmt.Println(os.Getenv("RC_ASKPASS_SECRET"))
		return
	}
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "relay":
			os.Exit(relay.Main(os.Args[2:]))
		case "menu":
			runMenu()
			return
		case "serve":
			addr := defaultAddr
			if len(os.Args) >= 3 {
				addr = os.Args[2]
			}
			runApp(addr, false)
			return
		}
	}
	runApp(defaultAddr, true)
}

func runMenu() {
	p, err := paths.Resolve()
	if err != nil {
		ui.Errf("remote-claude: %v", err)
		os.Exit(1)
	}
	menu.Run(menu.Menu{P: p, Plat: platform.New()})
}

func runApp(addr string, openBrowser bool) {
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
	srv := webui.New(cfg, cfgPath, p, mgr, prov, plat) // Normalizes cfg

	autoStart(cfg, prov, mgr)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if isAddrInUse(err) {
			url := "http://" + addr
			ui.Log("remote-claude appears to be running already: %s", url)
			if openBrowser {
				openURL(url)
			}
			return
		}
		ui.Errf("remote-claude: cannot listen on %s: %v", addr, err)
		os.Exit(1)
	}

	url := "http://" + ln.Addr().String()
	ui.Log("remote-claude app: %s", url)
	ui.Log("Keep this process running to keep tunnels up (Ctrl-C to stop).")
	if openBrowser {
		go openURL(url)
	}

	httpSrv := &http.Server{Handler: srv.Handler()}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		<-sig
		ui.Log("Shutting down — stopping tunnels…")
		mgr.StopAll()
		httpSrv.Close()
	}()

	if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
		ui.Errf("remote-claude: server error: %v", err)
	}
}

// autoStart brings up tunnels for hosts flagged AutoStart, best-effort.
func autoStart(cfg *store.Config, prov *provision.Client, mgr *bridge.Manager) {
	for _, h := range cfg.Hosts {
		if !h.AutoStart {
			continue
		}
		if err := prov.EnsureClient(h, cfg.ClientAlias); err != nil {
			ui.Warn("auto-start %s: client setup failed: %v", h.Name, err)
			continue
		}
		if err := mgr.Start(bridge.Spec{HostID: h.ID, Alias: paths.Alias, ReversePort: h.ReversePort}); err != nil {
			ui.Warn("auto-start %s: %v", h.Name, err)
		}
	}
}

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

// openURL best-effort opens the default browser at url.
func openURL(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		cmd, args = "open", []string{url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}
