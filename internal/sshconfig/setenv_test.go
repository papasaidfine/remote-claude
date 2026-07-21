package sshconfig

import (
	"strings"
	"testing"
)

func TestRenderClientNameSetEnv(t *testing.T) {
	got := Render(BlockOpts{Host: "h", User: "u", Port: 22, ClientName: "lisa-laptop"})
	if !strings.Contains(got, "    SetEnv LC_CLIENT_NAME=lisa-laptop") {
		t.Errorf("missing SetEnv line:\n%s", got)
	}
}

func TestRenderNoClientNameOmitsSetEnv(t *testing.T) {
	got := Render(BlockOpts{Host: "h", User: "u", Port: 22})
	if strings.Contains(got, "SetEnv") {
		t.Errorf("SetEnv present without ClientName:\n%s", got)
	}
}

// The app writes the client block with the reverse forward OFF (the bridge owns
// -R); make sure RevPort:0 keeps RemoteForward out.
func TestClientBlockHasNoRemoteForward(t *testing.T) {
	got := Render(BlockOpts{Host: "h", User: "u", Port: 22, ClientName: "x", RevPort: 0})
	if strings.Contains(got, "RemoteForward") {
		t.Errorf("client block must not carry RemoteForward (bridge owns -R):\n%s", got)
	}
}
