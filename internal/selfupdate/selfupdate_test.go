package selfupdate

import (
	"runtime"
	"testing"
)

func TestAssetNameFor(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "remote-claude-gui_linux_amd64"},
		{"darwin", "arm64", "remote-claude-gui_darwin_arm64"},
		{"windows", "amd64", "remote-claude-gui_windows_amd64.exe"},
	}
	for _, c := range cases {
		got, err := assetNameFor(c.goos, c.goarch)
		if err != nil {
			t.Errorf("assetNameFor(%q, %q) unexpected error: %v", c.goos, c.goarch, err)
			continue
		}
		if got != c.want {
			t.Errorf("assetNameFor(%q, %q) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestAssetNameForUnsupported(t *testing.T) {
	// Combos we do not publish a GUI build for must error, not guess a name.
	unsupported := [][2]string{
		{"linux", "arm64"},
		{"darwin", "amd64"},
		{"windows", "arm64"},
		{"freebsd", "amd64"},
		{"plan9", "386"},
	}
	for _, c := range unsupported {
		if name, err := assetNameFor(c[0], c[1]); err == nil {
			t.Errorf("assetNameFor(%q, %q) = %q, want error", c[0], c[1], name)
		}
	}
}

func TestAssetNameCurrentPlatform(t *testing.T) {
	name, err := AssetName()
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64", "darwin/arm64", "windows/amd64":
		if err != nil {
			t.Fatalf("AssetName() on supported platform errored: %v", err)
		}
		if name == "" {
			t.Fatal("AssetName() returned an empty name")
		}
	default:
		if err == nil {
			t.Fatalf("AssetName() = %q on unsupported platform, want error", name)
		}
	}
}

func TestHasUpdate(t *testing.T) {
	cases := []struct {
		tag, current string
		want         bool
	}{
		{"v0.2.0", "v0.1.0", true},  // newer tag → update
		{"v0.2.0", "v0.2.0", false}, // same version → no update
		{"v0.2.0", "dev", false},    // local dev build → never update
		{"", "v0.1.0", false},       // unknown/missing tag → no update
		{"", "dev", false},          // both unknown → no update
		{"v0.2.0", "", true},        // empty current is not "dev" → update
	}
	for _, c := range cases {
		if got := hasUpdate(c.tag, c.current); got != c.want {
			t.Errorf("hasUpdate(%q, %q) = %v, want %v", c.tag, c.current, got, c.want)
		}
	}
}
