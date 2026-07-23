package i18n

import "testing"

// TestEveryKeyTranslated guards against a message key that is missing a language
// — the most common i18n regression when someone adds a string.
func TestEveryKeyTranslated(t *testing.T) {
	for key, m := range messages {
		for _, l := range Available {
			if s, ok := m[l]; !ok || s == "" {
				t.Errorf("key %q missing %s translation", key, l)
			}
		}
	}
}

func TestParse(t *testing.T) {
	if Parse("zh") != ZH {
		t.Error("Parse(zh) should be ZH")
	}
	if Parse("en") != EN {
		t.Error("Parse(en) should be EN")
	}
	// Unknown/empty falls back to Detect (which returns a valid language).
	if got := Parse("  "); got != EN && got != ZH {
		t.Errorf("Parse(blank) = %q, want a valid language", got)
	}
}

func TestTFallback(t *testing.T) {
	// A missing key returns the key itself.
	if got := T(EN, "no_such_key_xyz"); got != "no_such_key_xyz" {
		t.Errorf("missing key = %q, want the key echoed back", got)
	}
	if T(ZH, "save") != "保存" {
		t.Errorf("T(ZH, save) = %q", T(ZH, "save"))
	}
}
