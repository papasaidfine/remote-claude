// Command remote-claude is the unified client bootstrap. With no arguments it
// runs the interactive setup menu; invoked as "remote-claude relay <host>
// <port>" it acts as the ssh ProxyCommand relay for the optional xray path.
package main

import (
	"fmt"
	"os"

	"github.com/papasaidfine/remote-claude/internal/menu"
	"github.com/papasaidfine/remote-claude/internal/paths"
	"github.com/papasaidfine/remote-claude/internal/platform"
	"github.com/papasaidfine/remote-claude/internal/relay"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "relay" {
		os.Exit(relay.Main(os.Args[2:]))
	}
	p, err := paths.Resolve()
	if err != nil {
		fmt.Fprintln(os.Stderr, "remote-claude:", err)
		os.Exit(1)
	}
	menu.Run(menu.Menu{P: p, Plat: platform.New()})
}
