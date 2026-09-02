package format

import (
	"strings"
	"testing"
)

// Each of these fell to flatJoin: the GRANT came back as one 205-column
// line, the partitioned tables at 95-96 each. The expected shape is the
// corpus's own -- header, then one clause per line at indent 2.
func TestIndentedDDLClauses(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"partition of",
			"create table lab.invoice_2022 partition of lab.invoice_by_year for values from (2022) to (2023);\n",
			[]string{"create table lab.invoice_2022", "  partition of lab.invoice_by_year", "  for values from (2022) to (2023);"}},
		{"create database",
			"create database chinook_prod owner chinook_owner encoding 'UTF8' lc_collate 'en_US.UTF-8' lc_ctype 'en_US.UTF-8' template template0;\n",
			[]string{"create database chinook_prod", "  owner chinook_owner", "  encoding 'UTF8'", "  template template0;"}},
		{"grant",
			"grant select (invoice_id, customer_id, invoice_date, billing_address, billing_city, billing_state, billing_country, billing_postal_code, total) on chinook.invoice to chinook_ro;\n",
			[]string{"grant select (invoice_id, customer_id, invoice_date, billing_address,", "  on chinook.invoice", "  to chinook_ro;"}},
		{"alter default privileges",
			"alter default privileges in schema chinook grant select, insert, update, delete on tables to chinook_rw;\n",
			[]string{"alter default privileges", "  in schema chinook", "  grant select, insert, update, delete", "  on tables"}},
		{"alter subcommand inner clauses",
			"alter table chinook.invoice add constraint invoice_currency_notnull check (currency is not null) not valid;\n",
			[]string{"alter table chinook.invoice", "  add constraint invoice_currency_notnull", "      check (currency is not null)", "      not valid;"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mustFormat(t, c.src)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("want line %q in:\n%s", w, got)
				}
			}
			for _, l := range strings.Split(got, "\n") {
				if cols(l) > targetWidth {
					t.Errorf("line over the margin (%d cols):\n%s", cols(l), got)
				}
			}
		})
	}
}

// A statement that fits is left whole; only the clause words that open a
// continuation are break points, never one that opens the statement.
func TestIndentedDDLKeepsShortStatementsWhole(t *testing.T) {
	for _, src := range []string{
		"grant usage on schema chinook to chinook_ro;\n",
		"create database sandbox;\n",
		"alter table t add column c int;\n",
	} {
		got := mustFormat(t, src)
		if strings.Count(strings.TrimRight(got, "\n"), "\n") != 0 {
			t.Errorf("short statement was broken up:\n%s", got)
		}
	}
}

// The ALTER/GRANT/CREATE DATABASE words are keywords now, so rule 1
// lowercases the whole construct rather than whichever word was already
// in the table: "ATTACH partition", "create DATABASE", "GRANT select".
func TestDDLVocabularyIsLowercased(t *testing.T) {
	got := mustFormat(t,
		"ALTER TABLE t ATTACH PARTITION p FOR VALUES FROM (1) TO (2);\n"+
			"CREATE DATABASE d OWNER o ENCODING 'UTF8';\n"+
			"GRANT SELECT ON t TO r;\n")
	for _, bad := range []string{"ATTACH", "DATABASE", "OWNER", "GRANT", "SELECT", "PARTITION"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q left uppercased:\n%s", bad, got)
		}
	}
}

// A constraint's continuation keywords are right-aligned, not indented,
// so every clause's value starts at the same column: "geoname" lands
// under the "(" of the key it qualifies, not under "isocode".
func TestConstraintClausesAreRiverAligned(t *testing.T) {
	src := "create table geoname.city (id int, isocode text, regcode text, discode text," +
		" foreign key (isocode, regcode, discode) references geoname.district(isocode, regcode, discode));\n"
	got := mustFormat(t, src)
	var keyCol, refCol int
	for _, l := range strings.Split(got, "\n") {
		if i := strings.Index(l, "foreign key ("); i >= 0 {
			keyCol = i + len("foreign key ")
		}
		if i := strings.Index(l, "references "); i >= 0 {
			refCol = i + len("references ")
		}
	}
	if keyCol == 0 || refCol == 0 {
		t.Fatalf("constraint not broken:\n%s", got)
	}
	if keyCol != refCol {
		t.Errorf("values at columns %d and %d, want them aligned:\n%s", keyCol, refCol, got)
	}
}

// PREPARE's payload is a statement, and went through flatJoin -- one
// 144-column line carrying a complete SELECT.
func TestPrepareHandsItsBodyToTheQueryLayout(t *testing.T) {
	src := "prepare foo as select date, shares, trades, dollars from factbook" +
		" where date >= $1::date and date < $1::date + interval '1 month' order by date;\n"
	got := mustFormat(t, src)
	if !strings.HasPrefix(got, "prepare foo as\n") {
		t.Errorf("header not on its own line:\n%s", got)
	}
	// The body is a real query river: every clause keyword ends at one column.
	end := -1
	for _, l := range strings.Split(got, "\n") {
		for _, kw := range []string{"select ", "from ", "where ", "and ", "order by "} {
			if i := strings.Index(l, kw); i >= 0 && strings.TrimLeft(l, " ") == strings.TrimLeft(l[i:], " ") {
				if e := i + len(kw) - 1; end == -1 {
					end = e
				} else if e != end {
					t.Errorf("clause keyword ends at %d, want %d:\n%s", e, end, got)
				}
				break
			}
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if cols(l) > targetWidth {
			t.Errorf("line over the margin (%d):\n%s", cols(l), got)
		}
	}
}

// A typed table still has a column list: "of" between the name and the
// paren belongs to the name. "partition" deliberately does not, so a
// partition definition still falls through to its own clause layout.
func TestTypedTableKeepsItsColumnList(t *testing.T) {
	got := mustFormat(t, "create table rate of rate_t(exclude using gist(currency with =, validity with &&));\n")
	if !strings.Contains(got, "create table rate of rate_t\n") {
		t.Errorf("typed table header not on its own line:\n%s", got)
	}
	if !strings.Contains(got, "  exclude using gist(") {
		t.Errorf("EXCLUDE not laid out as a table constraint:\n%s", got)
	}
	part := mustFormat(t, "create table lab.invoice_2022 partition of lab.invoice_by_year for values from (2022) to (2023);\n")
	if !strings.Contains(part, "  partition of lab.invoice_by_year") {
		t.Errorf("partition definition lost its clause layout:\n%s", part)
	}
}

// An aggregate and its FILTER / WITHIN GROUP suffix are right-aligned
// with each other, so their argument lists start at the same column:
// "count" is pushed one column right to end where "filter" ends, and
// where the function name is the longer of the two the suffix keyword
// moves instead.
func TestAggregateSuffixIsRiverAligned(t *testing.T) {
	src := "select season, count(*) filter(where milliseconds is null and position is null) as dnfs," +
		" percentile_cont(array[0.5, 0.9, 0.95, 0.99]) within group (order by cts - ats) as parr from r;\n"
	got := mustFormat(t, src)
	lines := strings.Split(got, "\n")
	checked := 0
	for i, l := range lines {
		body := strings.TrimLeft(l, " ")
		if !strings.HasPrefix(body, "filter(") && !strings.HasPrefix(body, "within group ") {
			continue
		}
		checked++
		// The open parens are what must line up, not the names: "within
		// group (" carries a space before its paren and "count(" does not.
		aggParen := strings.Index(lines[i-1], "(")
		kwParen := strings.Index(l, "(")
		if aggParen != kwParen {
			t.Errorf("suffix paren at %d, aggregate paren at %d -- not aligned:\n%s",
				kwParen, aggParen, got)
		}
	}
	if checked != 2 {
		t.Fatalf("expected both suffixes on their own line, saw %d:\n%s", checked, got)
	}
	// OVER keeps its own layout: a frame's clauses each need a line.
	over := mustFormat(t, "select avg(res.points) over(order by r.round rows between 2 preceding and current row)::numeric as x from t;\n")
	if !strings.Contains(over, "over(\n") {
		t.Errorf("OVER lost its frame layout:\n%s", over)
	}
}

// MERGE PARTITIONS and SPLIT PARTITION are ALTER TABLE subcommands, but
// neither "merge" nor "split" was in alterSubcommands, so alterHeaderEnd
// returned -1 and layoutAlter fell back to flatStatementLines: the merge
// came out on one 117-column line, the split on a 180-column one. Rule 22
// wants the subcommand at indent 2 and its INTO hung under it at indent 6.
func TestPartitionSubcommandsBreak(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"merge partitions",
			"alter table demo_races merge partitions (demo_races_2015, demo_races_2016, demo_races_2017) into demo_races_2015_2017;\n",
			[]string{
				"alter table demo_races",
				"  merge partitions (demo_races_2015, demo_races_2016, demo_races_2017)",
				"      into demo_races_2015_2017;",
			}},
		{"split partition",
			"alter table demo_races split partition demo_races_2015_2017 into (partition demo_races_2015 for values from (2015) to (2016), partition demo_races_2016 for values from (2016) to (2017));\n",
			[]string{
				"alter table demo_races",
				"  split partition demo_races_2015_2017",
				"      into (partition demo_races_2015 for values from (2015) to (2016),",
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mustFormat(t, c.src)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("want line %q in:\n%s", w, got)
				}
			}
			for _, l := range strings.Split(got, "\n") {
				if cols(l) > targetWidth {
					t.Errorf("line over the margin (%d cols):\n%s", cols(l), got)
				}
			}
			// The clause loss this whole area is prone to: nothing may
			// go missing, and a second pass must not move anything.
			if squash(got) != squash(c.src) {
				t.Errorf("content lost:\nwant %s\ngot  %s", squash(c.src), squash(got))
			}
			if twice := mustFormat(t, got); twice != got {
				t.Errorf("not idempotent:\nonce:\n%s\ntwice:\n%s", got, twice)
			}
		})
	}
}

// "partitions" is plural and was not in the keyword table, so it lexed as
// an identifier and rule 4 closed up the space before its "(".
func TestMergePartitionsKeepsSpaceBeforeParen(t *testing.T) {
	got := mustFormat(t, "alter table t merge partitions (a, b) into c;\n")
	if strings.Contains(got, "partitions(") {
		t.Errorf("space before \"(\" was dropped:\n%s", got)
	}
	if strings.Count(got, "\n") > 1 {
		t.Errorf("a short subcommand was broken up:\n%s", got)
	}
}

// "split" had to become a keyword for SPLIT PARTITION, which would have
// turned every split(...) call into "split (...)" -- the same trap
// left(...) and right(...) already sit in.
func TestSplitStaysAFunctionCall(t *testing.T) {
	for _, src := range []string{
		"select split(a) from t;\n",
		"select split_part(a, ',', 1) from t;\n",
	} {
		got := mustFormat(t, src)
		if got != src {
			t.Errorf("call was reshaped:\nwant %q\ngot  %q", src, got)
		}
	}
}

// PG 19's ON CONFLICT ... DO SELECT is a two-word conflict action like DO
// NOTHING -- "DO SELECT [FOR ...] [WHERE ...]", with no select list, since
// the projection is the INSERT's own (mandatory) RETURNING. "select" was a
// clauseWords entry, so it became a river segment with an empty body and
// pushed RETURNING under it.
func TestOnConflictDoSelectStaysInline(t *testing.T) {
	got := mustFormat(t, "insert into demo_driver_seen (driverid, surname) values (1, 'Hamilton') on conflict (driverid) do select returning driverid, surname, first_seen_at;\n")
	for _, w := range []string{
		"on conflict (driverid) do select\n",
		"  returning driverid, surname, first_seen_at;",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("want %q in:\n%s", w, got)
		}
	}
	if strings.Contains(got, "do\n") {
		t.Errorf("DO SELECT was split across lines:\n%s", got)
	}
	if twice := mustFormat(t, got); twice != got {
		t.Errorf("not idempotent:\nonce:\n%s\ntwice:\n%s", got, twice)
	}
	// DO UPDATE keeps its split: its SET really is a clause with a body.
	upd := mustFormat(t, "insert into t (a, b) values (1, 2) on conflict (a) do update set b = excluded.b returning a, b;\n")
	if !strings.Contains(upd, "do\n     update\n") {
		t.Errorf("DO UPDATE lost its layout:\n%s", upd)
	}
}

// The UPDATE closing a row-locking clause is not the UPDATE statement's
// clause keyword. This predates PG 19 -- the committed corpus had the
// mangled "... messageid for\n      update skip locked" baked into it --
// and DO SELECT's optional FOR UPDATE reaches the same code.
func TestRowLockingClauseIsNotAnUpdateClause(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"select a, b from t where a = 1 for update;\n", " where a = 1 for update;"},
		{"select a, b from t where a = 1 for no key update;\n", " where a = 1 for no key update;"},
		{"insert into t (a) values (1) on conflict (a) do select for update returning a;\n", "do select for update"},
	} {
		got := mustFormat(t, c.src)
		if !strings.Contains(got, c.want) {
			t.Errorf("want %q in:\n%s", c.want, got)
		}
		if strings.Contains(got, "\nupdate") {
			t.Errorf("row-locking UPDATE started a clause:\n%s", got)
		}
	}
	// A real UPDATE statement is untouched.
	if got := mustFormat(t, "update t set a = 1 where b = 2;\n"); !strings.HasPrefix(got, "update t") {
		t.Errorf("UPDATE statement was reshaped:\n%s", got)
	}
}

// SQL/PGQ (PG 19). PostgreSQL spells an edge out of single-character
// tokens -- "'-' '[' ... ']' '-' '>'" -- so the generic expression path
// saw the leading "-" as a binary operator and broke the line at it,
// stranding "-> (n is country)" on the next. A quantifier fared worse:
// "->{1,4}" is four more tokens, and its comma looked like a list
// separator, so it came back as "-> { 1,\n4 }".
func TestGraphPatternIsNeverBroken(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"edge arrow stays whole",
			"select neighbour from graph_table (borders match (c is country where c.name = 'France')-[is borders]->(n is country) columns (n.name as neighbour)) order by neighbour;\n",
			[]string{
				"    from graph_table (borders",
				"             match (c is country where c.name = 'France')",
				"                   -[is borders]->(n is country)",
				"           columns (n.name as neighbour))",
			}},
		{"quantifier keeps its braces closed up",
			"select r from graph_table (borders match (c is country)-[is borders]->{1,4}(n is country) columns (n.name as r)) order by r;\n",
			[]string{"-[is borders]->{1,4}(n is country)"}},
		{"left and any edges",
			"select a from graph_table (g match (a)<-[e is rel]-(b)-[f]-(c) columns (a.n as a));\n",
			[]string{"(a)<-[e is rel]-(b)-[f]-(c)"}},
		{"abbreviated edges",
			"select a from graph_table (g match (a)->(b)<-(c)-(d) columns (a.n as a));\n",
			[]string{"(a)->(b)<-(c)-(d)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mustFormat(t, c.src)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("want %q in:\n%s", w, got)
				}
			}
			if squash(got) != squash(c.src) {
				t.Errorf("content lost:\nwant %s\ngot  %s", squash(c.src), squash(got))
			}
			if twice := mustFormat(t, got); twice != got {
				t.Errorf("not idempotent:\nonce:\n%s\ntwice:\n%s", got, twice)
			}
			for _, l := range strings.Split(got, "\n") {
				if cols(l) > targetWidth {
					t.Errorf("line over the margin (%d cols):\n%s", cols(l), got)
				}
			}
		})
	}
}

// Ordinary spacing still applies inside a vertex or edge body: only the
// pattern's own punctuation is closed up. Passing a nil prevPrev to
// spaceBetween here turned "where a.pop - 1 > 0" into "a.pop -1 > 0",
// since it needs that token to tell a binary minus from a unary one.
func TestGraphElementBodyKeepsNormalSpacing(t *testing.T) {
	got := mustFormat(t, "select a from graph_table (g match (a is c where a.pop - 1 > 0)-[e]->(b) columns (a.n as a));\n")
	if !strings.Contains(got, "where a.pop - 1 > 0") {
		t.Errorf("body spacing was closed up:\n%s", got)
	}
}

// CREATE PROPERTY GRAPH fell through to flatJoin and came back as one
// 280-column line. Rule 22 wants the clauses at indent 2; the element
// table lists then need one definition per line, and a definition too wide
// on its own hangs its SOURCE/DESTINATION/LABEL under it.
func TestPropertyGraphBreaksIntoClauses(t *testing.T) {
	src := "create property graph borders vertex tables (geoname.country key (isocode) label country properties (name, iso)) edge tables (geoname.neighbour key (isocode, neighbour) source key (isocode) references country (isocode) destination key (neighbour) references country (isocode) label borders);\n"
	got := mustFormat(t, src)
	for _, w := range []string{
		"create property graph borders\n",
		"  vertex tables (\n",
		"    geoname.country key (isocode) label country properties (name, iso)\n",
		"  edge tables (\n",
		"    geoname.neighbour key (isocode, neighbour)\n",
		"      source key (isocode) references country(isocode)\n",
		"      destination key (neighbour) references country(isocode)\n",
		"      label borders\n",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("want %q in:\n%s", w, got)
		}
	}
	if squash(got) != squash(src) {
		t.Errorf("content lost:\nwant %s\ngot  %s", squash(src), squash(got))
	}
	if twice := mustFormat(t, got); twice != got {
		t.Errorf("not idempotent:\nonce:\n%s\ntwice:\n%s", got, twice)
	}
	for _, l := range strings.Split(got, "\n") {
		if cols(l) > targetWidth {
			t.Errorf("line over the margin (%d cols):\n%s", cols(l), got)
		}
	}
	// A short one is left whole, and DROP is not reshaped at all.
	short := mustFormat(t, "create property graph g1 vertex tables (v1, v2);\n")
	if strings.Count(short, "\n") > 1 {
		t.Errorf("a short CREATE PROPERTY GRAPH was broken up:\n%s", short)
	}
	drop := mustFormat(t, "drop property graph if exists borders cascade;\n")
	if drop != "drop property graph if exists borders cascade;\n" {
		t.Errorf("DROP was reshaped:\n%s", drop)
	}
}
