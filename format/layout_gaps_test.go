package format

import (
	"bytes"
	"strings"
	"testing"
)

func formatOK(t *testing.T, src string) string {
	t.Helper()
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format(%q) error: %v", src, err)
	}
	return got
}

// TestUnaryMinusNoSpace is a regression test for a real bug: unary minus
// was getting a stray space ("point(- 0.12, ...)" instead of
// "point(-0.12, ...)") because spaceBetween couldn't distinguish a unary
// sign from a binary operator using only the immediately adjacent token.
func TestUnaryMinusNoSpace(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"select -1;", "select -1;\n"},
		{"select (-1);", "select (-1);\n"},
		{"select point(-0.12, 51.516);", "select point(-0.12, 51.516);\n"},
		{"select f(-1, -2);", "select f(-1, -2);\n"},
		{"select case when x then -1 else -2 end;", "select case when x then -1 else -2 end;\n"},
	}
	for _, c := range cases {
		got := formatOK(t, c.src)
		if got != c.want {
			t.Errorf("Format(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}

// TestBinaryMinusHasSpace guards the other side of the same fix: a real
// subtraction must still get a space on both sides, not collapse into
// unary-style tight spacing just because the fix above exists.
func TestBinaryMinusHasSpace(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"select a-b;", "select a - b;\n"},
		{"select 1-2;", "select 1 - 2;\n"},
		{"select -1 - -2;", "select -1 - -2;\n"}, // unary, binary, unary
	}
	for _, c := range cases {
		got := formatOK(t, c.src)
		if got != c.want {
			t.Errorf("Format(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}

// TestArraySubscriptNoSpace is a regression test for a bug found in the
// same corpus file as the unary-minus one: no space belongs before "["
// regardless of what precedes it ("(pos)[0]", "arr[1]").
func TestArraySubscriptNoSpace(t *testing.T) {
	got := formatOK(t, "select (pos)[0], arr[1];")
	want := "select (pos)[0], arr[1];\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestUnionAllStartsOwnLine is a regression test for a real bug: UNION/
// UNION ALL/INTERSECT/EXCEPT weren't recognized as clause boundaries, so
// e.g. "union all" between two SELECTs got glued onto the end of the first
// SELECT's WHERE clause body instead of starting its own line.
func TestUnionAllStartsOwnLine(t *testing.T) {
	src := "select a from t where x = 1 union all select b from t2;"
	got := formatOK(t, src)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	found := false
	for _, l := range lines {
		if strings.TrimSpace(l) == "union all" {
			found = true
		}
		if strings.Contains(l, "union all") && strings.TrimSpace(l) != "union all" {
			t.Fatalf("\"union all\" glued onto another line instead of starting its own: %q\nfull output:\n%s", l, got)
		}
	}
	if !found {
		t.Fatalf("no standalone \"union all\" line found in output:\n%s", got)
	}
}

// TestWithRecursivePreserved is a regression test for a real bug: parseCTEs
// consumed the "recursive" keyword to skip past it but never re-emitted it
// anywhere, so "with recursive" silently became "with" -- a correctness
// bug (dropping RECURSIVE changes what the query does), not just cosmetic.
func TestWithRecursivePreserved(t *testing.T) {
	src := "with recursive t as (select 1 union all select n+1 from t where n < 10) select * from t;"
	got := formatOK(t, src)
	if !strings.HasPrefix(strings.TrimLeft(got, " "), "with recursive ") {
		t.Errorf("\"with recursive\" not preserved, got:\n%s", got)
	}
}

// TestUnionSegmentsIndependentlyFormatted checks that each side of a UNION
// is formatted (and rendered) without erroring, with "union all" as its own
// standalone line between them -- each SELECT gets its own independent
// river-alignment width, rather than the two SELECTs' clause lists being
// merged into one shared computation the way a single query's own clauses
// are (they're not the same query).
func TestUnionSegmentsIndependentlyFormatted(t *testing.T) {
	src := "select short from t1 union all select much_longer_column_name from t2;"
	got := formatOK(t, src)
	if !strings.Contains(got, "\nunion all\n") {
		t.Errorf("expected a standalone \"union all\" line, got:\n%s", got)
	}
}
