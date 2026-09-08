package explain

import (
	"os"
	"strings"
	"testing"
)

// textAndJSON is the same plan captured both ways. Keeping them side by
// side in one fixture is the point of the test: the claim this package
// makes is that a plan is one value regardless of which format it
// arrived in, and the only way to hold that claim honest is to build it
// from both and compare.
const textPlan = ` Hash Join  (cost=1.09..2.20 rows=5 width=36) (actual time=0.030..0.045 rows=5 loops=1)
   Hash Cond: (res.constructorid = c.constructorid)
   Buffers: shared hit=8
   ->  Seq Scan on results res  (cost=0.00..1.05 rows=5 width=8) (actual time=0.008..0.010 rows=5 loops=1)
         Filter: (raceid = 890)
         Rows Removed by Filter: 12
         Buffers: shared hit=5
   ->  Hash  (cost=1.04..1.04 rows=4 width=36) (actual time=0.012..0.012 rows=4 loops=1)
         Buckets: 1024  Batches: 1  Memory Usage: 9kB
         Buffers: shared hit=3
         ->  Seq Scan on constructors c  (cost=0.00..1.04 rows=4 width=36) (actual time=0.005..0.006 rows=4 loops=1)
               Buffers: shared hit=3
 Planning Time: 0.150 ms
 Execution Time: 0.070 ms
`

// The JSON below is the same plan as textPlan. Note what JSON carries
// that TEXT does not print: every buffer counter including the zeros,
// and "Parallel Aware"/"Async Capable" flags that are false.
const jsonPlan = `[
  {
    "Plan": {
      "Node Type": "Hash Join", "Parallel Aware": false, "Async Capable": false,
      "Join Type": "Inner", "Startup Cost": 1.09, "Total Cost": 2.20,
      "Plan Rows": 5, "Plan Width": 36,
      "Actual Startup Time": 0.030, "Actual Total Time": 0.045,
      "Actual Rows": 5, "Actual Loops": 1,
      "Inner Unique": false,
      "Hash Cond": "(res.constructorid = c.constructorid)",
      "Shared Hit Blocks": 8, "Shared Read Blocks": 0,
      "Shared Dirtied Blocks": 0, "Shared Written Blocks": 0,
      "Local Hit Blocks": 0, "Temp Read Blocks": 0, "Temp Written Blocks": 0,
      "Plans": [
        {
          "Node Type": "Seq Scan", "Parent Relationship": "Outer",
          "Parallel Aware": false, "Relation Name": "results", "Alias": "res",
          "Startup Cost": 0.00, "Total Cost": 1.05, "Plan Rows": 5, "Plan Width": 8,
          "Actual Startup Time": 0.008, "Actual Total Time": 0.010,
          "Actual Rows": 5, "Actual Loops": 1,
          "Filter": "(raceid = 890)", "Rows Removed by Filter": 12,
          "Shared Hit Blocks": 5, "Shared Read Blocks": 0
        },
        {
          "Node Type": "Hash", "Parent Relationship": "Inner", "Parallel Aware": false,
          "Startup Cost": 1.04, "Total Cost": 1.04, "Plan Rows": 4, "Plan Width": 36,
          "Actual Startup Time": 0.012, "Actual Total Time": 0.012,
          "Actual Rows": 4, "Actual Loops": 1,
          "Hash Buckets": 1024, "Original Hash Buckets": 1024,
          "Hash Batches": 1, "Original Hash Batches": 1, "Peak Memory Usage": 9,
          "Shared Hit Blocks": 3, "Shared Read Blocks": 0,
          "Plans": [
            {
              "Node Type": "Seq Scan", "Parent Relationship": "Outer",
              "Parallel Aware": false, "Relation Name": "constructors", "Alias": "c",
              "Startup Cost": 0.00, "Total Cost": 1.04, "Plan Rows": 4, "Plan Width": 36,
              "Actual Startup Time": 0.005, "Actual Total Time": 0.006,
              "Actual Rows": 4, "Actual Loops": 1,
              "Shared Hit Blocks": 3, "Shared Read Blocks": 0
            }
          ]
        }
      ]
    },
    "Planning Time": 0.150,
    "Execution Time": 0.070
  }
]`

func TestJSONMatchesText(t *testing.T) {
	fromText, err := Parse(textPlan)
	if err != nil {
		t.Fatalf("Parse(TEXT): %v", err)
	}
	fromJSON, err := ParseJSON([]byte(jsonPlan))
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}

	var walk func(path string, a, b *Node)
	walk = func(path string, a, b *Node) {
		t.Helper()
		if a.Type != b.Type {
			t.Errorf("%s: Type text=%q json=%q", path, a.Type, b.Type)
		}
		if a.Relation != b.Relation || a.Alias != b.Alias {
			t.Errorf("%s: relation text=%q/%q json=%q/%q", path, a.Relation, a.Alias, b.Relation, b.Alias)
		}
		if !eqFloat(a.CostStart, b.CostStart) || !eqFloat(a.CostEnd, b.CostEnd) {
			t.Errorf("%s: costs differ", path)
		}
		if !eqInt(a.RowsEstimate, b.RowsEstimate) || !eqInt(a.RowsActual, b.RowsActual) {
			t.Errorf("%s: rows differ", path)
		}
		if !eqFloat(a.TimeActual, b.TimeActual) {
			t.Errorf("%s: actual time differs", path)
		}

		// The real assertion: the property LINES reconstructed from JSON
		// are the lines the server printed in TEXT, in the same order.
		// This is where JSON's zero counters and false flags would show
		// up if they leaked through.
		textLines := rawProps(a)
		jsonLines := rawProps(b)
		if strings.Join(textLines, "\n") != strings.Join(jsonLines, "\n") {
			t.Errorf("%s: property lines differ\n  text: %q\n  json: %q", path, textLines, jsonLines)
		}

		if len(a.Children) != len(b.Children) {
			t.Fatalf("%s: %d children from text, %d from json", path, len(a.Children), len(b.Children))
		}
		for i := range a.Children {
			walk(path+"/"+a.Children[i].Type, a.Children[i], b.Children[i])
		}
	}
	walk(fromText.Root.Type, fromText.Root, fromJSON.Root)

	if !eqFloat(fromText.PlanningTime, fromJSON.PlanningTime) ||
		!eqFloat(fromText.ExecutionTime, fromJSON.ExecutionTime) {
		t.Errorf("plan timings differ")
	}
}

// Every Prop parsed out of the TEXT capture must name its values with the
// same JSON keys the JSON capture uses for them. That is the whole
// contract of props.go, checked against a real pair rather than a
// hand-written expectation.
func TestTextPropsCarryJSONKeys(t *testing.T) {
	plan, err := Parse(textPlan)
	if err != nil {
		t.Fatal(err)
	}
	hash := plan.Root.Children[1]
	if hash.Type != "hash" {
		t.Fatalf("expected the Hash node, got %q", hash.Type)
	}

	want := map[string]string{
		"Hash Buckets": "1024", "Hash Batches": "1", "Peak Memory Usage": "9",
	}
	var found map[string]string
	for _, p := range hash.Props {
		if p.Label == "Buckets" {
			found = p.Fields
		}
	}
	if found == nil {
		t.Fatal("no Buckets property parsed from the TEXT capture")
	}
	for k, v := range want {
		if found[k] != v {
			t.Errorf("Fields[%q] = %q, want %q (the JSON capture spells it this way)", k, found[k], v)
		}
	}
}

// A real capture from a live server, not a hand-written fixture: an
// EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) of a three-way join, taken
// from the Read Query Plans course. It exercises the pre-PostgreSQL-18
// unscoped "I/O Read Time" keys as a side effect of being that old.
func TestParseJSONRealCapture(t *testing.T) {
	data, err := os.ReadFile("testdata/pg17-analyze-buffers.json")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ParseJSON(data)
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}

	if plan.Root.Type != "sort" {
		t.Errorf("root type = %q, want sort", plan.Root.Type)
	}
	if plan.ExecutionTime == nil || *plan.ExecutionTime != 3.007 {
		t.Errorf("execution time = %v, want 3.007", plan.ExecutionTime)
	}

	// The root Sort reports 542 shared hits and a quicksort in 25kB. Both
	// are composite lines in TEXT, assembled here from four JSON keys.
	props := rawProps(plan.Root)
	assertHas(t, props, "Buffers: shared hit=542")
	assertHas(t, props, "Sort Method: quicksort  Memory: 25kB")

	// ... and none of JSON's zero counters leaked into them.
	for _, p := range props {
		if strings.Contains(p, "=0") {
			t.Errorf("a zero counter JSON reports but TEXT omits leaked into %q", p)
		}
	}

	// The Hash node's composite line, from five JSON keys.
	var hashNode *Node
	var find func(n *Node)
	find = func(n *Node) {
		if n.Type == "hash" && hashNode == nil {
			hashNode = n
		}
		for _, c := range n.Children {
			find(c)
		}
	}
	find(plan.Root)
	if hashNode == nil {
		t.Fatal("no Hash node in the capture")
	}
	assertHas(t, rawProps(hashNode), "Buckets: 1024  Batches: 1  Memory Usage: 9kB")
}

func rawProps(n *Node) []string {
	out := make([]string, 0, len(n.Props))
	for _, p := range n.Props {
		out = append(out, p.Raw)
	}
	return out
}

func assertHas(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, l := range lines {
		if l == want {
			return
		}
	}
	t.Errorf("missing property line %q\n  got: %q", want, lines)
}

func eqFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func eqInt(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// TEXT -> Plan -> JSON -> Plan -> TEXT is the round trip that has to
// hold: it is what lets a pasted plan reach a tool that only reads JSON
// and come back unchanged. (The other direction is lossy by design --
// see ToJSON's doc comment.)
func TestRoundTripTextThroughJSON(t *testing.T) {
	first, err := Parse(textPlan)
	if err != nil {
		t.Fatal(err)
	}
	data, err := ToJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseJSON(data)
	if err != nil {
		t.Fatalf("ParseJSON(ToJSON(...)): %v\n%s", err, data)
	}

	var walk func(path string, a, b *Node)
	walk = func(path string, a, b *Node) {
		t.Helper()
		if a.Type != b.Type {
			t.Errorf("%s: Type %q -> %q", path, a.Type, b.Type)
		}
		if strings.Join(rawProps(a), "\n") != strings.Join(rawProps(b), "\n") {
			t.Errorf("%s: property lines changed\n  before: %q\n  after:  %q", path, rawProps(a), rawProps(b))
		}
		if len(a.Children) != len(b.Children) {
			t.Fatalf("%s: child count %d -> %d", path, len(a.Children), len(b.Children))
		}
		for i := range a.Children {
			walk(path+"/"+a.Children[i].Type, a.Children[i], b.Children[i])
		}
	}
	walk(first.Root.Type, first.Root, again.Root)
}
