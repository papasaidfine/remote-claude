package sshdconf

import (
	"strings"
	"testing"
)

func TestSetDirectiveReplacesActive(t *testing.T) {
	in := "Port 22\nPasswordAuthentication yes\nX11Forwarding no\n"
	out := SetDirective(in, "PasswordAuthentication", "no")
	if !strings.Contains(out, "PasswordAuthentication no") {
		t.Errorf("directive not set:\n%s", out)
	}
	if strings.Contains(out, "PasswordAuthentication yes") {
		t.Errorf("old value still present:\n%s", out)
	}
	if strings.Count(out, "PasswordAuthentication") != 1 {
		t.Errorf("expected one PasswordAuthentication line:\n%s", out)
	}
}

func TestSetDirectiveReplacesCommented(t *testing.T) {
	in := "#PubkeyAuthentication yes\n"
	out := SetDirective(in, "PubkeyAuthentication", "yes")
	if strings.TrimSpace(out) != "PubkeyAuthentication yes" {
		t.Errorf("commented directive not activated: %q", out)
	}
}

func TestSetDirectiveAppendsWhenMissing(t *testing.T) {
	in := "Port 22\n"
	out := SetDirective(in, "PubkeyAuthentication", "yes")
	if !strings.Contains(out, "Port 22") || !strings.HasSuffix(out, "PubkeyAuthentication yes\n") {
		t.Errorf("directive not appended:\n%s", out)
	}
}

func TestCommentOutMatchAndIdempotent(t *testing.T) {
	pat := `(?m)^([ \t]*Match[ \t]+Group[ \t]+administrators[ \t]*)$`
	in := "Match Group administrators\n    AuthorizedKeysFile x\n"
	out := CommentOut(in, pat, "claude-bootstrap disabled")
	if !strings.Contains(out, "# claude-bootstrap disabled: Match Group administrators") {
		t.Errorf("Match line not commented:\n%s", out)
	}
	// Re-running must not double-comment (the line now starts with '#').
	again := CommentOut(out, pat, "claude-bootstrap disabled")
	if strings.Count(again, "claude-bootstrap disabled") != 1 {
		t.Errorf("CommentOut not idempotent:\n%s", again)
	}
}
