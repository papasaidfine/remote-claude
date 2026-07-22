//go:build !gui

// This directory is the native (Fyne) GUI. Fyne needs CGO + system graphics
// libraries, so it is built only with the "gui" tag (see the gui CI workflow).
// Without the tag we compile this stub instead, so the default CGO-free
// `go build ./...` and the CLI release stay green.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "remote-claude-gui must be built with the 'gui' tag and CGO:")
	fmt.Fprintln(os.Stderr, "  CGO_ENABLED=1 go build -tags gui ./cmd/remote-claude-gui")
	os.Exit(1)
}
