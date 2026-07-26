package i18n

import (
	"strings"
	"testing"
)

// TestNoXrayInUserFacingText guards a product decision: the UI never names the
// proxy implementation. Keys are checked as well as values, because T echoes an
// unknown key straight to the screen (see TestTFallback) — so a key named
// "xray_download" can surface as literal UI text the day its translation is
// dropped.
func TestNoXrayInUserFacingText(t *testing.T) {
	for key, m := range messages {
		if strings.Contains(strings.ToLower(key), "xray") {
			t.Errorf("message key %q names xray; a missing translation echoes the key to the screen", key)
		}
		for lang, s := range m {
			if strings.Contains(strings.ToLower(s), "xray") {
				t.Errorf("key %q (%s) shows xray to the user: %q", key, lang, s)
			}
		}
	}
}

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
