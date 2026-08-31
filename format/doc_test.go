package format

import (
	"bytes"
	"strings"
	"testing"
)

func docFmt(t *testing.T, src string) string {
	t.Helper()
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	return got
}

func longestLine(s string) int {
	m := 0
	for _, l := range strings.Split(s, "\n") {
		if len(l) > m {
			m = len(l)
		}
	}
	return m
}

// TestDocGroupIsAllOrNothing: a group either fits flat or breaks entirely.
func TestDocGroupIsAllOrNothing(t *testing.T) {
	d := group(concat(text("a"), line(), text("b"), line(), text("c")))
	if got := renderDoc(d, 0); len(got) != 1 || got[0] != "a b c" {
		t.Errorf("short group should stay flat, got %q", got)
	}
	wide := strings.Repeat("x", targetWidth)
	d = group(concat(text(wide), line(), text("b")))
	if got := renderDoc(d, 0); len(got) != 2 {
		t.Errorf("over-wide group should break, got %q", got)
	}
}

// TestDocFillPacks: a fill packs its parts, deciding each separator on its
// own, rather than breaking all of them like a group would.
func TestDocFillPacks(t *testing.T) {
	parts := make([]Doc, 30)
	for i := range parts {
		parts[i] = text("item")
	}
	got := renderDoc(fill(concat(text(","), line()), parts...), 0)
	if len(got) < 2 {
		t.Fatalf("expected a break, got %q", got)
	}
	if len(got) >= len(parts) {
		t.Errorf("fill exploded one item per line rather than packing: %q", got)
	}
	for _, l := range got {
		if len(l) > targetWidth {
			t.Errorf("fill produced an over-wide line: %q", l)
		}
	}
}

// TestDocNestIndentsContinuations checks the indent Nest contributes.
func TestDocNestIndentsContinuations(t *testing.T) {
	wide := strings.Repeat("x", targetWidth)
	d := group(concat(text("f("), nest(2, concat(soft(), text(wide), text(","), line(), text("y"))), soft(), text(")")))
	got := renderDoc(d, 0)
	if len(got) < 3 {
		t.Fatalf("expected a broken layout, got %q", got)
	}
	if !strings.HasPrefix(got[1], "  ") {
		t.Errorf("nested line not indented: %q", got[1])
	}
}

// TestCallArgsFillNotExplode is the behaviour the IR exists for: a call too
// wide for its line gets its arguments packed, not one per line.
func TestCallArgsFillNotExplode(t *testing.T) {
	got := docFmt(t, "select st_transscale(st_intersection(r.geom, win.env), -proj.x0, -proj.y0, proj.scale, proj.scale) as geom from r;")
	if longestLine(got) > targetWidth {
		t.Errorf("still over the target:\n%s", got)
	}
	packed := false
	for _, l := range strings.Split(got, "\n") {
		if strings.Count(l, ",") >= 2 {
			packed = true
		}
	}
	if !packed {
		t.Errorf("arguments exploded one per line:\n%s", got)
	}
}

// TestConcatChainOnePerLine: a "||" chain is a Group, not a Fill -- its
// parts are whole clauses of a template and the author writes them one per
// line.
func TestConcatChainOnePerLine(t *testing.T) {
	src := "select '<path d=\"' || st_assvg(geom, 0, 1) || '\" fill=\"none\" stroke=\"#C0B8AE\" stroke-width=\"2\"/>' as elem from shapes;"
	got := docFmt(t, src)
	// The operator hangs left of the operand column and every operand
	// lines up under the first one, as "and"/"or" do under "where".
	if strings.Count(got, "\n    || ") < 2 {
		t.Errorf("chain not broken one part per line:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if op := strings.Index(l, "|| "); op >= 0 {
			if !strings.HasPrefix(strings.TrimLeft(l, " "), "|| ") {
				continue
			}
			if operandCol := op + 3; operandCol != 7 {
				t.Errorf("operand at column %d, want 7 (aligned with the first):\n%s", operandCol, got)
			}
		}
	}
}

// TestCaseInsideCallIsDeferred: a CASE passed as an argument is laid out by
// the clause layer, via docDefer. Before that the Doc layer declined any
// run containing a CASE -- which meant declining the figure-generating
// calls that most needed it.
func TestCaseInsideCallIsDeferred(t *testing.T) {
	src := "select format('%s at (%s,%s)', case when rc.name = 'Czechia' then 'Czechia' when rc.name = 'North Macedonia' then 'N.Mac.' else rc.name end, round(nc.cx::numeric, 3), round(nc.cy::numeric, 3)) as elem from rc;"
	got := docFmt(t, src)
	if squash(got) != squash(src) {
		t.Errorf("content changed:\n%s", got)
	}
	if longestLine(got) > 95 {
		t.Errorf("line still far over the target (%d):\n%s", longestLine(got), got)
	}
}

// TestConcatSplitSkipsCaseSpans is a data-loss regression: a "||" inside a
// CASE branch is not a top-level operator, and splitting there cut the CASE
// in half -- each half then looked like its own CASE and the "end" came out
// twice.
func TestConcatSplitSkipsCaseSpans(t *testing.T) {
	src := "select case when length(t.name) < 55 then t.name else substring(t.name from 1 for 54) || '...' end as track, ar.name as artist from track t join artist ar on ar.id = t.artist_id;"
	got := docFmt(t, src)
	if squash(got) != squash(src) {
		t.Errorf("content changed:\nin:  %s\nout: %s", squash(src), squash(got))
	}
	if strings.Count(strings.ToLower(got), "end") != 1 {
		t.Errorf("the CASE's end was duplicated:\n%s", got)
	}
}

func TestDocLayerIdempotent(t *testing.T) {
	srcs := []string{
		"select st_transscale(st_intersection(r.geom, win.env), -proj.x0, -proj.y0, proj.scale, proj.scale) as geom from r;",
		"select '<path d=\"' || st_assvg(geom, 0, 1) || '\" fill=\"none\"/>' as elem from shapes;",
		"select case when length(t.name) < 55 then t.name else substring(t.name from 1 for 54) || '...' end as track from track t;",
	}
	for _, src := range srcs {
		once := docFmt(t, src)
		if twice := docFmt(t, once); once != twice {
			t.Errorf("not idempotent for %q:\nfirst:\n%s\nsecond:\n%s", src, once, twice)
		}
	}
}

// A group must weigh what follows it on the line, not just itself. The
// call below fits at column 0 on its own, but the alias that trails it
// does not, and only a continuation-aware fits check can see that.
func TestGroupCountsItsContinuation(t *testing.T) {
	call := group(concat(
		text("format("),
		nest(2, concat(soft(), fill(concat(text(","), line()),
			text("'%s %s'"), text("drivers.forename"), text("drivers.surname")))),
		soft(),
		text(")"),
	))
	if got := renderDoc(call, 0); len(got) != 1 {
		t.Fatalf("call alone fits at col 0, want 1 line, got %d: %q", len(got), got)
	}
	d := concat(call, text(` as "Driver's Champion"`))
	got := renderDoc(d, 9)
	for _, l := range got {
		if len(l) > targetWidth {
			t.Errorf("line over margin: %d cols %q", len(l), l)
		}
	}
	if len(got) < 2 {
		t.Fatalf("want the call broken to make room for the alias, got %q", got)
	}
}

// tailOf stops at the first break opportunity: content beyond a break is
// not on this line and must not count against it.
func TestTailStopsAtBreak(t *testing.T) {
	rest := []Doc{text("ab"), line(), text("cdefgh")}
	if got := tailOf(rest, 0); got != 2 {
		t.Errorf("tailOf = %d, want 2 (stop at the line)", got)
	}
	if got := tailOf([]Doc{text("ab"), text("cd")}, 3); got != 7 {
		t.Errorf("tailOf = %d, want 7 (no break, so the outer tail carries)", got)
	}
}
