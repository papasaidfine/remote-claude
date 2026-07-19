package menu

import (
	"fmt"

	"github.com/papasaidfine/remote-claude/internal/platform"
	"github.com/papasaidfine/remote-claude/internal/ui"
)

func banner(p platform.Platform) {
	fmt.Printf(`
╭──────────────────────────────────────────────────────────────╮
│  remote-claude · reverse SSH bootstrap (%-7s)             │
│                                                              │
│    Local ──────ssh─────▶ Claude   (you reach the server)     │
│    Local ◀──reverse ssh── Claude  (the agent reaches back)   │
`, p.Name())
	if p.SupportsXray() {
		fmt.Println("│    xray ═[ ssh ]═▶  optional wrap for hostile networks       │")
	}
	fmt.Print(`╰──────────────────────────────────────────────────────────────╯
 Each item is independent and idempotent; work top to bottom.
 Modified system files are backed up first (*.claude-bak-<timestamp>).
`)
}

func (m Menu) draw() {
	fmt.Println()
	ui.Header("① Local ──▶ Claude", "reach the server running Claude Code")
	fmt.Printf("     1) %-48s %s\n", "SSH config shortcut (Host "+"remote-claude)", ui.Mark(m.statusConfig()))
	fmt.Printf("     2) %-48s %s\n", "Local SSH key - create & show public key", ui.Mark(m.statusKey()))
	fmt.Printf("     3) %s\n", "Test connection")
	fmt.Println()
	ui.Header("② Claude ──▶ Local", "let the agent ssh back into this machine")
	fmt.Printf("     4) %-48s %s\n", "Incoming SSH - install + harden  "+m.sshdTag(), ui.Mark(m.statusSshd()))
	fmt.Printf("     5) %-48s %s\n", "Authorize the server's connect-back key", ui.Mark(m.statusAuthorize()))
	fmt.Printf("     6) %-48s %s\n", "Reverse tunnel port (RemoteForward)", ui.Mark(m.statusRport()))
	if m.Plat.SupportsXray() {
		fmt.Println()
		ui.Header("③ xray ═[ ssh ]═▶", "optional - wrap the tunnel for bad networks")
		fmt.Printf("     7) %-48s %s\n", "xray client (binary + relay)", ui.Mark(m.statusXray()))
		fmt.Printf("     8) %-48s %s\n", "Route the tunnel through xray", ui.Toggle(m.proxyOn()))
	}
	fmt.Println()
	fmt.Println("     q) Quit")
}

// sshdTag notes the privilege the incoming-SSH item needs, per OS.
func (m Menu) sshdTag() string {
	if m.Plat.Name() == "Windows" {
		return "[admin]"
	}
	return "[sudo]"
}
