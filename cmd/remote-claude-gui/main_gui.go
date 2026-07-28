//go:build gui

// Command remote-claude-gui is the native desktop front-end. It builds the same
// core.App the TUI drives and renders it with Fyne. Build with:
//
//	CGO_ENABLED=1 go build -tags gui ./cmd/remote-claude-gui
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/papasaidfine/remote-claude/internal/bridge"
	"github.com/papasaidfine/remote-claude/internal/core"
	"github.com/papasaidfine/remote-claude/internal/i18n"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/platform"
	"github.com/papasaidfine/remote-claude/internal/provision"
	"github.com/papasaidfine/remote-claude/internal/relay"
	"github.com/papasaidfine/remote-claude/internal/selfupdate"
	"github.com/papasaidfine/remote-claude/internal/single"
	"github.com/papasaidfine/remote-claude/internal/sshbin"
	"github.com/papasaidfine/remote-claude/internal/store"
)

// version is stamped at release time via -ldflags "-X main.version=...".
var version = "dev"

// awaitInstanceFlag is passed to the process a self-update launches. It tells
// that process to wait for the outgoing one to release the single-instance lock
// rather than treating it as an incumbent to defer to — without it the handover
// is a race the successor loses, and the update ends with nothing running.
const awaitInstanceFlag = "-await-instance"

// awaitInstanceWait bounds that wait. The outgoing process releases the lock
// immediately before spawning us, so this only has to cover scheduling jitter.
const awaitInstanceWait = 15 * time.Second

func main() {
	// ssh invokes this same binary headlessly for the ProxyCommand relay (a
	// host's ProxyCommand points at whichever binary wrote it — possibly this
	// GUI). Handle it and exit BEFORE opening a window or touching the
	// single-instance lock: relay runs once per ssh connection and must never
	// compete for it.
	if len(os.Args) >= 2 && os.Args[1] == "relay" {
		os.Exit(relay.Main(os.Args[2:]))
	}

	closeLog := setupLog()
	defer closeLog()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic: %v\n%s", r, debug.Stack())
			os.Exit(1)
		}
	}()
	selfupdate.CleanupOldBinary() // sweep a "<exe>.old" left by a prior self-update
	run()
}

// setupLog points the standard logger at a file next to the config, so a GUI
// crash (there is no console on Windows) leaves a trace the user can send. It is
// best-effort: failures are ignored.
func setupLog() func() {
	dir := os.TempDir()
	if p, err := paths.Resolve(); err == nil {
		dir = p.RCConfigDir
	}
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "gui.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return func() {}
	}
	log.SetOutput(f)
	log.Printf("remote-claude-gui %s starting", version)
	return func() { f.Close() }
}

func run() {
	lock, ok := claimInstance(hasArg(awaitInstanceFlag))
	if !ok {
		return // another instance owns this machine; we handed over to it
	}

	p, err := paths.Resolve()
	if err != nil {
		die(err)
	}
	cfg, _ := store.Load(store.Path(p)) // tolerant: never nil, never fatal
	plat := platform.New()
	mgr := bridge.NewManager(sshbin.SSH())
	prov := provision.New(p, plat)
	appCore := core.New(cfg, store.Path(p), p, mgr, prov, plat)
	appCore.RepairProxies() // fix any ProxyCommand left pointing at a moved binary
	appCore.AutoStart(func(string, error) {})

	a := app.New()
	a.Settings().SetTheme(newRCTheme())
	a.SetIcon(appIcon)
	// Quitting stops the tunnels (their ssh children would otherwise be orphaned).
	a.Lifecycle().SetOnStopped(func() { mgr.StopAll() })

	w := a.NewWindow("remote-claude " + version)
	w.Resize(fyne.NewSize(760, 660))

	lang := i18n.Parse(appCore.Lang())
	g := &gui{core: appCore, app: a, win: w, mgr: mgr, lock: lock, lang: lang, pr: i18n.P(lang)}
	w.SetContent(g.build())
	g.shown.Store(true) // ShowAndRun below puts the window on screen
	g.refresh()
	go g.autoRefresh()
	go g.spinRefresh()

	// A second launch hands over to us rather than starting a rival, so put the
	// window in front — that click was someone asking to see the app.
	if lock != nil {
		lock.OnActivate(func() { fyne.Do(g.surface) })
	}

	// System tray: closing the window hides to the tray so the app keeps holding
	// the tunnels up; Fyne adds a native Quit item. Only where a tray exists.
	// Hiding also parks the refresh tick — see tick().
	if desk, ok := a.(desktop.App); ok {
		desk.SetSystemTrayIcon(trayIcon)
		desk.SetSystemTrayMenu(fyne.NewMenu("remote-claude",
			fyne.NewMenuItem("Open", func() { fyne.Do(g.surface) }),
		))
		w.SetCloseIntercept(func() {
			w.Hide()
			g.shown.Store(false)
		})
	}

	w.ShowAndRun()
}

// hasArg reports whether name was passed on the command line.
func hasArg(name string) bool {
	for _, a := range os.Args[1:] {
		if a == name {
			return true
		}
	}
	return false
}

// claimInstance takes the machine-wide single-instance lock. It reports whether
// this process should carry on running.
//
// await is set when a self-update spawned us: the outgoing process is releasing
// the lock at about this moment, so we wait for it instead of mistaking it for
// an incumbent.
func claimInstance(await bool) (*single.Lock, bool) {
	acquire := single.Acquire
	if await {
		acquire = func(addr string) (*single.Lock, error) {
			return single.AcquireWait(addr, awaitInstanceWait)
		}
	}
	lock, err := acquire(single.DefaultAddr)
	switch {
	case err == nil:
		return lock, true
	case errors.Is(err, single.ErrRunning):
		log.Printf("another instance already holds %s — asking it to come forward", single.DefaultAddr)
		if serr := single.Signal(single.DefaultAddr); serr != nil {
			log.Printf("could not signal the running instance: %v", serr)
		}
		return nil, false
	default:
		// The guard itself is unavailable — a firewall, an exotic network stack.
		// Refusing to launch over that would be a worse failure than the
		// duplicate instance it protects against, so carry on unguarded.
		log.Printf("single-instance guard unavailable, continuing without it: %v", err)
		return nil, true
	}
}

// autoRefresh re-renders live tunnel status on a timer. UI mutations must run on
// the main goroutine, so it hops through fyne.Do.
func (g *gui) autoRefresh() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for range t.C {
		fyne.Do(g.tick)
	}
}

// tick is one auto-refresh step. It stands down while the window is hidden in
// the tray: Fyne only reclaims discarded widgets when a window actually
// repaints, so refreshing an off-screen window leaks everything it touches.
// Nothing is visible then anyway, and showing the window refreshes immediately.
func (g *gui) tick() {
	if !g.shown.Load() {
		return
	}
	g.refresh()
}

// spinRefresh turns the spinner on any tunnel still on its way. It is a separate,
// much faster timer than autoRefresh because a spinner that steps twice a second
// is not a spinner, and re-reading every host's state that often would be waste.
func (g *gui) spinRefresh() {
	t := time.NewTicker(90 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		fyne.Do(g.spinTick)
	}
}

// spinTick advances one frame, touching only the buttons that are spinning.
func (g *gui) spinTick() {
	if !g.shown.Load() {
		return
	}
	var spinning bool
	for _, r := range g.rows {
		if r.toggle != nil && isPending(r.cur.Status.State) {
			spinning = true
			r.toggle.SetIcon(spinnerFrames[g.spinFrame%len(spinnerFrames)])
		}
	}
	// Only advance while something is spinning, so a tunnel that starts later
	// picks the animation up from the top rather than mid-turn.
	if spinning {
		g.spinFrame++
	}
}

// isPending reports whether a tunnel is on its way somewhere — neither settled
// up nor deliberately stopped.
func isPending(s bridge.State) bool {
	return s == bridge.StateConnecting || s == bridge.StateRetrying
}

// surface brings the window back from the tray. Both the tray's Open item and a
// second launch land here, so they cannot drift apart.
func (g *gui) surface() {
	g.win.Show()
	g.win.RequestFocus()
	g.shown.Store(true)
	g.tick() // the window may have been parked for hours
}

func die(err error) {
	log.Printf("fatal: %v", err)
	fmt.Fprintln(os.Stderr, "remote-claude-gui:", err)
	os.Exit(1)
}

// The surf mark's own size, and how far it rides up and down. The cell it sits
// in reserves both, so the bob never encroaches on the label underneath.
const (
	surfSize   = 128
	surfTravel = 12
)

// waitPanel is the "hang tight" panel plus the handle that stops its animation.
//
// Stopping is the caller's job because a Fyne animation lives on the app's
// run-loop rather than on the object it moves: one whose dialog has closed is
// not collected, it keeps ticking for the life of the process. Every caller
// hands stop to dialog.SetOnClosed, which fires on Hide() as well as on the
// user closing the dialog.
type waitPanel struct {
	fyne.CanvasObject
	stop func()
}

// waiting is the panel shown in dialogs that block on the network: Claude surfing
// above the status line, so a slow ssh/HTTP round-trip reads as "hang tight"
// rather than a frozen window.
//
// The mark rides a swell while it waits. Motion is the part that says "still
// working" — a still image in a stalled dialog looks exactly like a still image
// in a hung one. It is animated from here rather than inside the SVG because
// Fyne rasterizes with oksvg, which draws shapes and ignores SMIL and CSS
// animation entirely; a <animateTransform> in the file would simply not move.
func waiting(msg string) *waitPanel {
	surf := canvas.NewImageFromResource(surfIcon)
	surf.FillMode = canvas.ImageFillContain
	surf.SetMinSize(fyne.NewSize(surfSize, surfSize))
	cell := container.New(surfCell{}, surf)

	// Move, not Refresh: moving an image repaints the canvas without dropping
	// the rasterized SVG, so the swell costs a redraw per frame and not a
	// re-render of 15 paths. AutoReverse rather than a loop — a loop would snap
	// the mark back down to the trough on every cycle — and ease-in-out for the
	// hang at the top and bottom that reads as floating rather than sliding.
	ride := canvas.NewPositionAnimation(
		fyne.NewPos(0, surfTravel), fyne.NewPos(0, 0), 1400*time.Millisecond, surf.Move)
	ride.AutoReverse = true
	ride.RepeatCount = fyne.AnimationRepeatForever
	ride.Curve = fyne.AnimationEaseInOut
	ride.Start()

	return &waitPanel{
		CanvasObject: container.NewCenter(container.NewVBox(
			cell,
			widget.NewLabelWithStyle(msg, fyne.TextAlignCenter, fyne.TextStyle{}),
		)),
		stop: ride.Stop,
	}
}

// surfCell is the mark's berth: it sizes the art but never positions it, because
// the animation owns the position and a layout that moved its child would fight
// it every frame. The reserved height carries the full travel.
type surfCell struct{}

func (surfCell) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(surfSize, surfSize+surfTravel)
}

func (surfCell) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Resize(fyne.NewSize(size.Width, surfSize))
	}
}

type gui struct {
	core *core.App
	app  fyne.App
	win  fyne.Window
	mgr  *bridge.Manager // held so the update path can stop tunnels itself
	lock *single.Lock    // machine-wide instance lock; nil if the guard was unavailable
	lang i18n.Lang
	pr   i18n.Printer

	// hosts tab
	alias     *widget.Entry
	aliasBtn  *widget.Button
	status    *widget.Label
	hostsBox  *fyne.Container
	rows      map[string]*hostRow // live host cards, keyed by ssh alias
	empty     *widget.Label       // the "no hosts yet" placeholder, built once
	spinFrame int                 // which frame the pending-tunnel spinners are on

	// settings tab
	proxyState *widget.Label
	sshdState  *widget.Label

	shown atomic.Bool // window is on screen (false while hidden in the tray)
}

// t translates key into the active UI language.
func (g *gui) t(key string) string { return g.pr.T(key) }

// applyLang switches the UI language, persists it, and rebuilds the window.
func (g *gui) applyLang(l i18n.Lang) {
	if l == g.lang {
		return
	}
	g.lang = l
	g.pr = i18n.P(l)
	_ = g.core.SetLang(string(l))
	g.win.SetContent(g.build())
	g.refresh()
}

// build lays out the whole window: two tabs over a shared core.
func (g *gui) build() fyne.CanvasObject {
	tabs := container.NewAppTabs(
		container.NewTabItem(g.t("tab_hosts"), g.buildHosts()),
		container.NewTabItem(g.t("tab_settings"), g.buildSettings()),
	)
	tabs.SetTabLocation(container.TabLocationTop)
	return tabs
}

// refresh re-reads state and pushes it into both tabs.
func (g *gui) refresh() {
	st := g.core.State()
	g.refreshHosts(st)
	g.refreshSettings(st)
}

// do runs a mutating action, shows any error, and refreshes on success.
func (g *gui) do(fn func() error) {
	if err := fn(); err != nil {
		dialog.ShowError(err, g.win)
		return
	}
	g.refresh()
}

// ---- self-update ----

// checkUpdate queries GitHub for a newer release and, if one exists, offers to
// download and install it. Network work runs off the UI thread.
//
// The "is this a dev build?" question is settled before going to the network, so
// a local build says so plainly instead of reporting whatever the network did.
func (g *gui) checkUpdate() {
	if version == "dev" {
		dialog.ShowInformation(g.t("update_title"), g.t("update_dev"), g.win)
		return
	}
	wait := waiting(g.t("update_checking"))
	prog := dialog.NewCustom(g.t("update_title"), g.t("close"), wait, g.win)
	prog.SetOnClosed(wait.stop)
	prog.Show()
	go func() {
		rel, err := selfupdate.Check(version)
		fyne.Do(func() {
			prog.Hide()
			switch {
			case err != nil:
				dialog.ShowError(fmt.Errorf(g.t("update_failed_fmt"), err), g.win)
			case !rel.HasUpdate:
				dialog.ShowInformation(g.t("update_title"), fmt.Sprintf(g.t("update_latest_fmt"), version), g.win)
			default:
				dialog.ShowCustomConfirm(g.t("update_title"), g.t("update_download_yes"), g.t("cancel"),
					widget.NewLabel(fmt.Sprintf(g.t("update_avail_fmt"), version, rel.Version)),
					func(ok bool) {
						if ok {
							g.applyUpdate()
						}
					}, g.win)
			}
		})
	}()
}

// applyUpdate downloads and installs the latest release, then offers a restart.
func (g *gui) applyUpdate() {
	wait := waiting(g.t("update_downloading"))
	prog := dialog.NewCustom(g.t("update_title"), g.t("close"), wait, g.win)
	prog.SetOnClosed(wait.stop)
	prog.Show()
	go func() {
		err := selfupdate.Apply("")
		fyne.Do(func() {
			prog.Hide()
			if err != nil {
				dialog.ShowError(fmt.Errorf(g.t("update_failed_fmt"), err), g.win)
				return
			}
			dialog.ShowCustomConfirm(g.t("update_title"), g.t("restart_now"), g.t("later"),
				widget.NewLabel(g.t("update_done")), func(ok bool) {
					if ok {
						g.restartIntoNewVersion()
					}
				}, g.win)
		})
	}()
}

// restartIntoNewVersion hands this machine over to the binary just installed.
// The order is the whole point:
//
//  1. stop the tunnels here, rather than trusting Fyne's OnStopped hook to fire
//     — if it doesn't, the ssh children outlive us as orphans;
//  2. release the single-instance lock BEFORE starting the successor. Start it
//     first and it finds us still holding the lock, concludes an instance is
//     already running, signals us and exits — and then we exit too, leaving
//     nothing running at all;
//  3. start the successor with the flag that makes it wait for the lock, which
//     closes the gap between steps 2 and 3;
//  4. quit through Fyne so the tray icon is torn down properly instead of being
//     left as a ghost the shell only clears when you mouse over it; and
//  5. exit hard shortly afterwards no matter what. This process is now running
//     an image that has already been renamed aside, and for as long as it lives
//     it pins the "<exe>.old" that the next launch tries to sweep.
func (g *gui) restartIntoNewVersion() {
	g.mgr.StopAll()
	if g.lock != nil {
		if err := g.lock.Release(); err != nil {
			log.Printf("releasing the instance lock before restart: %v", err)
		}
	}
	if err := selfupdate.Restart(awaitInstanceFlag); err != nil {
		// The handover failed. Take the lock back so this process remains the
		// legitimate instance rather than leaving the machine unguarded, and let
		// the user relaunch by hand. The tunnels stay down; they are one click away.
		if lock, lerr := single.Acquire(single.DefaultAddr); lerr == nil {
			lock.OnActivate(func() { fyne.Do(g.surface) })
			g.lock = lock
		}
		dialog.ShowError(err, g.win)
		return
	}
	g.app.Quit()
	time.AfterFunc(1500*time.Millisecond, func() { os.Exit(0) })
}

// ---- shared widget helpers ----

// The setters below are all guarded. Fyne's SetText/SetChecked/Enable refresh
// unconditionally, so writing the same value back on every tick would keep the
// whole tree churning even when nothing changed.

func setLabel(l *widget.Label, text string) {
	if l.Text != text {
		l.SetText(text)
	}
}

func setButton(b *widget.Button, text string) {
	if b.Text != text {
		b.SetText(text)
	}
}

// setButtonLook changes a button's whole appearance in one refresh. Text, icon
// and importance each trigger their own otherwise, so a tunnel changing state
// would repaint the card three times.
func setButtonLook(b *widget.Button, text string, icon fyne.Resource, imp widget.Importance) {
	if b.Text == text && b.Icon == icon && b.Importance == imp {
		return
	}
	b.Text, b.Icon, b.Importance = text, icon, imp
	b.Refresh()
}

// setButtonText changes a button's caption without touching its icon, for the
// pending state where spinTick owns the icon.
func setButtonText(b *widget.Button, text string, imp widget.Importance) {
	if b.Text == text && b.Importance == imp {
		return
	}
	b.Text, b.Importance = text, imp
	b.Refresh()
}

// setChecked syncs a checkbox without firing OnChanged: that handler writes the
// value straight back to config, so echoing it every tick would be a write storm.
func setChecked(c *widget.Check, on bool) {
	if c.Checked == on {
		return
	}
	fn := c.OnChanged
	c.OnChanged = nil
	c.SetChecked(on)
	c.OnChanged = fn
}

func (g *gui) stateLabel(s bridge.Status) string {
	name := g.stateName(s.State)
	if s.State == bridge.StateRetrying && s.LastError != "" {
		return name + " (" + s.LastError + ")"
	}
	return name
}

func (g *gui) stateName(st bridge.State) string {
	switch st {
	case bridge.StateStopped:
		return g.t("state_stopped")
	case bridge.StateConnecting:
		return g.t("state_connecting")
	case bridge.StateUp:
		return g.t("state_up")
	case bridge.StateRetrying:
		return g.t("state_retrying")
	}
	return string(st)
}

func (g *gui) yn(b bool) string {
	if b {
		return g.t("running")
	}
	return g.t("not_detected")
}

func (g *gui) authLabel(b bool) string {
	if b {
		return g.t("authorized")
	}
	return g.t("already_present")
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return def
	}
	return n
}
