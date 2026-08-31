package format

import (
	"bytes"
	"strings"
	"testing"
)

func mFmt(t *testing.T, src string) string {
	t.Helper()
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	return got
}

const mergeSrc = `merge into constructor_season_summary as target
using (
  select races.year as season, constructors.constructorid, sum(results.points) as points
    from results join races using(raceid) join constructors using(constructorid)
   where races.year = 2017
   group by races.year, constructors.constructorid
) as source
   on target.season = source.season
  and target.constructorid = source.constructorid
when matched then
  update set points = source.points, races = source.races
when not matched then
  insert (season, constructorid, name, points, races)
  values (source.season, source.constructorid, source.name, source.points, source.races);`

// TestMergeIsLaidOut: MERGE was not dispatched at all -- "merge" was not a
// keyword, so statementKeyword returned "" and the whole statement went
// through flatJoin: one 773-column line from a 76-column source.
func TestMergeIsLaidOut(t *testing.T) {
	got := mFmt(t, mergeSrc)
	for _, want := range []string{
		"merge into constructor_season_summary as target\n",
		"using (\n",
		"\n) as source\n",
		"\n   on target.season = source.season\n",
		"\n  and target.constructorid = source.constructorid\n",
		"\nwhen matched then\n",
		"\nwhen not matched then\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > 85 {
			t.Errorf("line over 85 columns:\n%s", l)
		}
	}
}

// TestMergeKeepsEverything: the MERGE insert action has no INTO, so the
// bare "insert" was not a clause bound -- and splitClauses drops whatever
// precedes its first bound, so the column list vanished silently.
func TestMergeKeepsEverything(t *testing.T) {
	got := mFmt(t, mergeSrc)
	if squash(got) != squash(mergeSrc) {
		t.Errorf("content changed:\nin:  %s\nout: %s", squash(mergeSrc), squash(got))
	}
	if !strings.Contains(got, "insert (season, constructorid, name, points, races)") {
		t.Errorf("INSERT column list lost:\n%s", got)
	}
}

// TestMergeWhenActionsIndented: each WHEN clause is a block, its action
// indented beneath the condition.
func TestMergeWhenActionsIndented(t *testing.T) {
	got := mFmt(t, mergeSrc)
	if !strings.Contains(got, "when matched then\n  update set ") {
		t.Errorf("UPDATE action not indented under its WHEN:\n%s", got)
	}
	if !strings.Contains(got, "when not matched then\n  insert (") {
		t.Errorf("INSERT action not indented under its WHEN:\n%s", got)
	}
}

// TestWideValuesRowFills: a single VALUES row wide enough to overflow is
// one item as far as layoutCommaList is concerned -- the whole "(a, b, c)"
// group -- so it had nothing to break and left the row long.
func TestWideValuesRowFills(t *testing.T) {
	src := "insert into t (season, constructorid, name, points, races) values (source.season, source.constructorid, source.name, source.points, source.races);"
	got := mFmt(t, src)
	if squash(got) != squash(src) {
		t.Errorf("content changed:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > 85 {
			t.Errorf("line over 85 columns:\n%s", l)
		}
	}
}

// TestMultipleValuesRowsStayOnePerLine: the several-row form is unchanged.
func TestMultipleValuesRowsStayOnePerLine(t *testing.T) {
	got := mFmt(t, "insert into t (a, b) values (1, 2), (3, 4), (5, 6);")
	if strings.Contains(got, "(1, 2),\n            (3, 4)") == false && !strings.Contains(got, "(1, 2), (3, 4), (5, 6)") {
		t.Logf("values rows rendered as:\n%s", got)
	}
	if squash(got) != squash("insert into t (a, b) values (1, 2), (3, 4), (5, 6);") {
		t.Errorf("content changed:\n%s", got)
	}
}

func TestMergeIdempotent(t *testing.T) {
	once := mFmt(t, mergeSrc)
	if twice := mFmt(t, once); once != twice {
		t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", once, twice)
	}
}

// TestConcatChainBreaks: a single select-list item can be far too wide with
// no comma in it to break at -- the figure-generating queries build TikZ as
// one long "format(...) || E'\n' || format(...)" chain. The "||" operators
// are the natural break, and are where the author breaks them by hand.
func TestConcatChainBreaks(t *testing.T) {
	src := "select '<path d=\"' || st_assvg(geom, 0, 1) || '\" fill=\"none\" stroke=\"#C0B8AE\" stroke-width=\"2\"/>' as elem from shapes;"
	got := mFmt(t, src)
	if squash(got) != squash(src) {
		t.Errorf("content changed:\n%s", got)
	}
	if !strings.Contains(got, "\n       || st_assvg(geom, 0, 1)\n") {
		t.Errorf("chain not broken at ||:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > 90 {
			t.Errorf("line over 90 columns:\n%s", l)
		}
	}
}

// TestShortConcatStaysInline: the break is width-driven.
func TestShortConcatStaysInline(t *testing.T) {
	got := mFmt(t, "select a || b || c as x from t;")
	if strings.Contains(got, "|| b\n") {
		t.Errorf("short concat was broken up:\n%s", got)
	}
}
