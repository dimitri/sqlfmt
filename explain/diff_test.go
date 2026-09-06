package explain

import (
	"strings"
	"testing"
)

// The headline case the tool exists for: a Seq Scan became an Index Scan
// and the query got much faster. One structural change, reported on its
// own, with the timings kept separate.
func TestDiffReportsScanMethodChange(t *testing.T) {
	before := mustParse(t, ` Seq Scan on drivers  (cost=0.00..30.40 rows=1 width=15) (actual time=0.010..340.100 rows=1 loops=1)
   Filter: (nationality = 'British'::text)
   Rows Removed by Filter: 678
 Planning Time: 0.120 ms
 Execution Time: 340.114 ms
`)
	after := mustParse(t, ` Index Scan using drivers_nationality on drivers  (cost=0.28..8.30 rows=1 width=15) (actual time=0.020..3.900 rows=1 loops=1)
   Index Cond: (nationality = 'British'::text)
 Planning Time: 0.200 ms
 Execution Time: 4.002 ms
`)

	d := DiffPlans(before, after)
	if d.SameStructure() {
		t.Fatal("expected a structural change")
	}
	got := d.String()
	for _, want := range []string{
		"-SEQ_SCAN(drivers)",
		"+INDEX_SCAN(drivers drivers_nationality)",
		"execution time",
		"-98.8%",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diff missing %q:\n%s", want, got)
		}
	}
}

// The other half of the contract: when only the numbers moved, say so and
// do not manufacture a structural difference out of cost jitter.
func TestDiffSameStructureDifferentNumbers(t *testing.T) {
	before := mustParse(t, ` Hash Join  (cost=1.00..2.00 rows=1 width=8) (actual time=0.001..0.002 rows=1 loops=1)
   Hash Cond: (a.id = b.id)
   ->  Seq Scan on alpha a  (cost=0.00..1.00 rows=1 width=4) (actual time=0.001..0.001 rows=1 loops=1)
   ->  Hash  (cost=1.00..1.00 rows=1 width=4) (actual time=0.001..0.001 rows=1 loops=1)
         ->  Seq Scan on beta b  (cost=0.00..1.00 rows=1 width=4) (actual time=0.001..0.001 rows=1 loops=1)
 Execution Time: 10.000 ms
`)
	after := mustParse(t, ` Hash Join  (cost=9999.00..99999.00 rows=500000 width=8) (actual time=812.400..9999.999 rows=500000 loops=1)
   Hash Cond: (a.id = b.id)
   ->  Seq Scan on alpha a  (cost=0.00..44444.00 rows=500000 width=4) (actual time=0.900..500.100 rows=500000 loops=1)
   ->  Hash  (cost=4444.00..4444.00 rows=250000 width=4) (actual time=700.000..700.000 rows=250000 loops=1)
         ->  Seq Scan on beta b  (cost=0.00..4444.00 rows=250000 width=4) (actual time=0.500..300.000 rows=250000 loops=1)
 Execution Time: 20.000 ms
`)

	d := DiffPlans(before, after)
	if !d.SameStructure() {
		t.Fatalf("expected no structural change, got %+v", d.Structural)
	}
	got := d.String()
	if !strings.Contains(got, "structure unchanged") {
		t.Errorf("expected 'structure unchanged':\n%s", got)
	}
	if !strings.Contains(got, "+100.0%") {
		t.Errorf("expected the timing regression to be reported:\n%s", got)
	}
}

// A join method changing is a structural change even when nothing else
// moves — this is the regression that a plain text diff makes hardest to
// see, because the surrounding numbers all shift too.
func TestDiffReportsJoinMethodChange(t *testing.T) {
	before := mustParse(t, ` Hash Join
   Hash Cond: (f.dim_id = d.id)
   ->  Seq Scan on join_fact f
   ->  Hash
         ->  Seq Scan on join_dim d
`)
	after := mustParse(t, ` Nested Loop
   Join Filter: (f.dim_id = d.id)
   ->  Seq Scan on join_fact f
   ->  Materialize
         ->  Seq Scan on join_dim d
`)

	d := DiffPlans(before, after)
	if d.SameStructure() {
		t.Fatal("expected a structural change")
	}
	got := d.String()
	if !strings.Contains(got, "-HASH_JOIN(d)") {
		t.Errorf("missing removed HASH_JOIN:\n%s", got)
	}
	if !strings.Contains(got, "+NESTED_LOOP_MATERIALIZE(d)") {
		t.Errorf("missing added NESTED_LOOP_MATERIALIZE:\n%s", got)
	}
}

// Losing parallelism is a common and easily missed regression.
func TestDiffReportsParallelismLoss(t *testing.T) {
	before := mustParse(t, ` Gather
   Workers Planned: 2
   ->  Parallel Seq Scan on results
`)
	after := mustParse(t, ` Seq Scan on results
`)
	d := DiffPlans(before, after)
	got := d.String()
	if !strings.Contains(got, "-GATHER(results)") || !strings.Contains(got, "+NO_GATHER(results)") {
		t.Errorf("parallelism change not reported:\n%s", got)
	}
}

// EXPLAIN without ANALYZE carries no timings. Reporting a delta against a
// missing value would read as a catastrophic change; there should just be
// no NUMBERS section.
func TestDiffOmitsNumbersWithoutAnalyze(t *testing.T) {
	before := mustParse(t, " Seq Scan on drivers\n")
	after := mustParse(t, " Seq Scan on drivers\n")
	got := DiffPlans(before, after).String()
	if strings.Contains(got, "execution time") {
		t.Errorf("expected no timing lines without ANALYZE:\n%s", got)
	}
}

func mustParse(t *testing.T, s string) *Plan {
	t.Helper()
	p, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The output shape is now a contract, not an implementation detail: it is
// what the book and courses typeset, and what a pager or review tool
// colours. Pin it exactly, header and all.
func TestDiffIsUnifiedDiffFormat(t *testing.T) {
	before := mustParse(t, ` Seq Scan on drivers  (cost=0.00..30.40 rows=1 width=15) (actual time=0.010..18.245 rows=1 loops=1)
 Planning Time: 0.301 ms
 Execution Time: 18.245 ms
`)
	after := mustParse(t, ` Index Scan using geoname_name on drivers  (cost=0.28..8.30 rows=1 width=15) (actual time=0.020..0.049 rows=1 loops=1)
 Planning Time: 0.306 ms
 Execution Time: 0.049 ms
`)
	d := DiffPlans(before, after)
	d.BeforeName = "4-4b-non-sargable-function"
	d.AfterName = "4-4c-sargable-rewrite"

	want := "--- 4-4b-non-sargable-function\n" +
		"+++ 4-4c-sargable-rewrite\n" +
		"@@ plan structure @@\n" +
		"-SEQ_SCAN(drivers)\n" +
		"+INDEX_SCAN(drivers geoname_name)\n" +
		"\n" +
		" execution time     18.245 ms ->     0.049 ms   (-99.7%)\n" +
		" planning time       0.301 ms ->     0.306 ms   (+1.7%)\n"
	if got := d.String(); got != want {
		t.Fatalf("unified diff mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// Unnamed sides still produce a well-formed header rather than "--- ".
func TestDiffHeaderFallsBackWhenUnnamed(t *testing.T) {
	p := mustParse(t, " Seq Scan on drivers\n")
	got := DiffPlans(p, p).String()
	if !strings.HasPrefix(got, "--- before\n+++ after\n") {
		t.Fatalf("expected fallback header, got:\n%s", got)
	}
}
