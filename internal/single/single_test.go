package single

import (
	"errors"
	"net"
	"testing"
	"time"
)

// freeAddr reserves and immediately releases a loopback port, returning its
// address. Good enough for tests: nothing else on the machine is racing for it.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestAcquireTakesAFreeAddress(t *testing.T) {
	lock, err := Acquire(freeAddr(t))
	if err != nil {
		t.Fatalf("Acquire on a free address: %v", err)
	}
	defer lock.Release()
}

func TestSecondAcquireReportsAlreadyRunning(t *testing.T) {
	addr := freeAddr(t)
	first, err := Acquire(addr)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer first.Release()

	second, err := Acquire(addr)
	if !errors.Is(err, ErrRunning) {
		t.Fatalf("second Acquire: got err %v, want ErrRunning", err)
	}
	if second != nil {
		t.Fatal("second Acquire returned a lock as well as an error")
	}
}

func TestSignalRunsTheHoldersActivateCallback(t *testing.T) {
	addr := freeAddr(t)
	lock, err := Acquire(addr)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	activated := make(chan struct{}, 1)
	lock.OnActivate(func() { activated <- struct{}{} })

	if err := Signal(addr); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	select {
	case <-activated:
	case <-time.After(2 * time.Second):
		t.Fatal("Signal did not run the activate callback")
	}
}

func TestSignalToNobodyFails(t *testing.T) {
	if err := Signal(freeAddr(t)); err == nil {
		t.Fatal("Signal to an address with no holder: want an error, got nil")
	}
}

func TestReleaseFreesTheAddressForANewHolder(t *testing.T) {
	addr := freeAddr(t)
	first, err := Acquire(addr)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second, err := Acquire(addr)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	second.Release()
}

func TestAcquireWaitSucceedsOnceTheHolderReleases(t *testing.T) {
	addr := freeAddr(t)
	held, err := Acquire(addr)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		held.Release()
	}()

	lock, err := AcquireWait(addr, 5*time.Second)
	if err != nil {
		t.Fatalf("AcquireWait while the holder was releasing: %v", err)
	}
	lock.Release()
}

func TestAcquireWaitGivesUpWhenTheHolderStays(t *testing.T) {
	addr := freeAddr(t)
	held, err := Acquire(addr)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer held.Release()

	start := time.Now()
	lock, err := AcquireWait(addr, 300*time.Millisecond)
	if !errors.Is(err, ErrRunning) {
		t.Fatalf("AcquireWait against a staying holder: got err %v, want ErrRunning", err)
	}
	if lock != nil {
		t.Fatal("AcquireWait returned a lock as well as an error")
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Fatalf("AcquireWait gave up after %v, before its %v deadline", elapsed, 300*time.Millisecond)
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	lock, err := Acquire(freeAddr(t))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}
