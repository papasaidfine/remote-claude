// Package single keeps one instance of the app per machine. It works by holding
// a loopback TCP port: whoever binds it owns the machine, and a later launch
// that finds the port taken hands over to the incumbent instead of starting a
// rival.
//
// A port rather than a PID lock file: the kernel frees it the moment the holder
// dies, so a crash can never leave a stale lock behind.
//
// The lock spans both binaries (GUI and CLI), not just one of them. Two running
// instances would each start their configured reverse tunnels, and the second
// one's `ssh -N -R <port>:…` fails outright with "remote port forwarding failed
// for listen port <port>".
package single

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// DefaultAddr is the loopback address every instance competes for.
const DefaultAddr = "127.0.0.1:8765"

// ErrRunning reports that another instance already holds the address.
var ErrRunning = errors.New("another instance is already running")

// dialTimeout bounds both the "is someone listening?" probe and Signal.
const dialTimeout = 2 * time.Second

// Lock is a held instance address. Release it to let another instance take over.
type Lock struct {
	ln net.Listener

	mu       sync.Mutex
	activate func()
	closed   bool
}

// Acquire takes addr for this instance. It returns ErrRunning if another
// instance already holds it, and the underlying error for any other failure.
func Acquire(addr string) (*Lock, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if someoneListening(addr) {
			return nil, ErrRunning
		}
		return nil, fmt.Errorf("claiming %s: %w", addr, err)
	}
	l := &Lock{ln: ln}
	go l.serve()
	return l, nil
}

// AcquireWait is Acquire, but waits up to d for a departing holder to let go
// instead of giving up at once. The updater uses it: the outgoing process
// releases the address moments before the incoming one starts, and without the
// wait that handover is a race the new instance loses.
func AcquireWait(addr string, d time.Duration) (*Lock, error) {
	deadline := time.Now().Add(d)
	for {
		lock, err := Acquire(addr)
		if !errors.Is(err, ErrRunning) {
			return lock, err // acquired, or failed for a reason waiting won't fix
		}
		if !time.Now().Before(deadline) {
			return nil, ErrRunning
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// OnActivate registers what to do when another launch hands over to this
// instance — typically raising the window. It may be called at most once per
// signal, on its own goroutine.
func (l *Lock) OnActivate(fn func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.activate = fn
}

// Release gives up the address. It is safe to call more than once.
func (l *Lock) Release() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()
	return l.ln.Close()
}

// Addr is the address actually held (useful when addr asked for port 0).
func (l *Lock) Addr() string { return l.ln.Addr().String() }

// serve answers hand-over connections until the lock is released.
func (l *Lock) serve() {
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			return // released, or the listener broke — either way we're done
		}
		conn.Close()
		l.mu.Lock()
		fn := l.activate
		l.mu.Unlock()
		if fn != nil {
			go fn()
		}
	}
}

// Signal tells the instance holding addr to bring itself to the front.
func Signal(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return fmt.Errorf("signalling the running instance at %s: %w", addr, err)
	}
	return conn.Close()
}

// someoneListening reports whether addr already has a listener. A failed Listen
// is not classified by inspecting its error: the "address in use" errno differs
// across platforms (Windows raises WSAEADDRINUSE, which does not compare equal
// to syscall.EADDRINUSE), so asking the address directly is both simpler and
// portable.
func someoneListening(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
