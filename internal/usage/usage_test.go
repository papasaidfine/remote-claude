package usage

import (
	"fmt"
	"testing"
	"time"
)

func line(ts time.Time, model string, in, out, cw, cr int64) string {
	return fmt.Sprintf(`{"timestamp":%q,"message":{"model":%q,"usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d}}}`,
		ts.Format(time.RFC3339), model, in, out, cw, cr)
}

func TestParseWindowsAndAggregation(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	data := []byte(
		line(now.Add(-1*time.Hour), "claude-opus-4-8", 1000, 500, 200, 3000) + "\n" +
			line(now.Add(-2*time.Hour), "claude-opus-4-8", 1000, 500, 0, 0) + "\n" + // same model, same day → aggregates
			line(now.Add(-3*24*time.Hour), "claude-sonnet-5", 2000, 1000, 0, 0) + "\n" +
			line(now.Add(-20*24*time.Hour), "claude-haiku-4-5", 500, 100, 0, 0) + "\n" +
			line(now.Add(-40*24*time.Hour), "claude-opus-4-8", 9999999, 0, 0, 0) + "\n" + // >30d → excluded
			`garbage not json` + "\n" +
			`{"timestamp":"2026-07-23T11:00:00Z","message":{"model":"x"}}` + "\n") // no usage → skipped

	r := Parse(data, now)

	// Day: only the two opus entries (aggregated).
	if len(r.Day.Models) != 1 || r.Day.Models[0].Model != "claude-opus-4-8" {
		t.Fatalf("day models = %+v", r.Day.Models)
	}
	if r.Day.Total.Input != 2000 || r.Day.Total.Output != 1000 || r.Day.Total.CacheWrite != 200 || r.Day.Total.CacheRead != 3000 {
		t.Errorf("day total = %+v", r.Day.Total)
	}
	// Week: opus + sonnet.
	if len(r.Week.Models) != 2 {
		t.Errorf("week models = %d, want 2", len(r.Week.Models))
	}
	// Month: opus + sonnet + haiku (the 40d one excluded).
	if len(r.Month.Models) != 3 {
		t.Errorf("month models = %d, want 3", len(r.Month.Models))
	}
}

func TestPricingByFamily(t *testing.T) {
	// opus: 1000 in, 500 out, 200 cacheW, 3000 cacheR
	got := cost("claude-opus-4-8", Tokens{Input: 1000, Output: 500, CacheWrite: 200, CacheRead: 3000})
	want := (1000*15.0 + 500*75.0 + 200*18.75 + 3000*1.5) / 1e6
	if got != want {
		t.Errorf("opus cost = %v, want %v", got, want)
	}
	if cost("claude-haiku-4-5", Tokens{Input: 1_000_000}) != 1.0 {
		t.Errorf("haiku 1M input should be $1")
	}
	if cost("claude-sonnet-5", Tokens{Output: 1_000_000}) != 15.0 {
		t.Errorf("sonnet 1M output should be $15")
	}
}

func TestModelsSortedByCostDesc(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	data := []byte(
		line(now, "claude-haiku-4-5", 1000, 0, 0, 0) + "\n" +
			line(now, "claude-opus-4-8", 1000, 0, 0, 0) + "\n")
	r := Parse(data, now)
	if len(r.Day.Models) != 2 || r.Day.Models[0].Model != "claude-opus-4-8" {
		t.Errorf("models not cost-sorted: %+v", r.Day.Models)
	}
}
