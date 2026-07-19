// Package nodes reads the vless-nodes.txt file (one vless:// URL per line, #
// comments) and picks a random node per connection.
package nodes

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"strings"
)

// Read returns the node URLs in file, dropping blank lines and # comments.
func Read(file string) ([]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// Count returns the number of usable nodes (0 when the file is missing).
func Count(file string) int {
	n, err := Read(file)
	if err != nil {
		return 0
	}
	return len(n)
}

// PickRandom returns one random node URL, or an error when there are none.
func PickRandom(file string) (string, error) {
	n, err := Read(file)
	if err != nil || len(n) == 0 {
		return "", fmt.Errorf("no vless:// nodes in %s — edit the file and add one", file)
	}
	return n[rand.Intn(len(n))], nil
}
