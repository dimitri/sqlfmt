package explain

import "testing"

// The cases below are lifted verbatim from PostgreSQL's own regression
// output for contrib/pg_plan_advice — contrib/pg_plan_advice/expected/*.out.
// Each pairs a real plan with the advice PostgreSQL 19 itself generated
// for it, which makes them the only fixtures that can actually settle
// whether this package agrees with the server.
//
// Where "want" differs from what 19 printed, the difference is stated on
// the case. There is exactly one such difference, and it is structural
// rather than a bug: 19 schema-qualifies index names (public.foo_pkey)
// because the planner knows the schema, while text EXPLAIN prints the
// bare index name and the schema is simply not in the input.
func TestAdviceAgainstPostgres19Fixtures(t *testing.T) {
	cases := []struct {
		name string
		plan string
		want string
		note string
	}{
		{
			name: "hash join, expected/join_strategy.out",
			plan: ` Hash Join
   Hash Cond: (f.dim_id = d.id)
   ->  Seq Scan on join_fact f
   ->  Hash
         ->  Seq Scan on join_dim d
`,
			want: "JOIN_ORDER(f d)\nHASH_JOIN(d)\nSEQ_SCAN(f d)\nNO_GATHER(f d)",
		},
		{
			name: "merge join plain, expected/join_strategy.out",
			plan: ` Merge Join
   Merge Cond: (f.dim_id = d.id)
   ->  Index Scan using join_fact_dim_id on join_fact f
   ->  Index Scan using join_dim_pkey on join_dim d
`,
			want: "JOIN_ORDER(f d)\nMERGE_JOIN_PLAIN(d)\n" +
				"INDEX_SCAN(f join_fact_dim_id d join_dim_pkey)\nNO_GATHER(f d)",
			note: "19 prints INDEX_SCAN(f public.join_fact_dim_id d public.join_dim_pkey); " +
				"text EXPLAIN carries no schema, so ours is unqualified",
		},
		{
			name: "nested loop materialize, expected/join_strategy.out",
			plan: ` Nested Loop
   Join Filter: (f.dim_id = d.id)
   ->  Seq Scan on join_fact f
   ->  Materialize
         ->  Seq Scan on join_dim d
`,
			want: "JOIN_ORDER(f d)\nNESTED_LOOP_MATERIALIZE(d)\nSEQ_SCAN(f d)\nNO_GATHER(f d)",
		},
		{
			name: "nested loop memoize, expected/join_strategy.out",
			plan: ` Nested Loop
   ->  Seq Scan on join_fact f
   ->  Memoize
         Cache Key: f.dim_id
         ->  Index Scan using join_dim_pkey on join_dim d
               Index Cond: (id = f.dim_id)
`,
			want: "JOIN_ORDER(f d)\nNESTED_LOOP_MEMOIZE(d)\nSEQ_SCAN(f)\n" +
				"INDEX_SCAN(d join_dim_pkey)\nNO_GATHER(f d)",
			note: "19 schema-qualifies the index name; note the two scan " +
				"lines, one per access method, in first-appearance order",
		},
		{
			name: "seq scan, expected/scan.out",
			plan: ` Seq Scan on scan_table
`,
			want: "JOIN_ORDER(scan_table)\nSEQ_SCAN(scan_table)\nNO_GATHER(scan_table)",
			note: "19 emits no JOIN_ORDER for a single-relation query; " +
				"we always name the relation set, which diffs the same way",
		},
		{
			name: "index scan, expected/scan.out",
			plan: ` Index Scan using scan_table_pkey on scan_table
   Index Cond: (a = 1)
`,
			want: "JOIN_ORDER(scan_table)\nINDEX_SCAN(scan_table scan_table_pkey)\n" +
				"NO_GATHER(scan_table)",
			note: "19: INDEX_SCAN(scan_table public.scan_table_pkey), no JOIN_ORDER",
		},
		{
			name: "index only scan, expected/scan.out",
			plan: ` Index Only Scan using scan_table_pkey on scan_table
   Index Cond: (a = 1)
`,
			want: "JOIN_ORDER(scan_table)\nINDEX_ONLY_SCAN(scan_table scan_table_pkey)\n" +
				"NO_GATHER(scan_table)",
			note: "19: INDEX_ONLY_SCAN(scan_table public.scan_table_pkey), no JOIN_ORDER",
		},
		{
			name: "bitmap heap scan takes no index, expected/scan.out",
			plan: ` Bitmap Heap Scan on scan_table
   Recheck Cond: (b > 'some text 8'::text)
   ->  Bitmap Index Scan on scan_table_b
         Index Cond: (b > 'some text 8'::text)
`,
			want: "JOIN_ORDER(scan_table)\nBITMAP_HEAP_SCAN(scan_table)\nNO_GATHER(scan_table)",
			note: "the Bitmap Index Scan's index must NOT appear: 19's " +
				"BITMAP_HEAP_SCAN takes no index argument",
		},
		{
			name: "tid scan, expected/scan.out",
			plan: ` Tid Scan on scan_table
   TID Cond: (ctid = '(0,1)'::tid)
`,
			want: "JOIN_ORDER(scan_table)\nTID_SCAN(scan_table)\nNO_GATHER(scan_table)",
			note: "19 emits no JOIN_ORDER for a single relation",
		},
		{
			name: "gather over a join product, expected/gather.out",
			plan: ` Gather
   Workers Planned: 2
   ->  Hash Join
         Hash Cond: (f.dim_id = d.id)
         ->  Parallel Seq Scan on gt_fact f
         ->  Hash
               ->  Parallel Seq Scan on gt_dim d
`,
			want: "JOIN_ORDER(f d)\nHASH_JOIN(d)\nSEQ_SCAN(f d)\nGATHER((f d))",
			note: "matches 19 exactly, including the grouped GATHER((f d))",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := Parse(tc.plan)
			if err != nil {
				t.Fatal(err)
			}
			got := AdviceString(plan)
			if got != tc.want {
				t.Errorf("advice mismatch\n got:\n%s\nwant:\n%s", got, tc.want)
				if tc.note != "" {
					t.Logf("note: %s", tc.note)
				}
			}
		})
	}
}

// Repeated aliases get 19's #occurrence numbering (README, "Relation
// Identifiers"): the first occurrence is bare, later ones carry #N. This
// is the one part of 19's identifier syntax that IS recoverable from
// plan text, and without it a self-join collapses two distinct relations
// into one name.
func TestAdviceNumbersRepeatedAliases(t *testing.T) {
	plan, err := Parse(` Hash Join
   Hash Cond: (a.id = b.id)
   ->  Seq Scan on t a
   ->  Hash
         ->  Seq Scan on t a
`)
	if err != nil {
		t.Fatal(err)
	}
	got := AdviceString(plan)
	want := "JOIN_ORDER(a a#2)\nHASH_JOIN(a#2)\nSEQ_SCAN(a a#2)\nNO_GATHER(a a#2)"
	if got != want {
		t.Fatalf("advice = %q, want %q", got, want)
	}
}

// A join on the inner side is a bushy tree, which JOIN_ORDER parenthesizes
// per the README's JOIN_ORDER(t1 (t2 t3)); the join-method tag names the
// same group.
func TestAdviceParenthesizesBushyTree(t *testing.T) {
	plan, err := Parse(` Nested Loop
   ->  Seq Scan on t1
   ->  Hash Join
         Hash Cond: (t2.id = t3.id)
         ->  Seq Scan on t2
         ->  Hash
               ->  Seq Scan on t3
`)
	if err != nil {
		t.Fatal(err)
	}
	got := AdviceString(plan)
	want := "JOIN_ORDER(t1 (t2 t3))\n" +
		"HASH_JOIN(t3)\n" +
		"NESTED_LOOP_PLAIN((t2 t3))\n" +
		"SEQ_SCAN(t1 t2 t3)\n" +
		"NO_GATHER(t1 t2 t3)"
	if got != want {
		t.Fatalf("advice = %q, want %q", got, want)
	}
}

// Gather Merge is its own tag in 19, not a spelling of GATHER.
func TestAdviceGatherMergeIsItsOwnTag(t *testing.T) {
	plan, err := Parse(` Gather Merge
   Workers Planned: 2
   ->  Sort
         Sort Key: f.id
         ->  Parallel Seq Scan on gt_fact f
`)
	if err != nil {
		t.Fatal(err)
	}
	got := AdviceString(plan)
	want := "JOIN_ORDER(f)\nSEQ_SCAN(f)\nGATHER_MERGE(f)"
	if got != want {
		t.Fatalf("advice = %q, want %q", got, want)
	}
}

// Scan types outside 19's vocabulary get no scan advice at all. A
// subquery is always read with a subquery scan, so there is nothing to
// advise; emitting a made-up SUBQUERY_SCAN tag would be inventing
// vocabulary the server does not have.
func TestAdviceEmitsNoTagForUnadvisableScans(t *testing.T) {
	plan, err := Parse(` Function Scan on generate_series g
`)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range Advice(plan) {
		switch l.Kind {
		case "FUNCTION_SCAN", "SUBQUERY_SCAN", "VALUES_SCAN", "CTE_SCAN":
			t.Fatalf("emitted invented tag %q", l.Kind)
		}
	}
}
