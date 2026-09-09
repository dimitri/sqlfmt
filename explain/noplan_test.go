package explain

import (
	"testing"
)

// An EXPLAIN that ran and planned nothing is a plan whose tree is empty,
// not a transcript this package failed to read. PostgreSQL prints these:
// CREATE TABLE / MATERIALIZED VIEW IF NOT EXISTS, when the relation is
// already there, is skipped before anything is planned, but psql has
// begun printing a result set and emits the header anyway.

func TestEmptyPlanParses(t *testing.T) {
	for _, tc := range []struct{ name, out string }{
		{
			"as select_into.out records it",
			" QUERY PLAN \n------------\n(0 rows)\n",
		},
		{
			"with the NOTICE psql printed above it",
			"NOTICE:  relation \"ctas_ine_tbl\" already exists, skipping\n QUERY PLAN \n------------\n(0 rows)\n",
		},
		{
			"unicode border style",
			" QUERY PLAN \n════════════\n(0 rows)\n",
		},
		{
			"trailing blank line",
			" QUERY PLAN \n------------\n(0 rows)\n\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Parse(tc.out)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if p == nil {
				t.Fatal("no plan returned")
			}
			if !p.Empty {
				t.Error("Empty is false")
			}
			if p.Root != nil {
				t.Errorf("Root is %+v, want nil: nothing was planned", p.Root)
			}
		})
	}
}

// Every consumer has to keep working on one without a special case --
// that is the reason this is a plan rather than an error. Each of these
// already falls out of a nil Root, and each is the truthful output for a
// query where nothing happened.
func TestEmptyPlanFlowsThroughConsumers(t *testing.T) {
	p, err := Parse(" QUERY PLAN \n------------\n(0 rows)\n")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("ToJSON emits no plan", func(t *testing.T) {
		b, err := ToJSON(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "[]" {
			t.Errorf("ToJSON = %s, want []", b)
		}
	})

	t.Run("Advice has nothing to say", func(t *testing.T) {
		if got := Advice(p); len(got) != 0 {
			t.Errorf("Advice = %v, want none", got)
		}
	})
}

// The distinction has to stay narrow, or it becomes a way to swallow real
// failures: only a result that says it has zero rows planned nothing.
//
// Prose is not in this list, and that is not an oversight. Parse is
// deliberately permissive about input with no header and no costs --
// pasted plans arrive with all sorts of surrounding mess -- so a line of
// prose comes back as one "unknown" node rather than an error at all.
// That predates this and is unchanged by it.
func TestNotAnEmptyPlan(t *testing.T) {
	for _, tc := range []struct{ name, out string }{
		{"nothing at all", ""},
		{
			// A header claiming rows we then failed to collect is a
			// genuine parse failure: this is the case Empty must never
			// stand in for.
			"header claiming rows, nothing collected",
			" QUERY PLAN \n------------\n(3 rows)\n",
		},
		{"a footer with no header", "(0 rows)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Parse(tc.out)
			if err == nil {
				t.Fatalf("expected an error, got plan %+v", p)
			}
			if p != nil {
				t.Errorf("got a plan back alongside the error: %+v", p)
			}
		})
	}
}

// A plan that does have rows must be untouched by any of this.
func TestRealPlansAreNotEmpty(t *testing.T) {
	out := ` QUERY PLAN 
------------------------------------------------------------
 Seq Scan on foo  (cost=0.00..1.05 rows=5 width=4)
(1 row)
`
	p, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Empty {
		t.Error("a plan with a Seq Scan in it reports Empty")
	}
	if p.Root == nil || p.Root.Type != "seq-scan" {
		t.Fatalf("root = %+v, want a seq-scan", p.Root)
	}
	if p.Root.Relation != "foo" {
		t.Errorf("relation = %q, want foo", p.Root.Relation)
	}
}
