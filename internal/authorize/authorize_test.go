package authorize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const samplePub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIabcdefghijklmnopqrstuvwxyz0123456789ABCD user@host"

func TestBlob(t *testing.T) {
	b, err := Blob(samplePub)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b, "AAAA") {
		t.Errorf("Blob = %q, want the AAAA field", b)
	}
	if _, err := Blob("not a key"); err == nil {
		t.Error("expected error when no AAAA field is present")
	}
}

func TestEntryFormat(t *testing.T) {
	e := Entry(samplePub)
	if !strings.HasPrefix(e, `from="127.0.0.1,::1",no-agent-forwarding,no-X11-forwarding `) {
		t.Errorf("Entry prefix wrong: %q", e)
	}
	if !strings.HasSuffix(e, samplePub) {
		t.Errorf("Entry should end with the key: %q", e)
	}
}

func TestAddThenDedup(t *testing.T) {
	ak := filepath.Join(t.TempDir(), "authorized_keys")

	added, err := Add(ak, samplePub, nil)
	if err != nil || !added {
		t.Fatalf("first Add: added=%v err=%v", added, err)
	}
	added, err = Add(ak, samplePub, nil)
	if err != nil || added {
		t.Fatalf("second Add should dedup: added=%v err=%v", added, err)
	}
	out, _ := os.ReadFile(ak)
	if n := strings.Count(string(out), "ssh-ed25519"); n != 1 {
		t.Errorf("expected the key once, found %d times", n)
	}
}

func TestAddValidationFailure(t *testing.T) {
	ak := filepath.Join(t.TempDir(), "authorized_keys")
	_, err := Add(ak, samplePub, func(string) error { return os.ErrInvalid })
	if err == nil {
		t.Error("expected Add to fail when validate returns an error")
	}
	if _, statErr := os.Stat(ak); statErr == nil {
		t.Error("authorized_keys should not be created when validation fails")
	}
}

func TestAddEmptyErrors(t *testing.T) {
	ak := filepath.Join(t.TempDir(), "authorized_keys")
	if _, err := Add(ak, "   ", nil); err == nil {
		t.Error("expected error on empty key")
	}
}
