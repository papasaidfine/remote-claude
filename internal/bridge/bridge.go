// Package bridge is the reverse-tunnel supervisor. It owns the `ssh -N -R`
// connection that carries the reverse tunnel (server loopback -> this machine's
// sshd), decoupled from interactive `ssh` sessions so those can be unlimited and
// concurrent. Each configured host gets one supervised tunnel that reconnects
// with exponential backoff and reports live status.
//
// The Runner seam keeps the supervise loop testable without spawning ssh.
package bridge

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// State is the coarse lifecycle of a tunnel.
type State string

const (
	StateStopped    State = "stopped"
	StateConnecting State = "connecting"
	StateUp         State = "up"
	StateRetrying   State = "retrying"
)

// Status is a snapshot of one tunnel, safe to serialize to the UI.
type Status struct {
	Alias     string    `json:"alias"`
	State     State     `json:"state"`
	LastError string    `json:"last_error,omitempty"`
	Restarts  int       `json:"restarts"`
	Since     time.Time `json:"since"`
}

// Spec identifies a tunnel: the ssh Host alias to connect. The reverse forward
// comes from that host's ~/.ssh/config (its RemoteForward), so the supervisor
// just runs `ssh -N <alias>`.
type Spec struct {
	Alias string
}

// Runner starts the tunnel process and blocks until it exits or ctx is done.
type Runner interface {
	Run(ctx context.Context, args []string) error
}

// SSHArgs builds the ssh argument list (excluding the ssh binary itself). It
// runs `ssh -N <alias>`, relying on the host's config for the reverse forward
// and proxy. ExitOnForwardFailure=yes (on the bridge's own connection only)
// makes ssh fail loudly — and the loop retry/report — when the reverse port is
// already taken, instead of silently running without a forward.
func SSHArgs(alias string) []string {
	return []string{
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		alias,
	}
}

// backoffFor returns base*2^attempt, clamped to [base, max]. Loop-based so it
// never overflows for large attempt counts.
func backoffFor(attempt int, base, max time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := base
	for i := 0; i < attempt && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	if d < base {
		d = base
	}
	return d
}

// execRunner runs the real ssh binary.
type execRunner struct{ bin string }

func (e execRunner) Run(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, e.bin, args...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		if tail := lastLine(errb.String()); tail != "" {
			return fmt.Errorf("%v: %s", err, tail)
		}
	}
	return err
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 300 {
		s = s[len(s)-300:]
	}
	return strings.TrimSpace(s)
}

// tunnel is one supervised reverse connection.
type tunnel struct {
	spec   Spec
	runner Runner

	upThreshold time.Duration
	baseBackoff time.Duration
	maxBackoff  time.Duration

	mu       sync.Mutex
	state    State
	lastErr  string
	restarts int
	since    time.Time
	attempt  int

	cancel context.CancelFunc
	done   chan struct{}
}

func (t *tunnel) set(state State) {
	t.mu.Lock()
	t.state = state
	t.since = time.Now()
	t.mu.Unlock()
}

func (t *tunnel) status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Status{Alias: t.spec.Alias, State: t.state, LastError: t.lastErr, Restarts: t.restarts, Since: t.since}
}

// supervise runs the connect/retry loop until ctx is cancelled.
func (t *tunnel) supervise(ctx context.Context) {
	defer close(t.done)
	for {
		if ctx.Err() != nil {
			t.set(StateStopped)
			return
		}
		err := t.runOnce(ctx)
		if ctx.Err() != nil {
			t.set(StateStopped)
			return
		}
		t.mu.Lock()
		t.restarts++
		if err != nil {
			t.lastErr = err.Error()
		} else {
			t.lastErr = "ssh exited"
		}
		t.state = StateRetrying
		t.since = time.Now()
		d := backoffFor(t.attempt, t.baseBackoff, t.maxBackoff)
		t.attempt++
		t.mu.Unlock()

		select {
		case <-ctx.Done():
			t.set(StateStopped)
			return
		case <-time.After(d):
		}
	}
}

// runOnce starts one ssh process. It reports the tunnel "up" once the process
// has survived upThreshold (there is no clean "connected" signal from ssh -N),
// resetting the backoff. It returns when the process exits or ctx is cancelled.
func (t *tunnel) runOnce(ctx context.Context) error {
	t.set(StateConnecting)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- t.runner.Run(runCtx, SSHArgs(t.spec.Alias))
	}()

	select {
	case err := <-errCh:
		return err // died before it could be considered up
	case <-ctx.Done():
		cancel()
		<-errCh
		return ctx.Err()
	case <-time.After(t.upThreshold):
		t.mu.Lock()
		t.state = StateUp
		t.since = time.Now()
		t.attempt = 0 // survived → reset backoff
		t.lastErr = ""
		t.mu.Unlock()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		cancel()
		<-errCh
		return ctx.Err()
	}
}

// Manager owns every tunnel; the app process is the single in-process owner.
type Manager struct {
	mu      sync.Mutex
	tunnels map[string]*tunnel

	newRunner   func(Spec) Runner
	upThreshold time.Duration
	baseBackoff time.Duration
	maxBackoff  time.Duration
}

// NewManager builds a Manager that runs the given ssh binary.
func NewManager(sshBin string) *Manager {
	return &Manager{
		tunnels:     map[string]*tunnel{},
		newRunner:   func(Spec) Runner { return execRunner{bin: sshBin} },
		upThreshold: 5 * time.Second,
		baseBackoff: 1 * time.Second,
		maxBackoff:  60 * time.Second,
	}
}

// Start (re)starts the tunnel for spec.Alias. Restarting stops the previous one.
func (m *Manager) Start(spec Spec) error {
	if spec.Alias == "" {
		return fmt.Errorf("bridge: empty ssh alias")
	}
	m.mu.Lock()
	if old, ok := m.tunnels[spec.Alias]; ok {
		delete(m.tunnels, spec.Alias)
		m.mu.Unlock()
		old.cancel()
		<-old.done
		m.mu.Lock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &tunnel{
		spec:        spec,
		runner:      m.newRunner(spec),
		upThreshold: m.upThreshold,
		baseBackoff: m.baseBackoff,
		maxBackoff:  m.maxBackoff,
		state:       StateConnecting,
		since:       time.Now(),
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	m.tunnels[spec.Alias] = t
	m.mu.Unlock()
	go t.supervise(ctx)
	return nil
}

// Stop stops the tunnel for alias (no-op if not running).
func (m *Manager) Stop(alias string) {
	m.mu.Lock()
	t, ok := m.tunnels[alias]
	if ok {
		delete(m.tunnels, alias)
	}
	m.mu.Unlock()
	if ok {
		t.cancel()
		<-t.done
	}
}

// Running reports whether a tunnel is currently supervised for alias.
func (m *Manager) Running(alias string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.tunnels[alias]
	return ok
}

// Status returns the tunnel status for alias (Stopped if none).
func (m *Manager) Status(alias string) Status {
	m.mu.Lock()
	t, ok := m.tunnels[alias]
	m.mu.Unlock()
	if !ok {
		return Status{Alias: alias, State: StateStopped}
	}
	return t.status()
}

// StatusAll returns statuses for every running tunnel, keyed by alias.
func (m *Manager) StatusAll() map[string]Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]Status, len(m.tunnels))
	for id, t := range m.tunnels {
		out[id] = t.status()
	}
	return out
}

// StopAll stops every tunnel and waits for them to exit.
func (m *Manager) StopAll() {
	m.mu.Lock()
	ts := make([]*tunnel, 0, len(m.tunnels))
	for id, t := range m.tunnels {
		ts = append(ts, t)
		delete(m.tunnels, id)
	}
	m.mu.Unlock()
	for _, t := range ts {
		t.cancel()
		<-t.done
	}
}
