package format

import (
	"bytes"
	"strings"
	"testing"
)

// caseFmt formats src and fails the test on error.
func caseFmt(t *testing.T, src string) string {
	t.Helper()
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format(%q) error: %v", src, err)
	}
	return got
}

// TestSimpleCaseKeepsItsBody is a regression test for silent data loss: a
// *simple* CASE (one with an operand between "case" and the first "when")
// that was too long to render inline came out as "case end" -- operand,
// every WHEN/THEN branch, and the ELSE all dropped, with no error and exit
// status 0.
//
// layoutCase's when-loop only advances while it is looking at a "when", so
// for "case <operand> when ..." it never started; i stayed at 1 and every
// token up to "end" was skipped.
func TestSimpleCaseKeepsItsBody(t *testing.T) {
	// Long enough that layoutCase takes the multi-line path rather than
	// returning the flat one-liner.
	src := "select case name when 'AAAAAAAAAAAAAAAAAAAA' then 'first value here' else 'second value here' end as label, count(*) from t group by name;"
	got := caseFmt(t, src)

	if strings.Contains(got, "case end") || strings.Contains(got, "case\n") && !strings.Contains(got, "when") {
		t.Fatalf("CASE body was dropped:\n%s", got)
	}
	for _, want := range []string{"name", "'AAAAAAAAAAAAAAAAAAAA'", "'first value here'", "'second value here'"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q from the CASE expression:\n%s", want, got)
		}
	}
}

// TestSimpleCaseOperandOnCaseLine pins the layout, not just the survival of
// the tokens: the operand belongs on the "case" line, with WHEN/THEN/ELSE
// under it exactly as they are for a searched CASE.
func TestSimpleCaseOperandOnCaseLine(t *testing.T) {
	src := "select case grouping(drivers.driverid) when 1 then '<all drivers>' else drivers.surname end as driver, sum(points) as points from results group by rollup(drivers.driverid);"
	got := caseFmt(t, src)
	want := "case grouping (drivers.driverid)\n"
	if !strings.Contains(got, want) {
		t.Errorf("operand not on the case line (want %q):\n%s", want, got)
	}
	for _, frag := range []string{"when 1", "then '<all drivers>'", "else drivers.surname"} {
		if !strings.Contains(got, frag) {
			t.Errorf("missing %q:\n%s", frag, got)
		}
	}
}

// TestSearchedCaseUnchanged guards the form that already worked.
func TestSearchedCaseUnchanged(t *testing.T) {
	src := "select case when name = 'AAAAAAAAAAAAAAAAAAAA' then 'first value here' else 'second value here' end as label, count(*) from t group by name;"
	got := caseFmt(t, src)
	if !strings.Contains(got, "case\n") {
		t.Errorf("searched CASE should keep a bare \"case\" line:\n%s", got)
	}
	for _, frag := range []string{"when name = 'AAAAAAAAAAAAAAAAAAAA'", "then 'first value here'", "else 'second value here'"} {
		if !strings.Contains(got, frag) {
			t.Errorf("missing %q:\n%s", frag, got)
		}
	}
}

// TestShortCaseStaysInline: the flat path is unaffected by the fix, per
// STYLE.md rule 15.
func TestShortCaseStaysInline(t *testing.T) {
	got := caseFmt(t, "select case a when 1 then 'x' else 'y' end as label from t;")
	if strings.Count(strings.TrimRight(got, "\n"), "\n") != 0 {
		t.Errorf("short CASE should stay on one line:\n%s", got)
	}
}

// TestSimpleCaseIdempotent: the multi-line output must survive a re-format,
// which is what the fix's own layout is then parsed back as.
func TestSimpleCaseIdempotent(t *testing.T) {
	srcs := []string{
		"select case name when 'AAAAAAAAAAAAAAAAAAAA' then 'first value here' else 'second value here' end as label, count(*) from t group by name;",
		"select case grouping(d.id) when 1 then '<all drivers>' else d.surname end as driver from r;",
		"select case when a = 1 then 'AAAAAAAAAAAAAAAAAAAAAAAAAAAA' else 'BBBBBBBBBBBBBBBBBBBBBBBBBB' end from t;",
	}
	for _, src := range srcs {
		once := caseFmt(t, src)
		twice := caseFmt(t, once)
		if once != twice {
			t.Errorf("not idempotent for %q:\nfirst:\n%s\nsecond:\n%s", src, once, twice)
		}
	}
}

// TestNestedSimpleCase: an operand containing parens must not confuse the
// top-level "when" scan.
func TestNestedSimpleCase(t *testing.T) {
	src := "select case coalesce(a, (select max(b) from u)) when 1 then 'first value here' else 'second value here' end as label from t;"
	got := caseFmt(t, src)
	for _, frag := range []string{"coalesce", "select max(b)", "when 1", "else 'second value here'"} {
		if !strings.Contains(got, frag) {
			t.Errorf("missing %q:\n%s", frag, got)
		}
	}
}
