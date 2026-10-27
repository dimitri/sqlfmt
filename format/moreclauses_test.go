package format

import "testing"

// Clause words that had no bound of their own, and so ran on at the end of
// whatever preceded them. Each case is a statement too wide for one line, so
// the clause has to land on a line of its own, right-aligned into the river.
func TestMoreClausesStartTheirOwnLine(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"fetch after order by",
			"select a, b, c from some_long_table_name where a = 1 order by a fetch first 10 rows with ties;\n",
			"  select a, b, c\n    from some_long_table_name\n   where a = 1\norder by a\n   fetch first 10 rows with ties;\n"},
		{"fetch after offset",
			"select a, b, c from some_long_table_name where a = 1 order by a offset 5 rows fetch first 10 rows only;\n",
			"  select a, b, c\n    from some_long_table_name\n   where a = 1\norder by a\n  offset 5 rows\n   fetch first 10 rows only;\n"},
		{"select into",
			"select a, b into newtable from some_long_table_name_here where a = 1 and b = 2 and c = 3;\n",
			"select a, b\n  into newtable\n  from some_long_table_name_here\n where a = 1\n   and b = 2\n   and c = 3;\n"},
		{"delete using",
			"delete from some_long_table_name using other_table where some_long_table_name.id = other_table.id and other_table.x = 1;\n",
			"delete\n  from some_long_table_name\n using other_table\n where some_long_table_name.id = other_table.id\n   and other_table.x = 1;\n"},
		{"with check option",
			"create view v as select a, b, c from some_long_table_name where a = 1 and b = 2 and c = 3 with check option;\n",
			"create view v as\nselect a, b, c\n  from some_long_table_name\n where a = 1\n   and b = 2\n   and c = 3\n  with check option;\n"},
		{"with no data",
			"create materialized view v as select a, b, c from some_long_table_name where a = 1 and b = 2 and c = 3 with no data;\n",
			"create materialized view v as\nselect a, b, c\n  from some_long_table_name\n where a = 1\n   and b = 2\n   and c = 3\n  with no data;\n"},
	}
	for _, c := range cases {
		got := formatOK(t, c.src)
		if got != c.want {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", c.name, got, c.want)
		}
		if again := formatOK(t, got); again != got {
			t.Errorf("%s: not idempotent:\n%s", c.name, again)
		}
	}
}

// The first USING of a DELETE is its clause; a later one belongs to a JOIN.
// And the INTO of INSERT INTO is not SELECT's INTO clause.
func TestUsingAndIntoStayWhereTheyBelong(t *testing.T) {
	flat := []string{
		"select *\n  from a\n  join b using(id);\n",
		"insert into t(a) select a from u;\n",
		"delete from t using u where t.id = u.id;\n",
	}
	for _, src := range flat {
		if got := formatOK(t, src); got != src {
			t.Errorf("got %q, want %q", got, src)
		}
	}
}

// "left outer join" and "natural left join" are one phrase each. Splitting
// at every modifier left "left" stranded on a line of its own.
func TestMultiWordJoinPhrases(t *testing.T) {
	got := formatOK(t, "select * from a left outer join b on a.id = b.id natural left join c natural join d;\n")
	want := "             select *\n" +
		"               from a\n" +
		"    left outer join b on a.id = b.id\n" +
		"  natural left join c\n" +
		"       natural join d;\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// COPY (query) TO lays the query out as a derived table; it came out as one
// line with the query's indentation spaces inside it.
func TestCopyQueryAndDeclareCursor(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"copy query",
			"copy (select a, b, c from some_long_table_name where a = 1 and b = 2 and c = 3) to stdout with (format csv);\n",
			"copy (\n  select a, b, c\n    from some_long_table_name\n   where a = 1\n     and b = 2\n     and c = 3\n) to stdout with (format csv);\n"},
		{"copy short query",
			"copy (select 1) to stdout;\n", "copy (select 1) to stdout;\n"},
		{"copy table",
			"copy t (a, b) from stdin with (format csv);\n", "copy t(a, b) from stdin with (format csv);\n"},
		{"declare cursor",
			"declare c cursor for select a, b, c from some_long_table_name where a = 1 and b = 2 and c = 3 order by 1;\n",
			"declare c cursor for\n   select a, b, c\n     from some_long_table_name\n    where a = 1\n      and b = 2\n      and c = 3\n order by 1;\n"},
	}
	for _, c := range cases {
		got := formatOK(t, c.src)
		if got != c.want {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", c.name, got, c.want)
		}
		if again := formatOK(t, got); again != got {
			t.Errorf("%s: not idempotent:\n%s", c.name, again)
		}
	}
}

// ROWS FROM (f(), g()): the FROM belongs to ROWS. Taken for the FROM clause
// it put "rows" alone on the select-side and the function list on a line of
// its own.
func TestRowsFromIsNotAFromClause(t *testing.T) {
	src := "select * from t, rows from (f1()) as r(a);\n"
	if got := formatOK(t, src); got != src {
		t.Errorf("got %q, want %q", got, src)
	}
}

// SEARCH and CYCLE trail a recursive CTE's closing paren; when the pair does
// not fit on that line, CYCLE gets a line of its own.
func TestCTESearchCycleWraps(t *testing.T) {
	src := "with recursive tree(id, parent_id, depth) as (select 1, 2, 3 union all select c.id, c.parent_id, p.depth+1 from c join tree p on p.id = c.parent_id) search depth first by id set the_order_column cycle id set is_cycle using the_path select * from tree;\n"
	got := formatOK(t, src)
	want := "with recursive tree(id, parent_id, depth) as (\n" +
		"  select 1, 2, 3\n" +
		"  union all\n" +
		"  select c.id, c.parent_id, p.depth + 1\n" +
		"    from c\n" +
		"    join tree p on p.id = c.parent_id\n" +
		") search depth first by id set the_order_column\n" +
		"  cycle id set is_cycle using the_path\n" +
		"select * from tree;\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
