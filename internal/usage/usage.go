// Package usage parses Claude Code transcript usage and prices it. Claude Code
// writes one JSONL file per session under ~/.claude/projects/<project>/, and
// each assistant turn line carries message.usage (input/output/cache tokens),
// message.model, and a top-level timestamp. This package aggregates those lines
// into 1-day / 7-day / 30-day windows, per model, with Anthropic list pricing.
package usage

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Tokens is a token tally.
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheWrite int64 `json:"cache_write"`
	CacheRead  int64 `json:"cache_read"`
}

func (t *Tokens) add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheWrite += o.CacheWrite
	t.CacheRead += o.CacheRead
}

// ModelUsage is a per-model tally plus its priced cost (USD).
type ModelUsage struct {
	Model  string  `json:"model"`
	Tokens Tokens  `json:"tokens"`
	Cost   float64 `json:"cost"`
}

// Window is one time window's totals, broken down by model (cost-sorted).
type Window struct {
	Models []ModelUsage `json:"models"`
	Total  Tokens       `json:"total"`
	Cost   float64      `json:"cost"`
}

// Report holds the three windows the UI shows.
type Report struct {
	Day   Window `json:"day"`   // past 1 day
	Week  Window `json:"week"`  // past 7 days
	Month Window `json:"month"` // past 30 days
}

// rate is per-million-token pricing (USD/MTok).
type rate struct{ in, out, cacheWrite, cacheRead float64 }

// rateFor returns Anthropic list pricing by model family. Cache-write is the
// 5-minute rate (1.25x input); cache-read is 0.1x input. Current Opus (4.5/4.6/
// 4.7/4.8) lists at $5/$25 — a third of the legacy Opus 3/4/4.1 $15/$75 tier.
func rateFor(model string) rate {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "fable") || strings.Contains(m, "mythos"):
		return rate{10, 50, 12.5, 1.0}
	case strings.Contains(m, "opus"):
		return rate{5, 25, 6.25, 0.5}
	case strings.Contains(m, "haiku"):
		return rate{1, 5, 1.25, 0.1}
	default: // sonnet / unknown → sonnet tier
		return rate{3, 15, 3.75, 0.3}
	}
}

func cost(model string, t Tokens) float64 {
	r := rateFor(model)
	return (float64(t.Input)*r.in +
		float64(t.Output)*r.out +
		float64(t.CacheWrite)*r.cacheWrite +
		float64(t.CacheRead)*r.cacheRead) / 1e6
}

// transcriptLine is the subset of a Claude Code JSONL line we read.
type transcriptLine struct {
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Model string `json:"model"`
		Usage struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Parse reads transcript JSONL (assistant usage lines) and builds the report
// relative to now. Lines without a model or usage, or that don't parse, are
// skipped.
func Parse(data []byte, now time.Time) Report {
	type agg map[string]*Tokens // model -> tokens
	day, week, month := agg{}, agg{}, agg{}
	addTo := func(a agg, model string, t Tokens) {
		cur := a[model]
		if cur == nil {
			cur = &Tokens{}
			a[model] = cur
		}
		cur.add(t)
	}

	for _, raw := range splitLines(data) {
		if len(raw) == 0 {
			continue
		}
		var l transcriptLine
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		u := l.Message.Usage
		if l.Message.Model == "" || (u.Input == 0 && u.Output == 0 && u.CacheWrite == 0 && u.CacheRead == 0) {
			continue
		}
		age := now.Sub(l.Timestamp)
		if age < 0 || age > 30*24*time.Hour {
			continue
		}
		t := Tokens{Input: u.Input, Output: u.Output, CacheWrite: u.CacheWrite, CacheRead: u.CacheRead}
		addTo(month, l.Message.Model, t)
		if age <= 7*24*time.Hour {
			addTo(week, l.Message.Model, t)
		}
		if age <= 24*time.Hour {
			addTo(day, l.Message.Model, t)
		}
	}
	return Report{Day: window(day), Week: window(week), Month: window(month)}
}

func window(a map[string]*Tokens) Window {
	var w Window
	for model, t := range a {
		c := cost(model, *t)
		w.Models = append(w.Models, ModelUsage{Model: model, Tokens: *t, Cost: c})
		w.Total.add(*t)
		w.Cost += c
	}
	sort.Slice(w.Models, func(i, j int) bool { return w.Models[i].Cost > w.Models[j].Cost })
	return w
}

func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, trimCR(data[start:i]))
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, trimCR(data[start:]))
	}
	return out
}

func trimCR(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\r' {
		return b[:n-1]
	}
	return b
}
