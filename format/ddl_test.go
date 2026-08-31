package format

import (
	"bytes"
	"strings"
	"testing"
)

func ddlFmt(t *testing.T, src string) string {
	t.Helper()
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format(%q): %v", src, err)
	}
	return got
}

// TestDDLHeadersAreLaidOut: everything except CREATE TABLE used to fall
// through to flatJoin, putting a whole DDL header on one line -- 138
// columns for a hand-written four-line CREATE FUNCTION.
func TestDDLHeadersAreLaidOut(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{
			"create function",
			"create or replace function twcache.tg_notify_counters() returns trigger language plpgsql as $$ begin end; $$;",
			[]string{"create or replace function twcache.tg_notify_counters()\n", " returns trigger\n", "language plpgsql\n"},
		},
		{
			"create index",
			"create index if not exists geoname_class_p_isocode on geoname.geoname (isocode) where class = 'P';",
			[]string{"create index if not exists geoname_class_p_isocode\n", "   on geoname.geoname(isocode)\n", "where class = 'P';"},
		},
		{
			"create trigger",
			"CREATE TRIGGER update_counters AFTER INSERT ON tweet.activity FOR EACH ROW EXECUTE PROCEDURE twcache.tg_update();",
			[]string{"create trigger update_counters\n", "  after insert\n", "     on tweet.activity\n", "    for each row\n"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ddlFmt(t, tc.src)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q:\n%s", w, got)
				}
			}
			for _, l := range strings.Split(got, "\n") {
				if len(l) > 90 {
					t.Errorf("line over 90 columns:\n%s", l)
				}
			}
		})
	}
}

// TestShortDDLStaysInline: the break is width-driven, per rule 17.
func TestShortDDLStaysInline(t *testing.T) {
	got := ddlFmt(t, "create index i on t (a);")
	if strings.Count(strings.TrimRight(got, "\n"), "\n") != 0 {
		t.Errorf("short DDL should stay on one line:\n%s", got)
	}
}

// TestLanguageHoistedIntoHeader: PostgreSQL accepts a function's options in
// any order, and "as $$ ... $$ language plpgsql" buries the language behind
// the whole body.
func TestLanguageHoistedIntoHeader(t *testing.T) {
	got := ddlFmt(t, "create function f() returns trigger as $$ declare x int; begin return new; end; $$ language plpgsql;")
	langAt := strings.Index(got, "language plpgsql")
	asAt := strings.Index(got, "as $$")
	if langAt == -1 || asAt == -1 {
		t.Fatalf("clauses missing:\n%s", got)
	}
	if langAt > asAt {
		t.Errorf("language still after the body:\n%s", got)
	}
}

// TestLanguageSQLBodyIsFormatted: the body of a LANGUAGE SQL function is
// SQL, so it gets the same layout as any other statement.
func TestLanguageSQLBodyIsFormatted(t *testing.T) {
	got := ddlFmt(t, "create function top_albums(x int) returns setof record language sql as $$ select a.title, count(*) as n from album a where a.artist_id=x group by a.title order by n desc limit 5; $$;")
	for _, w := range []string{"  select a.title, count(*) as n\n", "    from album a\n", "group by a.title\n"} {
		if !strings.Contains(got, w) {
			t.Errorf("body not formatted (missing %q):\n%s", w, got)
		}
	}
}

func TestDDLIdempotent(t *testing.T) {
	srcs := []string{
		"create or replace function f() returns trigger language plpgsql as $$ begin end; $$;",
		"create index if not exists i on t (a) where b = 'x';",
		"CREATE TRIGGER tr AFTER INSERT ON t FOR EACH ROW EXECUTE PROCEDURE f();",
		"create function g(x int) returns int language sql as $$ select x + 1; $$;",
		"create function h() returns trigger as $$ begin end; $$ language plpgsql;",
	}
	for _, src := range srcs {
		once := ddlFmt(t, src)
		if twice := ddlFmt(t, once); once != twice {
			t.Errorf("not idempotent for %q:\nfirst:\n%s\nsecond:\n%s", src, once, twice)
		}
	}
}

// TestRowAssignmentBreaksAtTheOperator: "set (a, ...) = (x, ...)" and the
// row comparison "(a, ...) <> (x, ...)" both run to a couple of hundred
// columns with no useful break point inside either list. The break has to
// be at the operator, which is a clause-level decision renderRun cannot
// make from inside the expression -- 246 columns before this.
func TestRowAssignmentBreaksAtTheOperator(t *testing.T) {
	src := "update moma.artist set (name, bio, nationality, gender, begin, \"end\", wiki_qid, ulan) = (batch.name, batch.bio, batch.nationality, batch.gender, batch.begin, batch.\"end\", batch.wiki_qid, batch.ulan) from batch where batch.constituentid = artist.constituentid;"
	got := ddlFmt(t, src)
	if !strings.Contains(got, "\n  = (batch.name") && !strings.Contains(got, "= (batch.name") {
		t.Errorf("no break at the operator:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > 90 {
			t.Errorf("line over 90 columns:\n%s", l)
		}
	}
}

// TestFillKeepsListsCompact: a wrapped list is filled, not exploded one
// item per line -- it should still read as a list.
func TestFillKeepsListsCompact(t *testing.T) {
	src := "update t set (alpha, bravo, charlie, delta, echo, foxtrot, golf, hotel) = (one, two, three, four, five, six, seven, eight) where id = 1;"
	got := ddlFmt(t, src)
	for _, l := range strings.Split(got, "\n") {
		if strings.Count(l, ",") == 1 && strings.Contains(l, "one") {
			t.Errorf("list exploded one item per line:\n%s", got)
		}
	}
}

// TestFunctionCallArgsFillWhenTooWide: a call that overflows the target has
// its arguments filled -- packed onto as few lines as fit, not exploded one
// per line. Three greedy attempts at this each improved some files and
// regressed others; the Doc layer decides the whole group at once instead.
func TestFunctionCallArgsFillWhenTooWide(t *testing.T) {
	got := ddlFmt(t, "select st_transscale(st_intersection(r.geom, win.env), -proj.x0, -proj.y0, proj.scale, proj.scale) as geom from r;")
	for _, l := range strings.Split(got, "\n") {
		if len(l) > 80 {
			t.Errorf("line over the target:\n%s", got)
		}
	}
	// Filled, not one per line: at least one line carries several args.
	packed := false
	for _, l := range strings.Split(got, "\n") {
		if strings.Count(l, ",") >= 2 {
			packed = true
		}
	}
	if !packed {
		t.Errorf("arguments were exploded one per line rather than filled:\n%s", got)
	}
}

// TestShortCallStaysInline: a call that fits is untouched.
func TestShortCallStaysInline(t *testing.T) {
	got := ddlFmt(t, "select coalesce(a, b, c) as x from t;")
	if strings.Contains(got, "coalesce(\n") {
		t.Errorf("short call was wrapped:\n%s", got)
	}
}

// TestTaggedDollarQuoteBody: dollar-quote matching uses the lexer's own
// rules, so a $tag$ body containing a "$" is split correctly.
func TestTaggedDollarQuoteBody(t *testing.T) {
	got := ddlFmt(t, "create function f(x int) returns int language sql as $body$ select x+1 from t where y = '$notatag$'; $body$;")
	if !strings.Contains(got, "'$notatag$'") {
		t.Errorf("body content lost:\n%s", got)
	}
	if !strings.Contains(got, "as $body$\n") {
		t.Errorf("tagged delimiter not preserved:\n%s", got)
	}
}

// TestMultiAssignmentSetDoesNotCascade: a SET with several assignments is a
// comma list. renderRun walked it inline instead, so the first assignment
// that had to wrap -- a CASE, typically -- wrapped from wherever it
// happened to start, and every assignment after it began deeper still. Four
// CASE expressions turned into a 130-column staircase.
func TestMultiAssignmentSetDoesNotCascade(t *testing.T) {
	src := "update twcache.daily_counters set rts = case when NEW.action = 'rt' then rts + 1 else rts end, de_rts = case when NEW.action = 'de-rt' then de_rts + 1 else de_rts end, favs = case when NEW.action = 'fav' then favs + 1 else favs end where daily_counters.day = current_date;"
	got := ddlFmt(t, src)

	// Every assignment starts at the same column.
	var cols []int
	for _, l := range strings.Split(got, "\n") {
		for _, name := range []string{"rts = case", "de_rts = case", "favs = case"} {
			if idx := strings.Index(l, name); idx >= 0 && strings.HasPrefix(strings.TrimSpace(l), name) {
				cols = append(cols, idx)
			}
		}
	}
	for i := 1; i < len(cols); i++ {
		if cols[i] != cols[0] {
			t.Errorf("assignments do not share a column (%v):\n%s", cols, got)
			break
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > 90 {
			t.Errorf("line over 90 columns:\n%s", l)
		}
	}
}

// TestSingleAssignmentSetUnchanged: the one-assignment form still renders
// inline, and the row form still breaks at its operator.
func TestSingleAssignmentSetUnchanged(t *testing.T) {
	got := ddlFmt(t, "update t set a = 1 where id = 2;")
	if !strings.Contains(got, "set a = 1") {
		t.Errorf("single assignment changed shape:\n%s", got)
	}
}

// TestCreateViewKeepsItsQuery: a view is its query. layoutDDL scanned the
// payload for its own clause words, so a view's SELECT was chopped up at
// its own FROM/AS/ON and most of it dropped -- 1344 characters of SQL in,
// 355 out, on the corpus file that found this.
func TestCreateViewKeepsItsQuery(t *testing.T) {
	src := `create or replace view v as
with recursive deps as (
  select a.id as obj_id, b.name as obj_name from t a join u b on b.id = a.id where a.k in ('v', 'm')
  union all
  select p.id, p.name from deps p join t x on x.id = p.id where x.k in ('v', 'm')
)
select depth, obj_name as dependent from deps group by depth, obj_name;`
	got := ddlFmt(t, src)
	if squash(got) != squash(src) {
		t.Errorf("content changed:\nin:  %s\nout: %s", squash(src), squash(got))
	}
	if !strings.Contains(got, "create or replace view v as\n") {
		t.Errorf("view header not on its own line:\n%s", got)
	}
	if !strings.Contains(got, "\nwith recursive deps as (") {
		t.Errorf("payload not formatted as a query:\n%s", got)
	}
}

// TestCreateViewWithFromInPayload is the narrow case: FROM is a DDL clause
// word too, and a view's payload is full of them.
func TestCreateViewWithFromInPayload(t *testing.T) {
	src := "create view v as select a, b from t where c = 1 order by a;"
	got := ddlFmt(t, src)
	if squash(got) != squash(src) {
		t.Errorf("content changed:\n%s", got)
	}
}

// TestTrailingLanguageStillFound: scanning has to resume after a
// dollar-quoted body -- that is where a trailing LANGUAGE clause lives --
// while still stopping at a view's query payload.
func TestTrailingLanguageStillFound(t *testing.T) {
	// Long enough to break across lines: a DDL statement that fits on one
	// stays there per rule 17, and there is then nothing to reorder.
	got := ddlFmt(t, "create or replace function chinook.album_duration(album_id bigint) returns interval as $$ select sum(milliseconds) * interval '1ms' from track where track.album_id = album_duration.album_id; $$ language sql;")
	langAt, asAt := strings.Index(got, "language sql"), strings.Index(got, "as $$")
	if langAt == -1 || asAt == -1 || langAt > asAt {
		t.Errorf("trailing LANGUAGE not hoisted:\n%s", got)
	}
}

// TestInsertColumnListFills: an INSERT column list is not a function call's
// argument list, but it looks exactly like one -- it follows an identifier
// directly -- so renderRun's paren handling declined to break it per rule 4
// and a wide list ran off the page. Only the clause knows better.
func TestInsertColumnListFills(t *testing.T) {
	src := "insert into twcache.daily_counters (day, rts, de_rts, favs, de_favs, mentions, replies, quotes) values (1,2,3,4,5,6,7,8);"
	got := ddlFmt(t, src)
	if squash(got) != squash(src) {
		t.Errorf("content changed:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > 90 {
			t.Errorf("line over 90 columns:\n%s", l)
		}
	}
	if !strings.Contains(got, "de_favs, mentions,\n") && !strings.Contains(got, "mentions,\n") {
		t.Errorf("column list did not fill across lines:\n%s", got)
	}
}

// TestShortInsertUnchanged: the break is width-driven.
func TestShortInsertUnchanged(t *testing.T) {
	got := ddlFmt(t, "insert into t (a, b) values (1, 2);")
	if strings.Count(strings.TrimRight(got, "\n"), "\n") > 1 {
		t.Errorf("short INSERT should not be broken up:\n%s", got)
	}
}

// TestInsertWithoutColumnList: nothing to fill, and no crash looking.
func TestInsertWithoutColumnList(t *testing.T) {
	got := ddlFmt(t, "insert into t select a, b from u;")
	if squash(got) != squash("insert into t select a, b from u;") {
		t.Errorf("content changed:\n%s", got)
	}
}

// TestDDLClauseListFills: an index's column list and a statistics kind list
// are call-shaped -- "using btree(a, b, ...)" -- so renderRun leaves them
// alone per rule 4 and a wide one overflows. Only the clause knows it is a
// list.
func TestDDLClauseListFills(t *testing.T) {
	src := "create index concurrently if not exists chinook_invoice_line_covering_idx on chinook.invoice_line using btree (invoice_id, track_id, unit_price, quantity, invoice_line_id, created_at, updated_at);"
	got := ddlFmt(t, src)
	if squash(got) != squash(src) {
		t.Errorf("content changed:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > 85 {
			t.Errorf("line over 85 columns:\n%s", l)
		}
	}
	if !strings.Contains(got, "using btree(") {
		t.Errorf("index method lost:\n%s", got)
	}
}

// TestDDLBareCommaListFills covers the other shape: "on col, col, col" with
// no parens at all.
func TestDDLBareCommaListFills(t *testing.T) {
	src := "create statistics if not exists geoname_multi_column_stats (dependencies, ndistinct, mcv) on isocode, class, feature, population, name, latitude, longitude from geoname.geoname;"
	got := ddlFmt(t, src)
	if squash(got) != squash(src) {
		t.Errorf("content changed:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > 85 {
			t.Errorf("line over 85 columns:\n%s", l)
		}
	}
}

// TestLateralSubqueryNotFlattened: layoutFrom rendered a JOIN's table
// expression with flatJoin, which joins renderRun's output with spaces --
// so a LATERAL subquery, which renderRun lays out across several lines, was
// folded back onto the JOIN line and ran to hundreds of columns.
func TestLateralSubqueryNotFlattened(t *testing.T) {
	src := `select d.surname as driver, top3.race
  from f1db.drivers d
  join lateral (
      select r.name as race, res.points
        from f1db.results res
        join f1db.races r on r.raceid = res.raceid
       where res.driverid = d.driverid and r.year = 2017
       order by res.points desc
       limit 3
  ) top3 on true
 where d.surname in ('Hamilton', 'Vettel')
 order by d.surname;`
	got := ddlFmt(t, src)
	if squash(got) != squash(src) {
		t.Errorf("content changed:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > 90 {
			t.Errorf("line over 90 columns:\n%s", l)
		}
	}
	if !strings.Contains(got, "join lateral (\n") {
		t.Errorf("lateral subquery was flattened onto the JOIN line:\n%s", got)
	}
}
