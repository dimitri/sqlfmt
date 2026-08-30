package format

import (
	"bytes"
	"strings"
	"testing"
)

// TestExplainPrefixIsItsOwnLine covers STYLE.md rule 19. Before EXPLAIN was
// handled, "explain" lexed as a bare identifier, statementKeyword returned
// "", and formatStatement's default arm flattened the whole statement --
// EXPLAIN silently cost you every clause break in the query it wrapped.
func TestExplainPrefixIsItsOwnLine(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "bare explain",
			src:  "explain select a, b from t where c = 1;",
			want: "explain\nselect a, b\n  from t\n where c = 1;\n",
		},
		{
			name: "parenthesized option list",
			src:  "explain (analyze, buffers) select a, b from t where c = 1;",
			want: "explain (analyze, buffers)\nselect a, b\n  from t\n where c = 1;\n",
		},
		{
			name: "legacy bare analyze verbose",
			src:  "explain analyze verbose select a, b from t where c = 1;",
			want: "explain analyze verbose\nselect a, b\n  from t\n where c = 1;\n",
		},
		{
			name: "option list with a two-word option",
			src:  "explain (costs off, analyze) select a, b from t where c = 1;",
			want: "explain (costs off, analyze)\nselect a, b\n  from t\n where c = 1;\n",
		},
		{
			// The wrapped statement need not be a SELECT; whatever it is,
			// it gets formatted the way it would be on its own.
			name: "non-select payload",
			src:  "explain (analyze) execute p(1);",
			want: "explain (analyze)\nexecute p(1);\n",
		},
		{
			name: "explain with nothing to wrap",
			src:  "explain;",
			want: "explain;\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Format(bytes.NewReader([]byte(tc.src)))
			if err != nil {
				t.Fatalf("Format error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// TestExplainKeepsInnerRiver is the regression the bug report was actually
// about: the river alignment of the wrapped query must survive, including
// the case where the longest clause keyword ("order by") indents "select".
func TestExplainKeepsInnerRiver(t *testing.T) {
	src := "explain (costs off, buffers, analyze) select name, location, country from circuits order by position <-> point(2.349014, 48.864716);"
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	want := "explain (costs off, buffers, analyze)\n" +
		"  select name, location, country\n" +
		"    from circuits\n" +
		"order by position <-> point(2.349014, 48.864716);\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestExplainIdempotent guards the property the pre-commit/CI use depends
// on: formatting already-formatted EXPLAIN output changes nothing.
func TestExplainIdempotent(t *testing.T) {
	srcs := []string{
		"explain select a, b from t where c = 1;",
		"explain (analyze, buffers) select a from t;",
		"explain analyze select a from t;",
	}
	for _, src := range srcs {
		once, err := Format(bytes.NewReader([]byte(src)))
		if err != nil {
			t.Fatalf("Format error: %v", err)
		}
		twice, err := Format(bytes.NewReader([]byte(once)))
		if err != nil {
			t.Fatalf("Format error on second pass: %v", err)
		}
		if once != twice {
			t.Errorf("not idempotent for %q:\nfirst:\n%s\nsecond:\n%s", src, once, twice)
		}
	}
}

// TestExplainPreservesComments checks that a comment header above an
// EXPLAIN -- the shape every course example uses -- still survives.
func TestExplainPreservesComments(t *testing.T) {
	src := "-- why this plan matters\nexplain (analyze) select a from t;"
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if !strings.HasPrefix(got, "-- why this plan matters\n") {
		t.Errorf("leading comment lost:\n%s", got)
	}
	if !strings.Contains(got, "explain (analyze)\nselect a from t;") {
		t.Errorf("explain not laid out on its own line:\n%s", got)
	}
}
