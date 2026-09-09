package explain

import (
	"errors"
	"testing"
)

// An EXPLAIN that produced no plan is a real thing PostgreSQL prints, and
// telling it apart from junk is the difference between "that EXPLAIN had
// nothing to report" and "that is not a query plan" -- only the second of
// which means the user pasted the wrong thing.

// The two shapes in PostgreSQL's own regression output, verbatim
// (src/test/regress/expected/select_into.out and matview.out). The IF NOT
// EXISTS check fires before anything is planned, so the statement is
// skipped, but psql is already printing a result set by then and emits
// its header regardless.
func TestErrNoPlanOnEmptyResult(t *testing.T) {
	for _, tc := range []struct{ name, out string }{
		{
			"CREATE TABLE IF NOT EXISTS, relation exists",
			" QUERY PLAN \n------------\n(0 rows)\n",
		},
		{
			"with the NOTICE psql printed above it",
			"NOTICE:  relation \"ctas_ine_tbl\" already exists, skipping\n QUERY PLAN \n------------\n(0 rows)\n",
		},
		{
			"box-drawing borders, as psql draws them with unicode line style",
			" QUERY PLAN \n════════════\n(0 rows)\n",
		},
		{
			"trailing blank line",
			" QUERY PLAN \n------------\n(0 rows)\n\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Parse(tc.out)
			if !errors.Is(err, ErrNoPlan) {
				t.Fatalf("err = %v, want ErrNoPlan", err)
			}
			if p != nil {
				t.Errorf("got a plan back as well as ErrNoPlan: %+v", p)
			}
		})
	}
}

// The distinction has to be narrow, or it becomes a way to swallow real
// failures: only a result that says it has zero rows is empty on purpose.
//
// Prose is not in this list, and that is not an oversight. Parse is
// deliberately permissive about input with no header and no costs --
// pasted plans arrive with all sorts of surrounding mess -- so a line of
// prose comes back as one "unknown" node rather than an error at all.
// That predates ErrNoPlan and is unchanged by it; what matters here is
// that ErrNoPlan never stands in for a failure.
func TestNotErrNoPlan(t *testing.T) {
	for _, tc := range []struct{ name, out string }{
		{"nothing at all", ""},
		{
			// A header claiming rows we then failed to collect is a
			// genuine parse failure and must keep reporting as one --
			// this is the case ErrNoPlan must not absorb.
			"header claiming rows, nothing collected",
			" QUERY PLAN \n------------\n(3 rows)\n",
		},
		{"a footer with no header", "(0 rows)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.out)
			if err == nil {
				t.Fatal("expected an error")
			}
			if errors.Is(err, ErrNoPlan) {
				t.Errorf("reported ErrNoPlan for input that is not an empty EXPLAIN result: %q", tc.out)
			}
		})
	}
}

// A plan that does have rows must be unaffected by any of this.
func TestEmptyDetectionDoesNotTouchRealPlans(t *testing.T) {
	out := ` QUERY PLAN 
------------------------------------------------------------
 Seq Scan on foo  (cost=0.00..1.05 rows=5 width=4)
(1 row)
`
	p, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p == nil || p.Root == nil {
		t.Fatal("no plan")
	}
	if p.Root.Type != "seq-scan" {
		t.Errorf("root type is %q, want seq-scan", p.Root.Type)
	}
	if p.Root.Relation != "foo" {
		t.Errorf("root relation is %q, want foo", p.Root.Relation)
	}
}
