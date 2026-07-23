package bridge

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSSHArgs(t *testing.T) {
	args := SSHArgs(Spec{Alias: "remote-claude", ReversePort: 2222})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-R 127.0.0.1:2222:127.0.0.1:22") {
		t.Errorf("missing reverse forward: %q", joined)
	}
	if !strings.Contains(joined, "-N") || !strings.Contains(joined, "ExitOnForwardFailure=yes") {
		t.Errorf("missing -N/ExitOnForwardFailure: %q", joined)
	}
	if args[len(args)-1] != "remote-claude" {
		t.Errorf("alias must be the last arg: %q", joined)
	}
}

func TestSSHArgsNoReversePort(t *testing.T) {
	if joined := strings.Join(SSHArgs(Spec{Alias: "a"}), " "); strings.Contains(joined, "-R") {
		t.Errorf("no reverse port → no -R: %q", joined)
	}
}

func TestBackoffFor(t *testing.T) {
	base, max := time.Second, 60*time.Second
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{6, 60 * time.Second}, // 64s clamped to 60
		{100, 60 * time.Second},
		{-5, 1 * time.Second},
	}
	for _, c := range cases {
		if got := backoffFor(c.attempt, base, max); got != c.want {
			t.Errorf("backoffFor(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}

// fakeRunner fails its first failFirst calls immediately, then blocks until the
// context is cancelled (simulating a tunnel that stays up).
type fakeRunner struct {
	mu        sync.Mutex
	calls     int
	failFirst int
}

func (f *fakeRunner) Run(ctx context.Context, args []string) error {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()
	if n <= f.failFirst {
		return fmt.Errorf("boom %d", n)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fastManager builds a Manager with tiny timings and an injected runner.
func fastManager(r Runner) *Manager {
	return &Manager{
		tunnels:     map[string]*tunnel{},
		newRunner:   func(Spec) Runner { return r },
		upThreshold: 20 * time.Millisecond,
		baseBackoff: 1 * time.Millisecond,
		maxBackoff:  5 * time.Millisecond,
	}
}

func waitState(t *testing.T, m *Manager, id string, want State, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.Status(id).State == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("state for %q did not reach %q in %v (got %q)", id, want, timeout, m.Status(id).State)
}

func TestSupervisorReachesUpThenStops(t *testing.T) {
	r := &fakeRunner{}
	m := fastManager(r)
	if err := m.Start(Spec{Alias: "remote-claude"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitState(t, m, "remote-claude", StateUp, time.Second)

	m.Stop("remote-claude")
	if s := m.Status("remote-claude").State; s != StateStopped {
		t.Fatalf("after Stop state = %q, want stopped", s)
	}
	if m.Running("remote-claude") {
		t.Fatal("tunnel still running after Stop")
	}
}

func TestSupervisorRetriesThenRecovers(t *testing.T) {
	r := &fakeRunner{failFirst: 3}
	m := fastManager(r)
	if err := m.Start(Spec{Alias: "a"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitState(t, m, "a", StateUp, 2*time.Second)
	if got := m.Status("a").Restarts; got < 3 {
		t.Fatalf("expected >=3 restarts after early exits, got %d", got)
	}
	if r.count() < 4 {
		t.Fatalf("runner should have been called >=4 times, got %d", r.count())
	}
	m.Stop("a")
}

func TestStartInvalidSpec(t *testing.T) {
	m := fastManager(&fakeRunner{})
	if err := m.Start(Spec{Alias: ""}); err == nil {
		t.Error("expected error for empty alias")
	}
}

func TestRestartReplacesTunnel(t *testing.T) {
	m := fastManager(&fakeRunner{})
	m.Start(Spec{Alias: "a"})
	waitState(t, m, "a", StateUp, time.Second)
	// Restarting the same alias should not error or deadlock.
	if err := m.Start(Spec{Alias: "a"}); err != nil {
		t.Fatalf("restart: %v", err)
	}
	waitState(t, m, "a", StateUp, time.Second)
	m.StopAll()
	if len(m.StatusAll()) != 0 {
		t.Fatal("StopAll left tunnels behind")
	}
}
