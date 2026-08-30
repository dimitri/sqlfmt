package format

import (
	"bytes"
	"strings"
	"testing"
)

// fmtOne is a Format() wrapper for the single-statement cases below.
func fmtOne(t *testing.T, src string) string {
	t.Helper()
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format(%q) error: %v", src, err)
	}
	return got
}

// TestExplainJoinsTheRiver is the core of STYLE.md rule 19: "explain" is a
// clause keyword like "select" or "order by", padded into the same river as
// the statement it wraps -- not a prefix bolted on above it.
//
// Before EXPLAIN was handled at all, "explain" lexed as a bare identifier,
// statementKeyword returned "", and formatStatement's default arm ran
// flatJoin over the whole statement: any EXPLAIN cost you every clause break
// in the query underneath it, silently and with exit status 0.
func TestExplainJoinsTheRiver(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			// "explain" (7) is itself the widest keyword here, so it sets
			// the river and the query's own clauses indent under it.
			name: "explain sets the river",
			src:  "explain (analyze, buffers) select a, b from t where c = 1;",
			want: "explain (analyze, buffers)\n" +
				" select a, b\n" +
				"   from t\n" +
				"  where c = 1;\n",
		},
		{
			// "order by" (8) is wider, so it sets the river instead and the
			// EXPLAIN line indents by one -- the whole point of aligning it
			// rather than pinning it to column 0.
			name: "order by is wider and sets the river",
			src:  "explain (costs off, buffers, analyze) select name, location, country from circuits order by position <-> point(2.349014, 48.864716);",
			want: " explain (costs off, buffers, analyze)\n" +
				"  select name, location, country\n" +
				"    from circuits\n" +
				"order by position <-> point(2.349014, 48.864716);\n",
		},
		{
			name: "group by and order by together",
			src:  "explain (analyze, buffers) select a, b from t group by a order by b;",
			want: " explain (analyze, buffers)\n" +
				"  select a, b\n" +
				"    from t\n" +
				"group by a\n" +
				"order by b;\n",
		},
		{
			// A JOIN phrase can widen the river past every clause keyword
			// (riverWidth's maxJoinPhraseLen arm); EXPLAIN follows it.
			name: "a join phrase widens the river",
			src:  "explain select title, name from album left join track using(album_id) where album_id = 1;",
			want: "    explain\n" +
				"     select title, name\n" +
				"       from album\n" +
				"  left join track using(album_id)\n" +
				"      where album_id = 1;\n",
		},
		{
			name: "bare explain, no option list",
			src:  "explain select a, b from t where c = 1;",
			want: "explain\n select a, b\n   from t\n  where c = 1;\n",
		},
		{
			// The legacy spelling the grammar still accepts. It is preserved
			// as written, not rewritten into the parenthesized form (rule 1:
			// never change content). Its extra words are the clause's body,
			// so they do not widen the river.
			name: "legacy bare analyze verbose",
			src:  "explain analyze verbose select a, b from t where c = 1;",
			want: "explain analyze verbose\n select a, b\n   from t\n  where c = 1;\n",
		},
		{
			name: "long option list does not widen the river",
			src:  "explain (analyze, buffers, costs off, timing off, summary off) select a from t;",
			want: "explain (analyze, buffers, costs off, timing off, summary off)\n" +
				" select a\n" +
				"   from t;\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fmtOne(t, tc.src); got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// TestExplainNonSelectPayloads covers the DML statements EXPLAIN accepts:
// each still forms a clause river, so EXPLAIN aligns into it the same way.
func TestExplainNonSelectPayloads(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "insert",
			src:  "explain (analyze) insert into t (a) values (1);",
			want: "    explain (analyze)\ninsert into t(a)\n     values (1);\n",
		},
		{
			name: "update",
			src:  "explain (analyze) update t set a = 1 where b = 2;",
			want: "explain (analyze)\n update t\n    set a = 1\n  where b = 2;\n",
		},
		{
			name: "delete",
			src:  "explain (analyze) delete from t where b = 2;",
			want: "explain (analyze)\n delete\n   from t\n  where b = 2;\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fmtOne(t, tc.src); got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// TestExplainPayloadsWithNoRiver covers the statements that have no single
// top-level clause river for EXPLAIN to join. They fall back to an unpadded
// prefix line plus the statement formatted as it would be on its own.
func TestExplainPayloadsWithNoRiver(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			// EXECUTE is not a clause keyword at all.
			name: "execute",
			src:  "explain (analyze) execute p(1);",
			want: "explain (analyze)\nexecute p(1);\n",
		},
		{
			// A CTE list gets its own per-CTE layout; there is no top-level
			// river to align with.
			name: "with",
			src:  "explain (analyze) with x as (select 1 as n) select n from x;",
			want: "explain (analyze)\nwith x as (\n  select 1 as n\n)\nselect n from x;\n",
		},
		{
			name: "explain with nothing to wrap",
			src:  "explain;",
			want: "explain;\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fmtOne(t, tc.src); got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// TestExplainPrefixAlwaysBreaks: the EXPLAIN line keeps a line of its own
// even when the whole statement would fit inline, so what is being explained
// still reads as a statement and not as an argument (fitsInline's rule-19
// arm).
func TestExplainPrefixAlwaysBreaks(t *testing.T) {
	got := fmtOne(t, "explain select a from t;")
	if strings.HasPrefix(strings.TrimLeft(got, " "), "explain select") {
		t.Errorf("EXPLAIN collapsed onto one line with its payload:\n%s", got)
	}
	if lines := strings.Split(strings.TrimRight(got, "\n"), "\n"); len(lines) < 2 {
		t.Errorf("expected the prefix on its own line, got:\n%s", got)
	}
}

// TestExplainNoTrailingWhitespace guards the empty-body case: "explain" with
// no option list must not leave the clause-keyword/body separator behind.
func TestExplainNoTrailingWhitespace(t *testing.T) {
	for _, src := range []string{
		"explain select a, b from t where c = 1;",
		"explain (analyze) select a, b from t where c = 1;",
		"explain analyze select a from t;",
	} {
		for _, line := range strings.Split(fmtOne(t, src), "\n") {
			if line != strings.TrimRight(line, " \t") {
				t.Errorf("trailing whitespace in %q output line: %q", src, line)
			}
		}
	}
}

// TestExplainIdempotent guards the property the pre-filter use depends on:
// formatting already-formatted EXPLAIN output changes nothing. It is the
// river cases that make this worth testing -- the EXPLAIN line's own
// indentation is input to the next parse.
func TestExplainIdempotent(t *testing.T) {
	srcs := []string{
		"explain select a, b from t where c = 1;",
		"explain (analyze, buffers) select a from t;",
		"explain analyze select a from t;",
		"explain (analyze, buffers) select a, b from t group by a order by b;",
		"explain select title, name from album left join track using(album_id) where album_id = 1;",
		"explain (analyze) insert into t (a) values (1);",
		"explain (analyze) execute p(1);",
		"explain (analyze) with x as (select 1 as n) select n from x;",
	}
	for _, src := range srcs {
		once := fmtOne(t, src)
		twice := fmtOne(t, once)
		if once != twice {
			t.Errorf("not idempotent for %q:\nfirst:\n%s\nsecond:\n%s", src, once, twice)
		}
	}
}

// TestExplainPreservesComments checks that a comment header above an
// EXPLAIN -- the shape every course example uses -- still survives.
func TestExplainPreservesComments(t *testing.T) {
	got := fmtOne(t, "-- why this plan matters\nexplain (analyze) select a, b from t where c = 1;")
	if !strings.HasPrefix(got, "-- why this plan matters\n") {
		t.Errorf("leading comment lost:\n%s", got)
	}
	if !strings.Contains(got, "explain (analyze)\n select a, b\n") {
		t.Errorf("explain not river-aligned after its comment:\n%s", got)
	}
}

// TestExplainKeywordDoesNotBreakIdentifiers: adding "explain" to the keyword
// table must not change how it lexes elsewhere in a statement -- a column
// named "explain" is still just a column.
func TestExplainKeywordAsColumnName(t *testing.T) {
	got := fmtOne(t, "select explain from t;")
	if !strings.Contains(got, "select explain") {
		t.Errorf("column named explain mangled:\n%s", got)
	}
}
