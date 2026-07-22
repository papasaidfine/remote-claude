// Package sshcfg parses and edits the whole ~/.ssh/config: every Host block and
// its parameters. Edits are line-level, so comments, blank lines, unknown
// keywords, and formatting are preserved — the file round-trips verbatim when
// unmodified. This is the general reader/editor behind the app's host list;
// the marker-delimited managed block lives in package sshconfig.
package sshcfg

import (
	"strings"
)

// File is a parsed config: the lines before the first Host/Match (Preamble) and
// the ordered blocks.
type File struct {
	Preamble []string
	Blocks   []*Block
}

// Block is one Host (or Match) section: its verbatim header line, the parsed
// Host patterns, and the verbatim body lines up to the next block.
type Block struct {
	Header   string   // e.g. "Host remote-claude" (verbatim, keeps indentation)
	IsMatch  bool     // a Match block rather than Host
	Patterns []string // patterns from a Host header (nil for Match)
	Body     []string // verbatim lines after the header
}

// Parse splits content into a File. It never fails; malformed lines are kept
// verbatim in whatever block they fall under.
func Parse(content string) *File {
	f := &File{}
	var cur *Block
	// Split on "\n" and rejoin the same way in String() → exact round-trip.
	for _, line := range strings.Split(content, "\n") {
		switch strings.ToLower(keyword(line)) {
		case "host":
			cur = &Block{Header: line, Patterns: fields(value(line))}
			f.Blocks = append(f.Blocks, cur)
		case "match":
			cur = &Block{Header: line, IsMatch: true}
			f.Blocks = append(f.Blocks, cur)
		default:
			if cur == nil {
				f.Preamble = append(f.Preamble, line)
			} else {
				cur.Body = append(cur.Body, line)
			}
		}
	}
	return f
}

// String reassembles the file. Parse(s).String() == s.
func (f *File) String() string {
	var lines []string
	lines = append(lines, f.Preamble...)
	for _, b := range f.Blocks {
		lines = append(lines, b.Header)
		lines = append(lines, b.Body...)
	}
	return strings.Join(lines, "\n")
}

// Hosts returns the Host blocks (skipping Match sections).
func (f *File) Hosts() []*Block {
	var out []*Block
	for _, b := range f.Blocks {
		if !b.IsMatch {
			out = append(out, b)
		}
	}
	return out
}

// FindHost returns the first Host block whose exact patterns include alias, or
// nil.
func (f *File) FindHost(alias string) *Block {
	for _, b := range f.Hosts() {
		for _, p := range b.Patterns {
			if p == alias {
				return b
			}
		}
	}
	return nil
}

// AddHost appends a new "Host <alias>" block and returns it.
func (f *File) AddHost(alias string) *Block {
	b := &Block{Header: "Host " + alias, Patterns: []string{alias}}
	f.Blocks = append(f.Blocks, b)
	return b
}

// RemoveHost deletes the first Host block matching alias. Returns whether it
// existed.
func (f *File) RemoveHost(alias string) bool {
	for i, b := range f.Blocks {
		if b.IsMatch {
			continue
		}
		for _, p := range b.Patterns {
			if p == alias {
				f.Blocks = append(f.Blocks[:i], f.Blocks[i+1:]...)
				return true
			}
		}
	}
	return false
}

// Alias is the block's first Host pattern (its display name), or "".
func (b *Block) Alias() string {
	if len(b.Patterns) == 0 {
		return ""
	}
	return b.Patterns[0]
}

// Get returns the value of the first body line whose keyword matches key
// (case-insensitive), or "".
func (b *Block) Get(key string) string {
	for _, l := range b.Body {
		if strings.EqualFold(keyword(l), key) {
			return value(l)
		}
	}
	return ""
}

// Has reports whether key is present in the block.
func (b *Block) Has(key string) bool {
	for _, l := range b.Body {
		if strings.EqualFold(keyword(l), key) {
			return true
		}
	}
	return false
}

// Set writes key=val: it replaces the first existing line (keeping its
// indentation) or appends a new indented line. An empty val removes the key.
func (b *Block) Set(key, val string) {
	if strings.TrimSpace(val) == "" {
		b.Remove(key)
		return
	}
	for i, l := range b.Body {
		if strings.EqualFold(keyword(l), key) {
			b.Body[i] = leadingWS(l) + key + " " + val
			return
		}
	}
	b.Body = append(b.Body, "    "+key+" "+val)
}

// Remove deletes every line whose keyword matches key.
func (b *Block) Remove(key string) {
	out := b.Body[:0]
	for _, l := range b.Body {
		if strings.EqualFold(keyword(l), key) {
			continue
		}
		out = append(out, l)
	}
	b.Body = out
}

// ---- line helpers (ssh keywords are case-insensitive; "Key Value" or "Key=Value") ----

func keyword(line string) string {
	s := strings.TrimLeft(line, " \t")
	if i := strings.IndexAny(s, " \t="); i >= 0 {
		return s[:i]
	}
	return s
}

func value(line string) string {
	s := strings.TrimLeft(line, " \t")
	i := strings.IndexAny(s, " \t=")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(strings.TrimLeft(s[i:], " \t="))
}

func fields(s string) []string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return nil
	}
	return f
}

func leadingWS(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}
