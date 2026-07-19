package nodes

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "vless-nodes.txt")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadDropsCommentsAndBlanks(t *testing.T) {
	p := write(t, "# header\n\n  vless://a@h:1\n   # spaced comment\nvless://b@h:2\n")
	got, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "vless://a@h:1" || got[1] != "vless://b@h:2" {
		t.Errorf("Read = %v, want the two vless URLs", got)
	}
}

func TestCountAndMissingFile(t *testing.T) {
	if Count(filepath.Join(t.TempDir(), "nope.txt")) != 0 {
		t.Error("missing file should count 0")
	}
	p := write(t, "vless://x@h:1\nvless://y@h:2\nvless://z@h:3\n")
	if Count(p) != 3 {
		t.Errorf("Count = %d, want 3", Count(p))
	}
}

func TestPickRandomReturnsAMember(t *testing.T) {
	p := write(t, "vless://x@h:1\nvless://y@h:2\n")
	set := map[string]bool{"vless://x@h:1": true, "vless://y@h:2": true}
	for i := 0; i < 20; i++ {
		got, err := PickRandom(p)
		if err != nil {
			t.Fatal(err)
		}
		if !set[got] {
			t.Fatalf("PickRandom returned unknown node %q", got)
		}
	}
}

func TestPickRandomEmptyErrors(t *testing.T) {
	p := write(t, "# only comments\n\n")
	if _, err := PickRandom(p); err == nil {
		t.Error("expected error on a file with no nodes")
	}
}
