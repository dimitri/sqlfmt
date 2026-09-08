package explain

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Run the same query against a real server twice -- once as TEXT, once as
// FORMAT JSON -- parse both, and require the two *Plan values to agree.
//
// This is the half PostgreSQL's own regression corpus cannot test. That
// corpus is written for stable diffs, so it is almost entirely EXPLAIN
// (COSTS OFF) with no ANALYZE and no BUFFERS -- which means it contains
// essentially none of the composite property lines (Buffers, I/O Timings,
// Sort Method, Buckets) that are the hard part of props.go. Here every
// query runs with ANALYZE and BUFFERS on, so those lines are exactly what
// gets compared.
//
// Shells out to psql rather than taking a driver dependency: this package
// has no business linking a Postgres client, and psql is what produced
// every capture it parses anyway.
//
//	PGEXPLAIN_DB=sqlfmt_regress go test ./explain/ -run TestLive -v
func TestLiveTextJSONAgree(t *testing.T) {
	db := os.Getenv("PGEXPLAIN_DB")
	if db == "" {
		t.Skip("set PGEXPLAIN_DB to a database with the regression tables loaded")
	}
	if _, err := exec.LookPath("psql"); err != nil {
		t.Skip("psql not on PATH")
	}

	for _, q := range liveQueries {
		t.Run(q.name, func(t *testing.T) {
			textOut, err := psql(db, "EXPLAIN (ANALYZE, BUFFERS) "+q.sql, aligned)
			if err != nil {
				t.Skipf("query not runnable here: %v", err)
			}
			jsonOut, err := psql(db, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+q.sql, unaligned)
			if err != nil {
				t.Fatalf("JSON EXPLAIN failed after TEXT succeeded: %v", err)
			}

			fromText, err := Parse(textOut)
			if err != nil {
				t.Fatalf("Parse(TEXT): %v\n%s", err, textOut)
			}
			fromJSON, err := ParseJSON([]byte(jsonOut))
			if err != nil {
				t.Fatalf("ParseJSON: %v", err)
			}

			compareTrees(t, "", fromText.Root, fromJSON.Root)
		})
	}
}

// compareTrees checks the structure and the property LINES, not the
// numbers: the two EXPLAINs are two separate executions of the query, so
// timings and even row counts legitimately differ between them. What must
// not differ is the shape of the plan and the set of properties each node
// reports -- and, for a property whose value is not a measurement, the
// value too.
func compareTrees(t *testing.T, path string, a, b *Node) {
	t.Helper()
	path += "/" + a.Type

	if a.Type != b.Type {
		t.Errorf("%s: node type text=%q json=%q", path, a.Type, b.Type)
		return
	}
	if a.Relation != b.Relation {
		t.Errorf("%s: relation text=%q json=%q", path, a.Relation, b.Relation)
	}
	if a.Alias != b.Alias {
		t.Errorf("%s: alias text=%q json=%q", path, a.Alias, b.Alias)
	}
	if a.Index != b.Index {
		t.Errorf("%s: index text=%q json=%q", path, a.Index, b.Index)
	}
	if !eqFloatPtr(a.CostStart, b.CostStart) || !eqFloatPtr(a.CostEnd, b.CostEnd) {
		t.Errorf("%s: costs differ (estimates, so they should not)", path)
	}
	if !eqIntPtr(a.RowsEstimate, b.RowsEstimate) {
		t.Errorf("%s: row estimate differs (an estimate, so it should not)", path)
	}

	// Property labels, in order. Values are compared only for the ones
	// that are not measurements -- a Buffers count moves between two runs
	// of the same query, an Index Cond does not.
	al, bl := propLabels(a), propLabels(b)
	if strings.Join(al, "|") != strings.Join(bl, "|") {
		t.Errorf("%s: property lines differ\n  text: %v\n  json: %v", path, rawOf(a), rawOf(b))
	} else {
		for i := range a.Props {
			if isMeasurement(a.Props[i].Label) {
				continue
			}
			if a.Props[i].Raw != b.Props[i].Raw {
				t.Errorf("%s: %q differs\n  text: %q\n  json: %q",
					path, a.Props[i].Label, a.Props[i].Raw, b.Props[i].Raw)
			}
		}
	}

	if len(a.Children) != len(b.Children) {
		t.Errorf("%s: %d children from text, %d from json", path, len(a.Children), len(b.Children))
		return
	}
	for i := range a.Children {
		compareTrees(t, path, a.Children[i], b.Children[i])
	}
}

// isMeasurement reports whether a property's value is something two runs
// of the same query may legitimately disagree about.
func isMeasurement(label string) bool {
	switch label {
	case "Buffers", "I/O Timings", "WAL", "Memory", "Storage",
		"Sort Method", "Buckets", "Batches", "Hits", "Estimates",
		"Heap Blocks", "Prefetch", "Rows Removed by Filter",
		"Rows Removed by Join Filter", "Heap Fetches", "Index Searches",
		"Full-sort Groups", "Pre-sorted Groups":
		return true
	}
	return false
}

func propLabels(n *Node) []string {
	out := make([]string, 0, len(n.Props))
	for _, p := range n.Props {
		out = append(out, p.Label)
	}
	return out
}

func rawOf(n *Node) []string {
	out := make([]string, 0, len(n.Props))
	for _, p := range n.Props {
		out = append(out, p.Raw)
	}
	return out
}

// The two formats need opposite psql settings, which is worth stating
// because getting it wrong looks like a parser bug:
//
//   - TEXT must be ALIGNED. Unaligned output strips the leading-space
//     padding that encodes a plan node's depth, so the whole tree comes
//     back as a flat list of siblings.
//   - JSON must be UNALIGNED. Aligned output pads a multi-line column
//     value and marks each continuation with a trailing "+", which is
//     not JSON any more.
type psqlAlign bool

const (
	aligned   psqlAlign = true
	unaligned psqlAlign = false
)

func psql(db, sql string, align psqlAlign) (string, error) {
	args := []string{"-X", "-q", "-t", "-v", "ON_ERROR_STOP=1"}
	if !align {
		args = append(args, "-A")
	}
	args = append(args, "-d", db, "-c", sql)
	cmd := exec.Command("psql", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", errorf(errb.String(), err)
	}
	return out.String(), nil
}

func errorf(stderr string, err error) error {
	if s := strings.TrimSpace(stderr); s != "" {
		return &psqlError{s}
	}
	return err
}

type psqlError struct{ msg string }

func (e *psqlError) Error() string { return e.msg }

func eqFloatPtr(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func eqIntPtr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Queries chosen to reach the plan nodes whose TEXT rendering is
// composite: a hash join (Buckets/Batches/Memory Usage), a sort (Sort
// Method + Memory), a hash aggregate (Batches/Memory Usage), a bitmap
// scan (Heap Blocks), a memoize (Hits/Misses/Estimates), and a gather
// (Workers Planned/Launched). All run against the standard regression
// tables.
var liveQueries = []struct{ name, sql string }{
	{"seqscan_filter", `SELECT * FROM onek WHERE unique1 < 100`},
	{"sort", `SELECT * FROM onek ORDER BY stringu1, unique2`},
	{"hash_agg", `SELECT four, count(*) FROM onek GROUP BY four`},
	{"group_agg", `SELECT four, count(*) FROM onek GROUP BY four ORDER BY four`},
	{"hash_join", `SELECT a.unique1, b.unique2 FROM onek a JOIN onek b ON a.unique1 = b.unique2`},
	{"nested_loop", `SELECT a.unique1 FROM onek a JOIN onek b ON a.unique1 = b.unique1 WHERE a.unique1 < 5`},
	{"merge_join", `SELECT a.unique1 FROM onek a JOIN onek b ON a.unique1 = b.unique1 ORDER BY a.unique1`},
	{"limit_sort", `SELECT * FROM onek ORDER BY unique1 DESC LIMIT 10`},
	{"distinct", `SELECT DISTINCT four, ten FROM onek`},
	{"union", `SELECT unique1 FROM onek UNION SELECT unique2 FROM onek`},
	{"cte", `WITH x AS (SELECT four, count(*) c FROM onek GROUP BY four) SELECT * FROM x WHERE c > 0`},
	{"subquery", `SELECT unique1 FROM onek WHERE unique2 IN (SELECT unique2 FROM onek WHERE four = 1)`},
	{"window", `SELECT unique1, rank() OVER (PARTITION BY four ORDER BY unique1) FROM onek`},
	{"except", `SELECT unique1 FROM onek EXCEPT SELECT unique2 FROM onek`},
	{"nested_agg", `SELECT four, sum(ten), avg(unique1) FROM onek GROUP BY four HAVING count(*) > 1`},
	{"self_join_agg", `SELECT a.four, count(*) FROM onek a JOIN onek b ON a.four = b.four GROUP BY a.four`},
	{"order_by_expr", `SELECT unique1 FROM onek ORDER BY unique1 % 7, unique2`},
	{"text_filter", `SELECT * FROM onek WHERE stringu1 > 'M'`},
	{"multi_join", `SELECT a.unique1 FROM onek a JOIN onek b ON a.unique1=b.unique1 JOIN onek c ON b.unique2=c.unique2`},
	{"agg_filter", `SELECT count(*) FILTER (WHERE four = 1), count(*) FROM onek`},
}
