package format

import "testing"

// WINDOW is a clause like HAVING or ORDER BY. Without a bound of its own it
// ran on at the end of the FROM clause, and its comma-separated definitions
// were never laid out as a list:
//
//	from (...) as t(x) window w1 as (...),
//	       w2 as (...);
func TestWindowClauseStartsItsOwnLine(t *testing.T) {
	src := "select x, array_agg(x) over w1 as rows, array_agg(x) over w2 as range\n" +
		"from (select x*x from generate_series(1, 10) as gs(x)) as t(x)\n" +
		"window w1 as (order by x rows between 1 preceding and 2 following),\n" +
		"w2 as (order by x range between 10 preceding and 20 following);\n"
	want := "select x, array_agg(x) over w1 as rows, array_agg(x) over w2 as range\n" +
		"  from (select x * x from generate_series(1, 10) as gs(x)) as t(x)\n" +
		"window w1 as (order by x rows between 1 preceding and 2 following),\n" +
		"       w2 as (order by x range between 10 preceding and 20 following);\n"
	if got := formatOK(t, src); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if again := formatOK(t, want); again != want {
		t.Errorf("not idempotent:\n%s", again)
	}
}

// WINDOW right-aligns into the river with the other clauses, and a short
// statement stays flat.
func TestWindowClauseJoinsTheRiver(t *testing.T) {
	flat := "select sum(x) over w from t window w as (partition by a) order by 1;\n"
	if got := formatOK(t, flat); got != flat {
		t.Errorf("short statement not flat:\n%s", got)
	}
	got := formatOK(t, "select sum(x) over w, row_number() over w as the_row_number_column_name\n"+
		"from some_table window w as (partition by a) order by 1;\n")
	want := "  select sum(x) over w, row_number() over w as the_row_number_column_name\n" +
		"    from some_table\n" +
		"  window w as (partition by a)\n" +
		"order by 1;\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
