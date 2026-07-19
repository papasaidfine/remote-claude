// Package ui provides the colored console output and interactive prompts shared
// by every menu item, mirroring the log/warn/err/ask helpers of the original
// bootstrap scripts.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ANSI color codes, blanked when stdout is not a terminal.
var (
	cGrn = "\033[1;32m"
	cYel = "\033[1;33m"
	cRed = "\033[1;31m"
	cHdr = "\033[1;36m"
	cDim = "\033[2m"
	cRst = "\033[0m"
)

var in = bufio.NewReader(os.Stdin)

func init() {
	if !isTerminal(os.Stdout) {
		cGrn, cYel, cRed, cHdr, cDim, cRst = "", "", "", "", "", ""
	}
}

// Log prints a green [+] status line.
func Log(format string, a ...any) { fmt.Printf("%s[+]%s %s\n", cGrn, cRst, sprintf(format, a)) }

// Warn prints a yellow [!] line.
func Warn(format string, a ...any) { fmt.Printf("%s[!]%s %s\n", cYel, cRst, sprintf(format, a)) }

// Errf prints a red [x] line to stderr.
func Errf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s[x]%s %s\n", cRed, cRst, sprintf(format, a))
}

// Tick prints an indented green check line (a passing sub-check).
func Tick(format string, a ...any) { fmt.Printf("  %s✔%s %s\n", cGrn, cRst, sprintf(format, a)) }

// Cross prints an indented red cross line (a failing sub-check).
func Cross(format string, a ...any) { fmt.Printf("  %s✘%s %s\n", cRed, cRst, sprintf(format, a)) }

// Plain prints a line with no prefix.
func Plain(format string, a ...any) { fmt.Println(sprintf(format, a)) }

// Header prints a bold-cyan phase header followed by a dim annotation.
func Header(title, note string) {
	fmt.Printf("  %s%s%s%s          %s%s\n", cHdr, title, cRst, cDim, note, cRst)
}

func sprintf(format string, a []any) string {
	if len(a) == 0 {
		return format
	}
	return fmt.Sprintf(format, a...)
}

// Mark renders a status checkbox: "[ ✔ ]" when ok, "[   ]" otherwise.
func Mark(ok bool) string {
	if ok {
		return fmt.Sprintf("[ %s✔%s ]", cGrn, cRst)
	}
	return "[   ]"
}

// Toggle renders an on/off marker for the proxy item.
func Toggle(on bool) string {
	if on {
		return fmt.Sprintf("[ %son%s]", cGrn, cRst)
	}
	return "[off]"
}

// Ask reads a line, returning def when the reply is empty.
func Ask(prompt, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", prompt, def)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	line, _ := in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// AskYN prompts for a yes/no answer, looping until it gets one; the default is
// returned on an empty reply.
func AskYN(prompt string, defYes bool) bool {
	hint := "y/N"
	if defYes {
		hint = "Y/n"
	}
	for {
		fmt.Printf("%s [%s]: ", prompt, hint)
		line, err := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			if err != nil {
				return defYes // EOF: fall back to the default
			}
			return defYes
		}
		switch line[0] {
		case 'y', 'Y':
			return true
		case 'n', 'N':
			return false
		}
	}
}

// ReadChoice prints the prompt and returns the trimmed line (menu selector).
func ReadChoice(prompt string) (string, bool) {
	fmt.Printf("%s: ", prompt)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}
