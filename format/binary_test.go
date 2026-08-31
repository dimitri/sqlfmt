package format

import (
	"strings"
	"testing"
)

// operandCol returns the column each operand of a hanging-operator line
// starts at, for lines whose first non-space token is the operator.
func operandCol(line, op string) (int, bool) {
	t := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(t, op+" ") {
		return 0, false
	}
	return len(line) - len(t) + len(op) + 1, true
}

// A broken chain puts the operator in the gutter to the left and lines
// every operand up under the first one, the way the river sets "and"
// under "where". Putting the operator at the operand column instead
// pushes each continuation operand right by the operator's width.
func TestBinaryChainHangsOperatorLeft(t *testing.T) {
	src := "select a_long_column_name || '--------------------------' || b_long_column || '----' as x from t;\n"
	got := mustFormat(t, src)
	first := -1
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "select ") {
			first = len("select ")
			continue
		}
		col, ok := operandCol(l, "||")
		if !ok {
			continue
		}
		if col != first {
			t.Errorf("operand at column %d, want %d:\n%s", col, first, got)
		}
		if opCol := col - 3; opCol >= first {
			t.Errorf("operator at column %d is not left of the operands:\n%s", opCol, got)
		}
	}
	if strings.Count(got, "|| ") != 3 {
		t.Errorf("chain not broken at every ||:\n%s", got)
	}
}

// The chain breaks at its loosest joint: "a - b * c" is a subtraction of
// a product, so it breaks at "-".
func TestBinarySplitTakesLowestPrecedence(t *testing.T) {
	toks, err := Lex([]byte("aaa - bbb * ccc"))
	if err != nil {
		t.Fatal(err)
	}
	toks, _ = attachComments(toks)
	parts, ops := splitTopLevelBinary(trimTokens(toks[:len(toks)-1]), binaryLevels)
	if len(parts) != 2 || len(ops) != 1 || ops[0] != "-" {
		t.Fatalf("split at %v into %d parts, want one split at -", ops, len(parts))
	}
}

// A sign is not an operator: the "-" in "(-1)" and in "x = -1" follows a
// token that cannot end an operand.
func TestBinarySplitIgnoresUnarySign(t *testing.T) {
	for _, src := range []string{"a, -1", "a = -1"} {
		toks, err := Lex([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		toks, _ = attachComments(toks)
		if parts, _ := splitTopLevelBinary(trimTokens(toks[:len(toks)-1]), binaryLevels); parts != nil {
			t.Errorf("%q split at a unary sign into %d parts", src, len(parts))
		}
	}
}

// A comparison is a break point in a condition but not in a value: an
// UPDATE's "set de_favs = case ... end" is an assignment, and hanging the
// "=" gives the column a line of its own, which is not what it means.
func TestComparisonSplitsOnlyInPredicates(t *testing.T) {
	toks, _ := Lex([]byte("de_favs = case when x then 1 else 2 end"))
	toks, _ = attachComments(toks)
	body := trimTokens(toks[:len(toks)-1])
	if parts, _ := splitTopLevelBinary(body, binaryLevels); parts != nil {
		t.Errorf("value expression split at =: %d parts", len(parts))
	}
	if parts, _ := splitTopLevelBinary(body, predicateLevels); len(parts) != 2 {
		t.Errorf("predicate did not split at =: %d parts", len(parts))
	}
}

// A WHERE whose single predicate is too wide breaks at its comparison,
// with the operator in the gutter and both operands aligned.
func TestWherePredicateBreaksAtComparison(t *testing.T) {
	src := "select 1 from races, decades" +
		" where extract('year' from date_trunc('decade', races.race_date)) = decades.decade_start;\n"
	got := mustFormat(t, src)
	var body, cont int
	for _, l := range strings.Split(got, "\n") {
		if i := strings.Index(l, "where "); i >= 0 {
			body = i + len("where ")
		}
		if c, ok := operandCol(l, "="); ok {
			cont = c
		}
	}
	if cont == 0 {
		t.Fatalf("predicate not broken at the comparison:\n%s", got)
	}
	if cont != body {
		t.Errorf("right operand at column %d, left at %d -- not aligned:\n%s", cont, body, got)
	}
}

// A trailing cast leaves the run ending in a type name rather than ")",
// which hid the call it wraps from the paren layout.
func TestTrailingCastIsPeeled(t *testing.T) {
	toks, _ := Lex([]byte("extract('year' from d)::int"))
	toks, _ = attachComments(toks)
	expr, cast := splitTrailingCast(trimTokens(toks[:len(toks)-1]))
	if cast != "::int" {
		t.Fatalf("cast = %q, want ::int", cast)
	}
	if last := expr[len(expr)-1].Text; last != ")" {
		t.Errorf("expression ends in %q, want )", last)
	}
}

// ALTER TABLE is a comma list of subcommands, and produced the corpus's
// worst line -- a 236-column ALTER with three "alter ... type ... using".
func TestAlterTableBreaksIntoSubcommands(t *testing.T) {
	src := "alter table factbook alter shares type bigint using replace(shares, ',', '')::bigint," +
		" alter trades type bigint using replace(trades, ',', '')::bigint;\n"
	got := mustFormat(t, src)
	if !strings.HasPrefix(got, "alter table factbook\n") {
		t.Errorf("header not on its own line:\n%s", got)
	}
	if strings.Count(got, "\n  alter ") != 2 {
		t.Errorf("subcommands not one per line at indent 2:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if cols(l) > targetWidth {
			t.Errorf("line still over the margin (%d):\n%s", cols(l), got)
		}
	}
	// A short one is left whole.
	short := mustFormat(t, "alter table t add column c int;\n")
	if strings.Count(short, "\n") > 1 {
		t.Errorf("short ALTER was broken up:\n%s", short)
	}
}

// cols counts display columns, not bytes: a box-drawing rule well inside
// the margin was being wrapped as if it were far past it.
func TestColsCountsColumnsNotBytes(t *testing.T) {
	rule := strings.Repeat("─", 60)
	if cols(rule) != 60 {
		t.Errorf("cols = %d, want 60", cols(rule))
	}
	if len(rule) != 180 {
		t.Fatalf("test premise wrong: %d bytes", len(rule))
	}
	got := mustFormat(t, "-- "+rule+"\nselect 1;\n")
	if !strings.Contains(got, rule) {
		t.Errorf("rule was re-wrapped:\n%s", got)
	}
}

// A call with a single wide argument has to be able to hang it, or it has
// nowhere to break at all.
func TestSoleArgumentHangs(t *testing.T) {
	src := "select jsonb_strip_nulls(jsonb_build_object('cmc', data ->> 'cmc', 'power', data -> 'power', 'toughness', data -> 'toughness')) as stats from cards;\n"
	got := mustFormat(t, src)
	if !strings.Contains(got, "jsonb_strip_nulls(\n") {
		t.Errorf("sole argument did not hang:\n%s", got)
	}
}

// The three shapes that must NOT hang: a clause paren has a good one-line
// form of its own, and a grouping paren has no callee, so hanging it
// strands "(" on a line above a stray ")".
func TestSoleArgumentDoesNotHangClauseOrGroupParens(t *testing.T) {
	cases := []struct{ name, src string }{
		{"within group", "select percentile_cont(array[0.5, 0.99]) within group (order by count) as pct from counts;\n"},
		{"grouping paren", "select '<a>' || (-(((pos)[1] - proj.y0) * proj.scale) + (case when rn % 2 = 0 then 9 else -4 end))::text from p;\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mustFormat(t, c.src)
			for _, l := range strings.Split(got, "\n") {
				if strings.HasSuffix(strings.TrimRight(l, " "), "(") {
					t.Errorf("paren left hanging on its own line:\n%s", got)
				}
			}
		})
	}
}

// An OVER that fits stays on one line: the sole-argument path must not
// reach for it just because the select list around it had to break.
// When it does not fit, splitTrailingOver hands it to layoutOver, whose
// own style aligns the frame under the paren -- that is a different path
// and is covered by TestWideOverIsDeferredToLayoutOver.
func TestFittingOverStaysOnOneLine(t *testing.T) {
	got := mustFormat(t, "select a_column, rank() over(partition by c order by p) as r from t;\n")
	if !strings.Contains(got, "rank() over(partition by c order by p) as r") {
		t.Errorf("a fitting OVER was broken up:\n%s", got)
	}
}

// "avg(x) over(...)" does not start with OVER, so deferredConstruct never
// fired, and the paren logic rightly refuses to hang a clause paren --
// which left the whole thing one unbreakable atom at 100 columns.
func TestWideOverIsDeferredToLayoutOver(t *testing.T) {
	src := "select avg(res.points) over(order by r.round rows between 2 preceding and current row)::numeric as running from results res;\n"
	got := mustFormat(t, src)
	if !strings.Contains(got, "avg(res.points) over(\n") {
		t.Errorf("wide OVER not deferred:\n%s", got)
	}
	if !strings.Contains(got, ")::numeric as running") {
		t.Errorf("cast and alias lost their place:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if cols(l) > targetWidth {
			t.Errorf("line still over the margin (%d):\n%s", cols(l), got)
		}
	}
}

// A fill packs items onto as few lines as fit, but once an item has
// broken across lines it ends on a line of its own; packing the next item
// after it runs two arguments together so they read as one.
func TestFillBreaksAfterABrokenItem(t *testing.T) {
	src := "select format('%s / %s', row_number() over(partition by constructorid order by position nulls last), count(*) over(partition by constructorid)) as p from t;\n"
	got := mustFormat(t, src)
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, ")") && strings.Contains(l, ", count(*)") {
			t.Errorf("next argument packed onto a broken item's last line:\n%s", got)
		}
	}
}

// "filter" and "within" are keywords, so rule 1 lowercases both words of
// each construct rather than only the one that happened to be in the
// table -- "WITHIN GROUP" used to come out as "WITHIN group".
func TestAggregateSuffixKeywordsAreLowercased(t *testing.T) {
	got := mustFormat(t, "select percentile_cont(0.5) WITHIN GROUP (ORDER BY x), count(*) FILTER (WHERE y > 0) from t;\n")
	for _, want := range []string{"within group (order by x)", "filter(where y > 0)"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in:\n%s", want, got)
		}
	}
}
