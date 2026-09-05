package explain

import (
	"sort"
	"strings"
)

// Advice renders a plan's STRUCTURE in the shape PostgreSQL 19's
// pg_plan_advice generates:
//
//	JOIN_ORDER(results races drivers)
//	HASH_JOIN(races drivers)
//	SEQ_SCAN(results races drivers)
//	NO_GATHER(results races drivers)
//
// Why mirror a format from a version almost nobody runs yet: because of
// what the format leaves out. There is no cost in it, no row estimate, no
// timing — only the decisions the planner made about join order, join
// method, access method, and parallelism. That makes it the one rendering
// of a plan that is stable across runs of the same plan, and it is
// exactly what you need to answer the question a plain diff of two
// EXPLAIN outputs drowns in noise: did the SHAPE change, or did the
// numbers just move?
//
// PostgreSQL 19 computes this inside the planner, which knows everything.
// We are reconstructing it from a rendering, on any version back to the
// ones people are actually running. That difference has consequences, and
// they are stated rather than papered over:
//
//   - This is a COMPARISON KEY, not a round-trippable advice string. Do
//     not feed the output to pg_plan_advice and expect it to apply. Text
//     EXPLAIN does not carry the planner's internal relation identity, so
//     subqueries, CTEs and repeated aliases can be ambiguous here in ways
//     they never are inside the planner.
//   - Relation ordering within a line is ours (join order, see below),
//     not PostgreSQL's internal ordering. Two plans compared with each
//     other agree; a line compared byte-for-byte against a real 19 server
//     may not.
//   - Join-method variant spellings (19's MERGE_JOIN_PLAIN and friends)
//     are not reproduced. A merge join is MERGE_JOIN here.
//
// Within those limits it does the job it exists for: two plans of the
// same query produce identical Advice when, and only when, the planner
// made the same structural decisions.
func Advice(p *Plan) []AdviceLine {
	if p == nil || p.Root == nil {
		return nil
	}
	// Join order doubles as this package's canonical relation ordering:
	// every other line orders its relations by first appearance here, so
	// the whole block is deterministic for a given tree.
	order := joinOrder(p.Root)
	rank := make(map[string]int, len(order))
	for i, rel := range order {
		if _, seen := rank[rel]; !seen {
			rank[rel] = i
		}
	}
	byOrder := func(rels []string) []string {
		out := dedupe(rels)
		sort.SliceStable(out, func(i, j int) bool {
			ri, oki := rank[out[i]]
			rj, okj := rank[out[j]]
			switch {
			case oki && okj:
				return ri < rj
			case oki:
				return true
			case okj:
				return false
			}
			return out[i] < out[j]
		})
		return out
	}

	var lines []AdviceLine
	if rels := dedupe(order); len(rels) > 0 {
		lines = append(lines, AdviceLine{Kind: "JOIN_ORDER", Relations: rels})
	}

	for _, jm := range joinMethods(p.Root) {
		lines = append(lines, AdviceLine{Kind: jm.kind, Relations: byOrder(jm.inner)})
	}

	for _, sm := range scanMethods(p.Root) {
		lines = append(lines, AdviceLine{Kind: sm.kind, Relations: byOrder(sm.rels)})
	}

	gather := "NO_GATHER"
	if hasGather(p.Root) {
		gather = "GATHER"
	}
	if rels := dedupe(order); len(rels) > 0 {
		lines = append(lines, AdviceLine{Kind: gather, Relations: rels})
	}
	return lines
}

// AdviceLine is one line of the advice block: a decision kind and the
// relations it applies to.
type AdviceLine struct {
	Kind      string // "JOIN_ORDER", "HASH_JOIN", "SEQ_SCAN", "NO_GATHER", ...
	Relations []string
}

func (l AdviceLine) String() string {
	return l.Kind + "(" + strings.Join(l.Relations, " ") + ")"
}

// AdviceString renders Advice as the multi-line block, one decision per
// line, without a trailing newline.
func AdviceString(p *Plan) string {
	lines := Advice(p)
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.String())
	}
	return strings.Join(out, "\n")
}

// relName is how a base relation is named throughout the advice block:
// its alias when the query gave it one, else the relation itself. The
// alias is preferred because it is what distinguishes two scans of the
// same table in a self-join — exactly the case where using the bare
// relation name would collapse two different decisions into one.
func relName(n *Node) string {
	if n.Alias != "" {
		return n.Alias
	}
	return n.Relation
}

// isBaseRelation reports whether n reads a named relation, i.e. whether
// it contributes a name to the advice block. Nodes with no relation of
// their own (Sort, Hash, Aggregate, ...) never do.
//
// Bitmap Index Scan is the exception that has to be spelled out. It
// prints as "Bitmap Index Scan on <index>", so the parser stores the
// INDEX name in Relation — the only node type where that field is not a
// relation at all. Left in, an index name shows up as a participant in
// JOIN_ORDER, which is both wrong and confusing. The Bitmap Heap Scan
// directly above it already contributes the real relation.
func isBaseRelation(n *Node) bool {
	return n.Relation != "" && n.Type != "bitmap-index-scan"
}

// isJoin reports whether n is a join node.
func isJoin(n *Node) bool {
	switch n.Type {
	case "hash-join", "nested-loop", "merge-join":
		return true
	}
	return false
}

// joinOrder walks the tree outer-side-first, collecting base relations in
// the order the joins bring them in. For the usual left-deep tree that
// puts the driving relation first and each joined relation after it,
// which is what 19's JOIN_ORDER line reports.
func joinOrder(n *Node) []string {
	var out []string
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if isBaseRelation(n) {
			out = append(out, relName(n))
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(n)
	return out
}

type joinMethod struct {
	kind  string
	inner []string
}

// joinMethods collects one entry per join node, naming the relations on
// that join's INNER side — the side 19's HASH_JOIN(...)/MERGE_JOIN(...)
// lines name. Deepest join first, so the ordering is stable regardless of
// how the tree is nested.
//
// The inner side is read through whatever the join builds on it: a Hash
// Join's inner child is the Hash node, not the scan under it, and a merge
// join's inner side is routinely a Sort. Those wrappers carry no relation
// of their own, so descending through them lands on the relations that
// matter.
func joinMethods(root *Node) []joinMethod {
	var out []joinMethod
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
		if !isJoin(n) || len(n.Children) < 2 {
			return
		}
		kind := ""
		switch n.Type {
		case "hash-join":
			kind = "HASH_JOIN"
		case "nested-loop":
			kind = "NESTED_LOOP"
		case "merge-join":
			kind = "MERGE_JOIN"
		}
		inner := joinOrder(n.Children[1])
		if len(inner) == 0 {
			return
		}
		out = append(out, joinMethod{kind: kind, inner: inner})
	}
	walk(root)

	// Merge entries sharing a kind into one line, as 19 does: two hash
	// joins in one plan produce a single HASH_JOIN(...) naming both inner
	// sides, not two lines.
	var merged []joinMethod
	idx := map[string]int{}
	for _, jm := range out {
		if i, ok := idx[jm.kind]; ok {
			merged[i].inner = append(merged[i].inner, jm.inner...)
			continue
		}
		idx[jm.kind] = len(merged)
		merged = append(merged, jm)
	}
	return merged
}

type scanMethod struct {
	kind string
	rels []string
}

// scanMethodKind maps a parsed node type to its advice keyword. Only node
// types that actually read a relation appear; anything else returns "".
var scanMethodKind = map[string]string{
	"seq-scan":              "SEQ_SCAN",
	"index-scan":            "INDEX_SCAN",
	"index-only-scan":       "INDEX_ONLY_SCAN",
	"bitmap-heap-scan":      "BITMAP_HEAP_SCAN",
	"tid-scan":              "TID_SCAN",
	"tid-range-scan":        "TID_RANGE_SCAN",
	"sample-scan":           "SAMPLE_SCAN",
	"foreign-scan":          "FOREIGN_SCAN",
	"custom-scan":           "CUSTOM_SCAN",
	"function-scan":         "FUNCTION_SCAN",
	"table-function-scan":   "TABLE_FUNCTION_SCAN",
	"values-scan":           "VALUES_SCAN",
	"subquery-scan":         "SUBQUERY_SCAN",
	"cte-scan":              "CTE_SCAN",
	"worktable-scan":        "WORKTABLE_SCAN",
	"named-tuplestore-scan": "NAMED_TUPLESTORE_SCAN",
}

// scanMethods groups base relations by how they are read, one line per
// access method, in first-appearance order of the method itself.
//
// Bitmap Index Scan is deliberately absent: it names the index, and the
// Bitmap Heap Scan above it already names the relation, so counting both
// would name the same access path twice.
func scanMethods(root *Node) []scanMethod {
	var out []scanMethod
	idx := map[string]int{}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if isBaseRelation(n) {
			if kind, ok := scanMethodKind[n.Type]; ok {
				i, seen := idx[kind]
				if !seen {
					i = len(out)
					idx[kind] = i
					out = append(out, scanMethod{kind: kind})
				}
				out[i].rels = append(out[i].rels, relName(n))
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// hasGather reports whether any part of the plan runs in parallel, which
// decides between the GATHER and NO_GATHER lines.
func hasGather(n *Node) bool {
	if n == nil {
		return false
	}
	if n.Type == "gather" || n.Type == "gather-merge" {
		return true
	}
	for _, c := range n.Children {
		if hasGather(c) {
			return true
		}
	}
	return false
}

// dedupe removes repeats while keeping first-appearance order.
func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
