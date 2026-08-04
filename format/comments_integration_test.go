package format

import (
	"bytes"
	"strings"
	"testing"
)

// TestLineCommentDoesNotSwallowFollowingTokens is a regression test for a
// real correctness bug: a "--" comment not at the end of a statement used
// to be echoed inline with no line break, silently absorbing every token
// that followed it on the same rendered line into the comment text --
// `select id -- inline\nfrom users;` rendered as `select id -- inline from
// users;`, an entirely different (and, if re-parsed, differently-behaving)
// query. "from"/"users" must appear as real, uncommented tokens.
func TestLineCommentDoesNotSwallowFollowingTokens(t *testing.T) {
	src := "select id -- inline\nfrom users;"
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	for _, line := range strings.Split(got, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			if strings.Contains(line[idx:], "from") || strings.Contains(line[idx:], "users") {
				t.Fatalf("comment absorbed following tokens: %q\nfull output:\n%s", line, got)
			}
		}
	}
	if !strings.Contains(got, "from users") {
		t.Errorf("from/users clause missing from output entirely:\n%s", got)
	}
}

func TestHeaderCommentPreserved(t *testing.T) {
	src := "-- a header comment\nselect id from users;"
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if !strings.HasPrefix(got, "-- a header comment\n") {
		t.Errorf("header comment not preserved as the first line, got:\n%s", got)
	}
}

func TestTrailingCommentPreserved(t *testing.T) {
	src := "select id from users; -- note"
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if !strings.Contains(got, "-- note") {
		t.Errorf("trailing comment not preserved, got:\n%s", got)
	}
}

func TestBlockCommentPreservedAndReformatted(t *testing.T) {
	src := "/* a block comment */\nselect id from users;"
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	want := "/*\n * a block comment\n */\nselect id from users;\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestFormatIsIdempotentWithComments guards against a real bug found during
// development: a leading comment attached to a clause KEYWORD's own token
// (which is what a second formatting pass produces -- by then the comment
// already sits on its own line directly before "from" rather than before
// the first token of its body) must still be found and rendered, not
// silently dropped, or Format(Format(x)) != Format(x).
func TestFormatIsIdempotentWithComments(t *testing.T) {
	cases := []string{
		"-- header\nselect id from users;",
		"select id, name -- trailing\n  from users;",
		"select id\n  /* a block comment about the from clause */\n  from users;",
		"select id from users; -- trailing on the statement",
	}
	for _, src := range cases {
		once, err := Format(bytes.NewReader([]byte(src)))
		if err != nil {
			t.Fatalf("Format(%q) error: %v", src, err)
		}
		twice, err := Format(bytes.NewReader([]byte(once)))
		if err != nil {
			t.Fatalf("Format(Format(%q)) error: %v", src, err)
		}
		if once != twice {
			t.Errorf("not idempotent for %q:\n--- once ---\n%s\n--- twice ---\n%s", src, once, twice)
		}
	}
}
