package format

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func mustFormat(t *testing.T, src string) string {
	t.Helper()
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format(%q) error: %v", src, err)
	}
	return got
}

// squash reduces SQL to the part a reformat must never change: comments
// out, all whitespace out, case folded. Two strings that differ after this
// differ in content, not in layout.
func squash(s string) string {
	s = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`--[^\n]*`).ReplaceAllString(s, " ")
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// TestNoContentLost is the regression net for a family of bugs that all had
// the same shape: the formatter reached a construct it did not model, and
// silently dropped the tokens it could not place. Exit status 0, valid-
// looking output, missing SQL. Each case below lost something before the
// fixes in this commit.
func TestNoContentLost(t *testing.T) {
	cases := []struct {
		name string
		src  string
		lost string // a fragment that used to disappear
	}{
		{
			"simple CASE that has to wrap",
			"select case name when 'AAAAAAAAAAAAAAAAAAAA' then 'first value here' else 'second value here' end as label, count(*) from t group by name;",
			"'first value here'",
		},
		{
			"WITH ... AS MATERIALIZED",
			"with x as materialized (select 1 as n) select n from x;",
			"select 1 as n",
		},
		{
			"WITH ... AS NOT MATERIALIZED",
			"with x as not materialized (select 1 as n) select n from x;",
			"not materialized",
		},
		{
			"recursive CTE CYCLE clause",
			"with recursive b(i, p) as (select 1, '{}'::int[] union all select i+1, p from b where i < 4) cycle i set is_cycle using path select * from b;",
			"cycle i set is_cycle using path",
		},
		{
			"CREATE TABLE ... PARTITION BY",
			"create table t (a int, b int) partition by range (a);",
			"partition by range",
		},
		{
			"CREATE TABLE ... INHERITS",
			"create table t (a int) inherits (parent);",
			"inherits",
		},
		{
			"CREATE TABLE ... PARTITION OF",
			"create table t_2020 partition of t for values from (2020) to (2021);",
			"to (2021)",
		},
		{
			"CREATE TABLE ... AS SELECT",
			"create table t as select a, b from other where c = 1;",
			"where c = 1",
		},
		{
			"EXCEPT ALL keeps its ALL",
			"select a from t except all select a from u;",
			"except all",
		},
		{
			"INTERSECT ALL keeps its ALL",
			"select a from t intersect all select a from u;",
			"intersect all",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustFormat(t, tc.src)
			if squash(got) != squash(tc.src) {
				t.Errorf("content changed:\nin:  %s\nout: %s\n(squashed in:  %s)\n(squashed out: %s)",
					tc.src, got, squash(tc.src), squash(got))
			}
			if !strings.Contains(strings.ToLower(strings.Join(strings.Fields(got), " ")), tc.lost) {
				t.Errorf("lost %q from the output:\n%s", tc.lost, got)
			}
		})
	}
}

// TestSetOperatorModifiers: ALL and DISTINCT belong to the set operator, not
// to the query after it. Only UNION used to consume them, so "except all"
// rendered as an "except" line followed by a stray "all select ..." -- and
// dropping the ALL silently changes which rows the query returns.
func TestSetOperatorModifiers(t *testing.T) {
	for _, op := range []string{"union", "intersect", "except"} {
		for _, mod := range []string{"", " all", " distinct"} {
			src := "select a from t " + op + mod + " select a from u;"
			got := mustFormat(t, src)
			want := op + mod
			if !strings.Contains(got, want+"\n") {
				t.Errorf("%q: expected a %q line, got:\n%s", src, want, got)
			}
		}
	}
}

// TestBetweenIsOnePredicate: the "and" in BETWEEN joins two bounds of a
// single predicate and must not be treated as a boolean conjunction.
func TestBetweenIsOnePredicate(t *testing.T) {
	src := "select res.raceid, res.driverid, res.points, res.positionorder from f1db.results res where res.year between 2010 and 2017 and res.points > 0;"
	got := mustFormat(t, src)
	if !strings.Contains(got, "where res.year between 2010 and 2017\n") {
		t.Errorf("BETWEEN split across lines:\n%s", got)
	}
	if !strings.Contains(got, "and res.points > 0") {
		t.Errorf("the real conjunction was lost:\n%s", got)
	}
}

// TestWindowClausesEachOnTheirOwnLine covers STYLE.md rule 14. layoutOver
// searched for ORDER BY in the tokens *before* PARTITION BY -- empty for the
// usual "over(partition by x order by y)" -- so it never split: the whole
// window spec went out on one line and only the ")" moved down, leaving an
// over-long line with an orphaned paren under it.
func TestWindowClausesEachOnTheirOwnLine(t *testing.T) {
	src := "select driverid, sum(points) over (partition by driverid order by raceid rows between unbounded preceding and current row) as running from results;"
	got := mustFormat(t, src)
	for _, want := range []string{
		"over(\n",
		"partition by driverid\n",
		"order by raceid\n",
		"rows between unbounded preceding and current row\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > 90 {
			t.Errorf("line over 90 columns:\n%s", l)
		}
	}
}

// TestWindowOrderByOnly / TestWindowPartitionByOnly: the split must work
// with either clause missing.
func TestWindowOrderByOnly(t *testing.T) {
	got := mustFormat(t, "select x, array_agg(x) over (order by x rows between unbounded preceding and current row) as agg from t;")
	if !strings.Contains(got, "order by x\n") || !strings.Contains(got, "rows between unbounded preceding and current row\n") {
		t.Errorf("ORDER BY and frame not split:\n%s", got)
	}
}

func TestWindowPartitionByOnly(t *testing.T) {
	got := mustFormat(t, "select x, count(*) over (partition by aaaaaaaaaaaaaaaaaaaa, bbbbbbbbbbbbbbbbbbbb, cccccccccccccccccccc) as n from t;")
	if !strings.Contains(got, "partition by") {
		t.Errorf("PARTITION BY lost:\n%s", got)
	}
}

// TestIndentedCommentsPreserved: a comment whose text is itself indented is
// laid out on purpose -- an EXPLAIN plan sketch, an ASCII diagram -- and
// reflowing it to the width destroys the only thing it communicated. Prose
// comments still reflow.
func TestIndentedCommentsPreserved(t *testing.T) {
	src := "-- Limit\n--    -> Index Scan using idx on t\n--         Filter: (a = 1)\nselect a from t;\n"
	got := mustFormat(t, src)
	for _, want := range []string{"--    -> Index Scan using idx on t", "--         Filter: (a = 1)"} {
		if !strings.Contains(got, want) {
			t.Errorf("comment indentation lost (want %q):\n%s", want, got)
		}
	}
}

func TestProseCommentsStillReflow(t *testing.T) {
	src := "-- a normal prose comment that is long enough that it really ought to be reflowed by the formatter to the target width\nselect a from t;\n"
	got := mustFormat(t, src)
	if strings.Count(got, "--") < 2 {
		t.Errorf("prose comment was not reflowed:\n%s", got)
	}
}

// TestAllFixesIdempotent: every construct above must survive a second pass
// unchanged, since the pre-filter this feeds re-runs over its own output.
func TestAllFixesIdempotent(t *testing.T) {
	srcs := []string{
		"select case name when 'AAAAAAAAAAAAAAAAAAAA' then 'first value here' else 'second value here' end as label from t;",
		"with x as materialized (select 1 as n) select n from x;",
		"with recursive b(i, p) as (select 1, '{}'::int[] union all select i+1, p from b where i < 4) cycle i set is_cycle using path select * from b;",
		"create table t (a int, b int) partition by range (a);",
		"create table t as select a, b from other where c = 1;",
		"select a from t except all select a from u;",
		"select res.raceid from f1db.results res where res.year between 2010 and 2017 and res.points > 0;",
		"select driverid, sum(points) over (partition by driverid order by raceid rows between unbounded preceding and current row) as running from results;",
		"-- Limit\n--    -> Index Scan using idx on t\nselect a from t;\n",
	}
	for _, src := range srcs {
		once := mustFormat(t, src)
		twice := mustFormat(t, once)
		if once != twice {
			t.Errorf("not idempotent for %q:\nfirst:\n%s\nsecond:\n%s", src, once, twice)
		}
	}
}
