package explain

import "testing"

// articlePlan is the plan PostgreSQL 19 itself reported advice for, in
// "Plan Advice in PostgreSQL 19" — three F1 tables, hash-joined, no
// parallelism, printed exactly as EXPLAIN (COSTS OFF, PLAN_ADVICE) gives
// it. Reproducing 19's own answer from this, on any version, is the whole
// claim this file exists to check.
const articlePlan = `                        QUERY PLAN
----------------------------------------------------------
 HashAggregate
   Group Key: drivers.surname
   ->  Hash Join
         Hash Cond: (results.driverid = drivers.driverid)
         ->  Hash Join
               Hash Cond: (results.raceid = races.raceid)
               ->  Seq Scan on results
               ->  Hash
                     ->  Seq Scan on races
                           Filter: (year = 2017)
         ->  Hash
               ->  Seq Scan on drivers
`

// PostgreSQL 19 reported exactly this for the plan above:
//
//	JOIN_ORDER(results races drivers)
//	HASH_JOIN(races drivers)
//	SEQ_SCAN(results races drivers)
//	NO_GATHER(results races drivers)
func TestAdviceMatchesPostgres19Output(t *testing.T) {
	plan, err := Parse(articlePlan)
	if err != nil {
		t.Fatal(err)
	}
	got := AdviceString(plan)
	want := "JOIN_ORDER(results races drivers)\n" +
		"HASH_JOIN(races drivers)\n" +
		"SEQ_SCAN(results races drivers)\n" +
		"NO_GATHER(results races drivers)"
	if got != want {
		t.Fatalf("advice mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// A merge join with an index scan on one side and a sorted seq scan on
// the other: exercises reading the inner side through a Sort, and the
// alias-over-relation naming rule.
func TestAdviceMergeJoinThroughSortAndAliases(t *testing.T) {
	plan, err := Parse(analyzePlan)
	if err != nil {
		t.Fatal(err)
	}
	got := AdviceString(plan)
	want := "JOIN_ORDER(d res)\n" +
		"MERGE_JOIN(res)\n" +
		"INDEX_SCAN(d)\n" +
		"SEQ_SCAN(res)\n" +
		"NO_GATHER(d res)"
	if got != want {
		t.Fatalf("advice mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// The structural claim the whole thing rests on: same shape, different
// numbers, identical advice. Costs and timings move constantly between
// runs; if they leaked into the advice it would be useless as a
// comparison key.
func TestAdviceIgnoresCostsAndTimings(t *testing.T) {
	fast := ` Hash Join  (cost=1.00..2.00 rows=1 width=8) (actual time=0.001..0.002 rows=1 loops=1)
   Hash Cond: (a.id = b.id)
   ->  Seq Scan on alpha a  (cost=0.00..1.00 rows=1 width=4) (actual time=0.001..0.001 rows=1 loops=1)
   ->  Hash  (cost=1.00..1.00 rows=1 width=4) (actual time=0.001..0.001 rows=1 loops=1)
         ->  Seq Scan on beta b  (cost=0.00..1.00 rows=1 width=4) (actual time=0.001..0.001 rows=1 loops=1)
`
	slow := ` Hash Join  (cost=9999.00..99999.00 rows=500000 width=8) (actual time=812.400..9999.999 rows=500000 loops=1)
   Hash Cond: (a.id = b.id)
   ->  Seq Scan on alpha a  (cost=0.00..44444.00 rows=500000 width=4) (actual time=0.900..500.100 rows=500000 loops=1)
   ->  Hash  (cost=4444.00..4444.00 rows=250000 width=4) (actual time=700.000..700.000 rows=250000 loops=1)
         ->  Seq Scan on beta b  (cost=0.00..4444.00 rows=250000 width=4) (actual time=0.500..300.000 rows=250000 loops=1)
`
	pf, err := Parse(fast)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := Parse(slow)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := AdviceString(pf), AdviceString(ps); a != b {
		t.Fatalf("same shape gave different advice:\n%s\n---\n%s", a, b)
	}
}

// The converse: a genuine structural change must show up. A Seq Scan
// becoming an Index Scan is the single most common "what changed?" and
// the advice has to say so.
func TestAdviceDetectsScanMethodChange(t *testing.T) {
	before := ` Seq Scan on drivers  (cost=0.00..30.40 rows=1 width=15)
   Filter: (nationality = 'British'::text)
`
	after := ` Index Scan using drivers_nationality on drivers  (cost=0.28..8.30 rows=1 width=15)
   Index Cond: (nationality = 'British'::text)
`
	pb, err := Parse(before)
	if err != nil {
		t.Fatal(err)
	}
	pa, err := Parse(after)
	if err != nil {
		t.Fatal(err)
	}
	gotB, gotA := AdviceString(pb), AdviceString(pa)
	if gotB == gotA {
		t.Fatal("scan method change produced identical advice")
	}
	if wantB := "JOIN_ORDER(drivers)\nSEQ_SCAN(drivers)\nNO_GATHER(drivers)"; gotB != wantB {
		t.Fatalf("before advice = %q, want %q", gotB, wantB)
	}
	if wantA := "JOIN_ORDER(drivers)\nINDEX_SCAN(drivers)\nNO_GATHER(drivers)"; gotA != wantA {
		t.Fatalf("after advice = %q, want %q", gotA, wantA)
	}
}

// Parallelism is one of the four decisions the format reports, so a
// Gather in the tree has to flip the last line.
func TestAdviceReportsGather(t *testing.T) {
	parallel := ` Gather  (cost=1000.00..2000.00 rows=100 width=8)
   Workers Planned: 2
   ->  Parallel Seq Scan on results  (cost=0.00..990.00 rows=42 width=8)
`
	plan, err := Parse(parallel)
	if err != nil {
		t.Fatal(err)
	}
	got := AdviceString(plan)
	want := "JOIN_ORDER(results)\nSEQ_SCAN(results)\nGATHER(results)"
	if got != want {
		t.Fatalf("advice = %q, want %q", got, want)
	}
}

// A Bitmap Index Scan prints "on <index>", so the parser stores an index
// name where every other node stores a relation. It must not turn up as a
// participant in JOIN_ORDER — the Bitmap Heap Scan above it is the one
// that names the actual table.
func TestAdviceExcludesBitmapIndexName(t *testing.T) {
	plan, err := Parse(` Bitmap Heap Scan on cards  (cost=18.91..1006.04 rows=346 width=32)
   Recheck Cond: (data @> '{"rarity": "Mythic Rare"}'::jsonb)
   ->  Bitmap Index Scan on cards_data_path_ops  (cost=0.00..18.82 rows=346 width=0)
         Index Cond: (data @> '{"rarity": "Mythic Rare"}'::jsonb)
`)
	if err != nil {
		t.Fatal(err)
	}
	got := AdviceString(plan)
	want := "JOIN_ORDER(cards)\nBITMAP_HEAP_SCAN(cards)\nNO_GATHER(cards)"
	if got != want {
		t.Fatalf("advice = %q, want %q", got, want)
	}
}

func TestAdviceOnNilPlanIsEmpty(t *testing.T) {
	if got := Advice(nil); got != nil {
		t.Fatalf("Advice(nil) = %v, want nil", got)
	}
	if got := AdviceString(&Plan{}); got != "" {
		t.Fatalf("AdviceString(empty) = %q, want empty", got)
	}
}
