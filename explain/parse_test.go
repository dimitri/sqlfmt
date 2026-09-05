package explain

import (
	"strings"
	"testing"
)

const simplePlan = `                                    QUERY PLAN
═══════════════════════════════════════════════════════════════════════════════════
 WindowAgg  (cost=748.44..748.96 rows=23 width=47)
   ->  Sort  (cost=748.44..748.50 rows=23 width=23)
         Sort Key: results.milliseconds
         ->  Hash Join  (cost=40.90..747.92 rows=23 width=23)
               Hash Cond: (results.driverid = drivers.driverid)
               ->  Seq Scan on results  (cost=0.00..706.96 rows=23 width=24)
                     Filter: (raceid = 890)
               ->  Hash  (cost=30.40..30.40 rows=840 width=15)
                     ->  Seq Scan on drivers  (cost=0.00..30.40 rows=840 width=15)
(9 rows)
`

const analyzePlan = ` Limit  (cost=2983.51..2983.56 rows=20 width=31) (actual time=6.723..6.725 rows=20 loops=1)
   Buffers: shared hit=433
   ->  Sort  (cost=2983.51..2985.61 rows=840 width=31) (actual time=6.722..6.723 rows=20 loops=1)
         Sort Key: (sum(res.points)) DESC
         Sort Method: top-N heapsort  Memory: 26kB
         Buffers: shared hit=433
         ->  GroupAggregate  (cost=2362.14..2961.16 rows=840 width=31) (actual time=3.354..6.651 rows=840 loops=1)
               Group Key: d.driverid
               Buffers: shared hit=430
               ->  Merge Join  (cost=2362.14..2775.78 rows=23597 width=23) (actual time=3.311..5.516 rows=23597 loops=1)
                     Merge Cond: (d.driverid = res.driverid)
                     Buffers: shared hit=430
                     ->  Index Scan using idx_49514_primary on drivers d  (cost=0.28..57.88 rows=840 width=15) (actual time=0.004..0.064 rows=840 loops=1)
                           Buffers: shared hit=18
                     ->  Sort  (cost=2361.86..2420.85 rows=23597 width=16) (actual time=3.303..4.007 rows=23597 loops=1)
                           Sort Key: res.driverid
                           Sort Method: quicksort  Memory: 1690kB
                           Buffers: shared hit=412
                           ->  Seq Scan on results res  (cost=0.00..647.97 rows=23597 width=16) (actual time=0.002..1.590 rows=23597 loops=1)
                                 Buffers: shared hit=412
 Planning Time: 0.508 ms
 Execution Time: 6.819 ms
(24 rows)
`

func TestParseSimplePlan(t *testing.T) {
	plan, err := Parse(simplePlan)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Root.Type != "window-agg" {
		t.Fatalf("root type = %q, want window-agg", plan.Root.Type)
	}
	if len(plan.Root.Children) != 1 || plan.Root.Children[0].Type != "sort" {
		t.Fatalf("expected a single Sort child, got %+v", plan.Root.Children)
	}
	hj := plan.Root.Children[0].Children[0]
	if hj.Type != "hash-join" || len(hj.Children) != 2 {
		t.Fatalf("expected Hash Join with 2 children, got type=%q children=%d", hj.Type, len(hj.Children))
	}
	seqScan := hj.Children[0]
	if seqScan.Type != "seq-scan" || seqScan.Relation != "results" {
		t.Fatalf("expected Seq Scan on results, got type=%q relation=%q", seqScan.Type, seqScan.Relation)
	}
	if len(seqScan.Props) != 1 || seqScan.Props[0] != "Filter: (raceid = 890)" {
		t.Fatalf("unexpected props: %+v", seqScan.Props)
	}
	if plan.PlanningTime != nil || plan.Root.TimeActual != nil {
		t.Fatal("plain EXPLAIN (no ANALYZE) should have no timing data")
	}
}

func TestParseAnalyzePlanWithBranching(t *testing.T) {
	plan, err := Parse(analyzePlan)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlanningTime == nil || *plan.PlanningTime != 0.508 {
		t.Fatalf("planning time = %v, want 0.508", plan.PlanningTime)
	}
	if plan.ExecutionTime == nil || *plan.ExecutionTime != 6.819 {
		t.Fatalf("execution time = %v, want 6.819", plan.ExecutionTime)
	}

	limit := plan.Root
	if limit.Type != "limit" || limit.RowsActual == nil || *limit.RowsActual != 20 {
		t.Fatalf("unexpected root: %+v", limit)
	}
	sort := limit.Children[0]
	if sort.RowsEstimate == nil || sort.RowsActual == nil || *sort.RowsEstimate == *sort.RowsActual {
		t.Fatalf("expected Sort's estimate/actual rows to differ, got %+v", sort)
	}
	mergeJoin := sort.Children[0].Children[0]
	if mergeJoin.Type != "merge-join" || len(mergeJoin.Children) != 2 {
		t.Fatalf("expected Merge Join with 2 children, got type=%q children=%d", mergeJoin.Type, len(mergeJoin.Children))
	}
	idxScan := mergeJoin.Children[0]
	if idxScan.Type != "index-scan" || idxScan.Relation != "drivers" || idxScan.Alias != "d" {
		t.Fatalf("unexpected index scan node: %+v", idxScan)
	}
}

func TestHasPlan(t *testing.T) {
	if !HasPlan(simplePlan) {
		t.Fatal("expected HasPlan(simplePlan) to be true")
	}
	if HasPlan("select 1;\n") {
		t.Fatal("expected HasPlan on non-EXPLAIN text to be false")
	}
}

// EXPLAIN (COSTS OFF) — how the PostgreSQL regression suite, most
// documentation, and every PLAN_ADVICE example print plans. There is no
// "(cost=" anywhere in that output, which the original parser used as the
// signal for where the plan begins; it found nothing and returned an
// error. The QUERY PLAN header is the fallback marker.
func TestParseCostsOffPlan(t *testing.T) {
	const costsOff = `                  QUERY PLAN
-----------------------------------------------
 Hash Join
   Hash Cond: (results.driverid = drivers.driverid)
   ->  Seq Scan on results
   ->  Hash
         ->  Seq Scan on drivers
               Filter: (nationality = 'British'::text)
(6 rows)
`
	plan, err := Parse(costsOff)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Root.Type != "hash-join" {
		t.Fatalf("root type = %q, want hash-join", plan.Root.Type)
	}
	if len(plan.Root.Children) != 2 {
		t.Fatalf("root children = %d, want 2", len(plan.Root.Children))
	}
	if plan.Root.CostStart != nil {
		t.Fatal("COSTS OFF plan should carry no cost data")
	}
	inner := plan.Root.Children[1]
	if inner.Type != "hash" || len(inner.Children) != 1 {
		t.Fatalf("expected a Hash node with one child, got %+v", inner)
	}
	if got := inner.Children[0].Relation; got != "drivers" {
		t.Fatalf("inner scan relation = %q, want drivers", got)
	}
}

// A plan with neither costs nor a QUERY PLAN header still parses: the
// first non-empty line is the root.
func TestParseBareCostsOffPlan(t *testing.T) {
	plan, err := Parse(" Seq Scan on drivers\n   Filter: (nationality = 'British'::text)\n")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Root.Type != "seq-scan" || plan.Root.Relation != "drivers" {
		t.Fatalf("unexpected root: %+v", plan.Root)
	}
}

// EXPLAIN (BUFFERS) puts a "Planning:" block, with its own indented
// Buffers and I/O Timings lines, between the plan tree and the timings.
// The parser used to stop dead at "Planning:", which silently dropped
// both Planning Time and Execution Time from every buffered plan — and
// those timings are half of what a plan comparison reports.
func TestParseKeepsTimingsAfterPlanningBlock(t *testing.T) {
	const buffered = ` Limit  (cost=0.14..2.06 rows=10 width=40) (actual time=0.016..0.018 rows=10 loops=1)
   Buffers: shared hit=1 read=1
   ->  Index Scan using season_summary_year on season_summary  (cost=0.14..13.16 rows=68 width=40) (actual time=0.016..0.017 rows=10 loops=1)
         Buffers: shared hit=1 read=1
 Planning:
   Buffers: shared hit=61 read=1
   I/O Timings: shared read=0.009
 Planning Time: 0.181 ms
 Execution Time: 0.032 ms
(11 rows)
`
	plan, err := Parse(buffered)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlanningTime == nil || *plan.PlanningTime != 0.181 {
		t.Fatalf("planning time = %v, want 0.181", plan.PlanningTime)
	}
	if plan.ExecutionTime == nil || *plan.ExecutionTime != 0.032 {
		t.Fatalf("execution time = %v, want 0.032", plan.ExecutionTime)
	}
	// The planning block's own Buffers line must not have been collected
	// as a property of the deepest plan node.
	scan := plan.Root.Children[0]
	for _, p := range scan.Props {
		if strings.Contains(p, "hit=61") {
			t.Fatalf("planning-block line leaked into node props: %q", p)
		}
	}
}
