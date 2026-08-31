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

// TestNonSQLBodiesUntouched: a plpgsql body is opaque to this formatter and
// must be passed through exactly as written.
func TestNonSQLBodiesUntouched(t *testing.T) {
	body := "\ndeclare\n  channel text := TG_ARGV[0];\nbegin\n  PERFORM 1;\n  return NEW;\nend;\n"
	got := ddlFmt(t, "create function f() returns trigger language plpgsql as $$"+body+"$$;")
	if !strings.Contains(got, body) {
		t.Errorf("plpgsql body was altered:\n%s", got)
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

// TestFunctionCallArgsNotWrapped: rule 4's no-space-before-"(" marks a
// function call, and the corpus keeps long calls on one line -- breaking
// their arguments reads worse than the overflow.
func TestFunctionCallArgsNotWrapped(t *testing.T) {
	got := ddlFmt(t, "select st_transscale(st_intersection(r.geom, win.env), -proj.x0, -proj.y0, proj.scale, proj.scale) as geom from r;")
	if strings.Contains(got, "st_transscale(\n") {
		t.Errorf("function call arguments were wrapped:\n%s", got)
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
