package explain

import (
	"strings"
	"testing"
)

// Every line below is a real one, copied out of captured psql output in
// the TAOP course corpus -- not invented. The composite shapes in
// particular are easy to get subtly wrong from the source alone (Buffers
// spells a counter "written" while the I/O Timings line directly beneath
// it spells the same idea "write"), so the fixtures are observations.
func TestParsePropComposite(t *testing.T) {
	for _, tc := range []struct {
		line  string
		label string
		want  map[string]string
	}{
		{
			"Buffers: shared hit=412",
			"Buffers",
			map[string]string{"Shared Hit Blocks": "412"},
		},
		{
			"Buffers: shared hit=1234 read=56 dirtied=7 written=8",
			"Buffers",
			map[string]string{
				"Shared Hit Blocks": "1234", "Shared Read Blocks": "56",
				"Shared Dirtied Blocks": "7", "Shared Written Blocks": "8",
			},
		},
		{
			// Comma separates scope groups; space separates counters.
			"Buffers: shared hit=95 read=3, temp read=8 written=8",
			"Buffers",
			map[string]string{
				"Shared Hit Blocks": "95", "Shared Read Blocks": "3",
				"Temp Read Blocks": "8", "Temp Written Blocks": "8",
			},
		},
		{
			// "write", not "written" -- the timings line differs from
			// the blocks line one row above it.
			"I/O Timings: shared read=0.123, temp read=4.5 write=6.7",
			"I/O Timings",
			map[string]string{
				"Shared I/O Read Time": "0.123",
				"Temp I/O Read Time":   "4.5", "Temp I/O Write Time": "6.7",
			},
		},
		{
			"Sort Method: quicksort  Memory: 25kB",
			"Sort Method",
			map[string]string{
				"Sort Method": "quicksort", "Sort Space Type": "Memory", "Sort Space Used": "25",
			},
		},
		{
			// Two-word method name: the separator is TWO spaces, so
			// "external merge" must survive as one value.
			"Sort Method: external merge  Disk: 12345kB",
			"Sort Method",
			map[string]string{
				"Sort Method": "external merge", "Sort Space Type": "Disk", "Sort Space Used": "12345",
			},
		},
		{
			"Sort Method: top-N heapsort  Memory: 27kB",
			"Sort Method",
			map[string]string{
				"Sort Method": "top-N heapsort", "Sort Space Type": "Memory", "Sort Space Used": "27",
			},
		},
		{
			// The line whose TEXT words share nothing with its JSON keys.
			"Buckets: 1024  Batches: 1  Memory Usage: 44kB",
			"Buckets",
			map[string]string{
				"Hash Buckets": "1024", "Hash Batches": "1", "Peak Memory Usage": "44",
			},
		},
		{
			"Buckets: 4096 (originally 1024)  Batches: 4 (originally 1)  Memory Usage: 96kB",
			"Buckets",
			map[string]string{
				"Hash Buckets": "4096", "Original Hash Buckets": "1024",
				"Hash Batches": "4", "Original Hash Batches": "1",
				"Peak Memory Usage": "96",
			},
		},
		{
			// Standalone Batches is HashAggregate's, not Hash's.
			"Batches: 1  Memory Usage: 105kB",
			"Batches",
			map[string]string{"HashAgg Batches": "1", "Peak Memory Usage": "105"},
		},
		{
			"Heap Blocks: exact=1234 lossy=5",
			"Heap Blocks",
			map[string]string{"Exact Heap Blocks": "1234", "Lossy Heap Blocks": "5"},
		},
		{
			"Heap Blocks: lossy=42",
			"Heap Blocks",
			map[string]string{"Lossy Heap Blocks": "42"},
		},
		{
			"Hits: 5  Misses: 2  Evictions: 0  Overflows: 0  Memory Usage: 1kB",
			"Hits",
			map[string]string{
				"Cache Hits": "5", "Cache Misses": "2", "Cache Evictions": "0",
				"Cache Overflows": "0", "Peak Memory Usage": "1",
			},
		},
		{
			"Estimates: capacity=8 distinct keys=4 lookups=10 hit percent=60.00%",
			"Estimates",
			map[string]string{
				"Estimated Capacity": "8", "Estimated Distinct Lookup Keys": "4",
				"Estimated Lookups": "10", "Estimated Hit Percent": "60.00",
			},
		},
		{
			"WAL: records=3 fpi=1 bytes=245",
			"WAL",
			map[string]string{"WAL Records": "3", "WAL FPI": "1", "WAL Bytes": "245"},
		},
		{
			"Full-sort Groups: 2  Sort Method: quicksort  Average Memory: 26kB  Peak Memory: 28kB",
			"Full-sort Groups",
			map[string]string{
				"Full-sort Group Count": "2", "Full-sort Sort Methods Used": "quicksort",
				"Full-sort Average Sort Space Used": "26", "Full-sort Peak Sort Space Used": "28",
			},
		},
		{
			"Storage: Memory  Maximum Storage: 32kB",
			"Storage",
			map[string]string{"Storage": "Memory", "Maximum Storage": "32"},
		},
		{
			"Memory: used=120kB  allocated=256kB",
			"Memory",
			map[string]string{"Memory Used": "120", "Memory Allocated": "256"},
		},
	} {
		t.Run(tc.line, func(t *testing.T) {
			p := ParseProp(tc.line)
			if p.Raw != tc.line {
				t.Errorf("Raw = %q, want the line unchanged", p.Raw)
			}
			if p.Label != tc.label {
				t.Errorf("Label = %q, want %q", p.Label, tc.label)
			}
			if len(p.Fields) != len(tc.want) {
				t.Errorf("Fields = %v, want %v", p.Fields, tc.want)
			}
			for k, want := range tc.want {
				if got, ok := p.Fields[k]; !ok || got != want {
					t.Errorf("Fields[%q] = %q (present=%v), want %q", k, got, ok, want)
				}
			}
		})
	}
}

// The straightforward majority: the TEXT label IS the JSON key, so the
// only work is stripping the unit suffix TEXT adds and JSON omits.
func TestParsePropSimple(t *testing.T) {
	for _, tc := range []struct{ line, label, value string }{
		{"Filter: (points = ANY ('{25,18}'::double precision[]))", "Filter", "(points = ANY ('{25,18}'::double precision[]))"},
		{"Index Cond: (driverid = 1)", "Index Cond", "(driverid = 1)"},
		{"Rows Removed by Filter: 18450", "Rows Removed by Filter", "18450"},
		{"Sort Key: results.points DESC", "Sort Key", "results.points DESC"},
		{"Heap Fetches: 0", "Heap Fetches", "0"},
		{"Workers Planned: 2", "Workers Planned", "2"},
		{"Peak Memory Usage: 44kB", "Peak Memory Usage", "44"},
		// Citus.
		{"Task Count: 32", "Task Count", "32"},
		{"Tasks Shown: One of 32", "Tasks Shown", "One of 32"},
		{"Node: host=worker1a port=5432 dbname=demo", "Node", "host=worker1a port=5432 dbname=demo"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			p := ParseProp(tc.line)
			if p.Label != tc.label || p.Value != tc.value {
				t.Errorf("got Label=%q Value=%q, want %q / %q", p.Label, p.Value, tc.label, tc.value)
			}
			if v := p.Fields[tc.label]; v != tc.value {
				t.Errorf("Fields[%q] = %q, want %q", tc.label, v, tc.value)
			}
		})
	}
}

// An unrecognised line must survive intact and unflagged. EXPLAIN is
// extensible; this corpus alone already carries PostgreSQL 19's plan
// advice lines, which no released server emitted when this table was
// written.
func TestParsePropUnknownSurvives(t *testing.T) {
	for _, line := range []string{
		"Supplied Plan Advice: SEQ_SCAN(res)",
		"Some Future Extension Counter: 42 widgets",
		"a line with no colon at all",
	} {
		p := ParseProp(line)
		if p.Raw != line {
			t.Errorf("ParseProp(%q).Raw = %q — a line must never be altered", line, p.Raw)
		}
	}
	if p := ParseProp("a line with no colon at all"); p.Known() {
		t.Errorf("a line with no colon should not be Known(), got Label=%q", p.Label)
	}
	// A "Label: value" shape not in the vocabulary still splits, so a
	// caller can read it, but claims no kind.
	p := ParseProp("Supplied Plan Advice: SEQ_SCAN(res)")
	if !p.Known() || p.Value != "SEQ_SCAN(res)" {
		t.Errorf("unknown-but-well-shaped line: Label=%q Value=%q", p.Label, p.Value)
	}
}

// Block headers ("Planning:", "JIT:", "Settings:") introduce indented
// lines rather than carrying a value.
func TestParsePropBlockHeader(t *testing.T) {
	for _, h := range []string{"Planning:", "JIT:", "Settings:", "Triggers:"} {
		p := ParseProp(h)
		if p.Label != h[:len(h)-1] || p.Value != "" {
			t.Errorf("ParseProp(%q) = Label %q Value %q, want the header name and no value", h, p.Label, p.Value)
		}
	}
}

// A subplan header is structure, not a property. It used to be collected
// as a Prop of the PARENT node -- the single largest class of
// unrecognised property line across PostgreSQL's regression corpus (268
// of 1076 occurrences) -- and it names the node BELOW it.
func TestSubplanNameIsStructureNotAProperty(t *testing.T) {
	const plan = ` Aggregate  (cost=1.05..1.06 rows=1 width=8)
   InitPlan 1 (returns $0)
     ->  Limit  (cost=0.00..0.02 rows=1 width=4)
           ->  Seq Scan on onek  (cost=0.00..13.00 rows=1000 width=4)
   ->  Result  (cost=0.00..1.01 rows=1 width=8)
   CTE x
     ->  Seq Scan on onek onek_1  (cost=0.00..13.00 rows=1000 width=4)
`
	p, err := Parse(plan)
	if err != nil {
		t.Fatal(err)
	}

	for _, pr := range p.Root.Props {
		if strings.HasPrefix(pr.Raw, "InitPlan") || strings.HasPrefix(pr.Raw, "CTE ") {
			t.Errorf("subplan header collected as a property of the parent: %q", pr.Raw)
		}
	}

	var names []string
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.SubplanName != "" {
			names = append(names, n.SubplanName)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(p.Root)

	want := []string{"InitPlan 1 (returns $0)", "CTE x"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("SubplanName values = %q, want %q", names, want)
	}

	// The named node is the one below the header, not beside it.
	if p.Root.Children[0].Type != "limit" {
		t.Errorf("the InitPlan header should name the Limit, got %q", p.Root.Children[0].Type)
	}
}

// A plan-advice block decorates the whole plan, not any node in it.
// contrib/pg_plan_advice prints it after the tree; before this it was
// collected as property lines of whatever node was on the stack, which
// put "NO_GATHER(...)" on a Seq Scan.
func TestPlanAdviceIsPlanLevel(t *testing.T) {
	const plan = ` HashAggregate
   Group Key: drivers.surname
   ->  Hash Join
         Hash Cond: (results.driverid = drivers.driverid)
         ->  Seq Scan on results
 Supplied Plan Advice:
   JOIN_ORDER(drivers results races) /* matched */
 Generated Plan Advice:
   JOIN_ORDER(results races drivers)
   SEQ_SCAN(alt_t1 alt_t2@exists_to_any_1)
   NO_GATHER(results races drivers)
`
	p, err := Parse(plan)
	if err != nil {
		t.Fatal(err)
	}

	var walk func(n *Node)
	walk = func(n *Node) {
		for _, pr := range n.Props {
			if strings.Contains(pr.Raw, "JOIN_ORDER") || strings.Contains(pr.Raw, "Plan Advice") {
				t.Errorf("advice leaked into node %q as a property: %q", n.Type, pr.Raw)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(p.Root)

	if len(p.SuppliedAdvice) != 1 {
		t.Fatalf("supplied advice = %d items, want 1", len(p.SuppliedAdvice))
	}
	got := p.SuppliedAdvice[0]
	if got.Kind != "JOIN_ORDER" || got.Annotation != "matched" {
		t.Errorf("supplied[0] = kind %q annotation %q, want JOIN_ORDER / matched", got.Kind, got.Annotation)
	}
	if got.String() != "JOIN_ORDER(drivers results races)" {
		t.Errorf("supplied[0].String() = %q — the annotation must not survive into the item itself", got.String())
	}

	if len(p.GeneratedAdvice) != 3 {
		t.Fatalf("generated advice = %d items, want 3", len(p.GeneratedAdvice))
	}
	// A target can carry a subplan qualifier; it is part of the arg.
	// Each target is its own argument, and a target may carry a subplan
	// qualifier.
	if got := strings.Join(p.GeneratedAdvice[1].Args, "|"); got != "alt_t1|alt_t2@exists_to_any_1" {
		t.Errorf("generated[1] args = %q", p.GeneratedAdvice[1].Args)
	}
	// The tree is untouched by all of this.
	if p.Root.Type != "hash-aggregate" || len(p.Root.Children) != 1 {
		t.Errorf("the plan tree changed shape: %q with %d children", p.Root.Type, len(p.Root.Children))
	}
}

// SetOp spells its strategy into the node name and its command after it;
// collapsing both to "Set Op" lost which set operation this even was.
func TestSetOpKeepsStrategyAndCommand(t *testing.T) {
	for _, tc := range []struct{ line, label, strategy, command string }{
		{" HashSetOp Except  (cost=1.00..2.00 rows=1 width=4)", "HashSetOp Except", "Hashed", "Except"},
		{" SetOp Intersect All  (cost=1.00..2.00 rows=1 width=4)", "SetOp Intersect All", "Sorted", "Intersect All"},
		{" HashSetOp Except All  (cost=1.00..2.00 rows=1 width=4)", "HashSetOp Except All", "Hashed", "Except All"},
	} {
		p, err := Parse(tc.line + "\n")
		if err != nil {
			t.Fatal(err)
		}
		if got := NodeLabel(p.Root); got != tc.label {
			t.Errorf("NodeLabel = %q, want %q", got, tc.label)
		}
		if p.Root.Type != "set-op" {
			t.Errorf("type = %q, want set-op (the colour category is shared)", p.Root.Type)
		}
		nt, strategy, command, ok := splitStrategyLabel(p.Root)
		if !ok || nt != "SetOp" || strategy != tc.strategy || command != tc.command {
			t.Errorf("json split = %q/%q/%q, want SetOp/%q/%q", nt, strategy, command, tc.strategy, tc.command)
		}
	}
}
