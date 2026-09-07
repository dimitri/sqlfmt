package format

import (
	"strings"
	"testing"
)

// leftCol returns the indent of the first line whose trimmed text starts
// with want, or -1.
func leftCol(out, want string) int {
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimLeft(l, " ")
		if strings.HasPrefix(t, want) {
			return len(l) - len(t)
		}
	}
	return -1
}

// A subquery after EXISTS gets its own bracket: the paren moves to a line
// of its own at the EXISTS column, the body is indented inside it, and the
// closing paren returns to that column.
//
// Leaving the paren at the end of "where exists (" reads as though the
// subquery were part of the predicate rather than its whole right-hand
// side, and leaves the body hanging off a column the eye has no reason to
// expect. The three lines together make the extent of the subquery
// visible without reading it.
//
// The regression underneath this is worse than a wrong indent: the body's
// own clause keywords were river-aligned at one base while its first line
// was emitted at another, so SELECT sat two columns right of the FROM and
// WHERE that were supposed to align with it.
func TestSubqueryAfterExistsBracketsUnderKeyword(t *testing.T) {
	out := fmtOne(t, `select d.surname from f1db.drivers d
where exists (select 1 from f1db.results res
join f1db.races r on r.raceid = res.raceid
where res.driverid = d.driverid and r.year = 2017 and res.positionorder = 1)
order by d.surname;`)

	exists := leftCol(out, "where exists") + len("where ")
	if exists < len("where ") {
		t.Fatalf("no exists line:\n%s", out)
	}
	if !strings.Contains(out, "\n"+strings.Repeat(" ", exists)+"(\n") {
		t.Errorf("open paren not alone on the EXISTS column %d:\n%s", exists, out)
	}
	if !strings.Contains(out, "\n"+strings.Repeat(" ", exists)+")\n") {
		t.Errorf("close paren not alone on the EXISTS column %d:\n%s", exists, out)
	}
	// select is the longest keyword at that level, so it is the river's
	// left edge: two columns inside the paren.
	if c := leftCol(out, "select 1"); c != exists+2 {
		t.Errorf("subquery river at %d, want %d (inside the paren):\n%s", c, exists+2, out)
	}
	// FROM is one shorter than SELECT, so it sits two columns right of it.
	if c := leftCol(out, "from f1db.results"); c != exists+4 {
		t.Errorf("subquery FROM at %d, want %d:\n%s", c, exists+4, out)
	}
}

// NOT EXISTS brackets under the "not", not under the "exists".
func TestSubqueryAfterNotExistsBracketsUnderNot(t *testing.T) {
	out := fmtOne(t, `select a from t
where not exists (select 1 from u where u.id = t.id and u.x = 1
and u.y = 2 and u.zzzzzzzzzzzz = 3 and u.wwwwwwwwww = 4);`)
	not := leftCol(out, "where not exists") + len("where ")
	if !strings.Contains(out, "\n"+strings.Repeat(" ", not)+"(\n") {
		t.Errorf("open paren not alone on the NOT column %d:\n%s", not, out)
	}
	if c := leftCol(out, "select 1"); c != not+2 {
		t.Errorf("subquery river at %d, want %d:\n%s", c, not+2, out)
	}
}

// A scalar subquery in the select list opens with its own paren, so the
// body is indented inside that paren and the closing paren returns to it.
func TestScalarSubqueryIndentsInsideItsParen(t *testing.T) {
	out := fmtOne(t, `select ds.driverid, ds.points,
(select count(*) + 1 from f1db.driverstandings inner_ds
where inner_ds.raceid = ds.raceid and inner_ds.points > ds.points) as rank
from f1db.driverstandings ds order by rank;`)

	open := leftCol(out, "(")
	if open < 0 {
		t.Fatalf("no lone open paren:\n%s", out)
	}
	if c := leftCol(out, "select count(*)"); c != open+2 {
		t.Errorf("body at %d, want %d (inside the paren):\n%s", c, open+2, out)
	}
	if c := leftCol(out, ") as rank"); c != open {
		t.Errorf("closing paren at %d, want %d (the opening paren's column):\n%s", c, open, out)
	}
}

// A derived table aligns to the JOIN phrase. layoutFrom folds the join
// keywords into a phrase before rendering the relation, so the paren path
// cannot see the LATERAL that marks where the construct starts -- this
// pins the separate path that handles it.
func TestLateralDerivedTableAlignsToJoinPhrase(t *testing.T) {
	out := fmtOne(t, `select decade, wins from decades
left join lateral (select code, forename, surname, count(*) as wins
from drivers join results on results.driverid = drivers.driverid
where extract('year' from date_trunc('decade', races.date)) = decades.decade
group by decades.decade, drivers.driverid order by wins desc limit 3) as winners
on true order by decade;`)

	join := leftCol(out, "left join lateral (")
	if join < 0 {
		t.Fatalf("no lateral join line:\n%s", out)
	}
	if c := leftCol(out, ") as winners"); c != join {
		t.Errorf("closing paren at %d, want %d (the join phrase's column):\n%s", c, join, out)
	}
	if c := leftCol(out, "group by decades.decade"); c != join+2 {
		t.Errorf("derived-table river at %d, want %d:\n%s", c, join+2, out)
	}
}

// STYLE.md rule 13: a CTE body is indented 2 from the CTE's own base. A
// body that wrapped used to land at 4, because renderCTE added the indent
// that formatQuerySegment had already applied -- while a body that fitted
// on one line came back unindented and so landed correctly. Same call,
// two contracts.
func TestCTEBodyIndentedTwo(t *testing.T) {
	out := fmtOne(t, `with c as (select id, name, x from u
where u.x = 1 and u.y = 2 and u.zzzzzzzzz = 3 and u.wwwwwwwww = 9)
select a from c;`)
	if c := leftCol(out, "select id"); c != 2 {
		t.Errorf("CTE body at %d, want 2:\n%s", c, out)
	}
}

// "any(" takes no space before its paren (rule 4), so the operator's
// column is one nearer the paren than a spaced introducer's would be.
func TestAnySubqueryBracketsUnderOperator(t *testing.T) {
	out := fmtOne(t, `select a from t where t.id = any (select u.id from u
where u.x = 1 and u.yyyyy = 22 and u.zzzzzzzz = 3 and u.wwwwwwwwww = 4);`)
	// "any" takes no space before its paren (rule 4), so the operator's
	// column has to be found on the line rather than assumed one space
	// left of the paren -- the off-by-one this pins.
	col := -1
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, "= any"); i >= 0 {
			col = i + len("= ")
		}
	}
	if col < 0 {
		t.Fatalf("no ANY line:\n%s", out)
	}
	if !strings.Contains(out, "\n"+strings.Repeat(" ", col)+"(\n") {
		t.Errorf("open paren not alone on the ANY column %d:\n%s", col, out)
	}
	if c := leftCol(out, "select u.id"); c != col+2 {
		t.Errorf("subquery river at %d, want %d:\n%s", c, col+2, out)
	}
}

// Formatting the formatter's own output must not move anything. Every
// indent bug above produced output that shifted again on a second pass.
func TestSubqueryLayoutIsIdempotent(t *testing.T) {
	for _, src := range []string{
		`select a from t where exists (select 1 from u where u.id = t.id
and u.x = 1 and u.yyyyyyyyyy = 2 and u.zzzzzzzzzz = 3 and u.w = 4);`,
		`select a, (select count(*) from u where u.id = t.id and u.x = 1
and u.yyyyyyyyyyyy = 2 and u.zzzzzzzzzzzz = 3) as n from t;`,
		`with c as (select id, name from u where u.x = 1 and u.yyyyyyyy = 2
and u.zzzzzzzzzz = 3) select a from c;`,
	} {
		once := fmtOne(t, src)
		twice := fmtOne(t, once)
		if once != twice {
			t.Errorf("not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
		}
	}
}
