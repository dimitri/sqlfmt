package explain

import "strings"

// joinVariantRE recognizes a join node whose type carries a Semi/Anti/
// Left/Right/Full modifier — matched as a unit (rather than exploding the
// token table combinatorially) so the FULL matched text becomes the
// node's display Label (e.g. "Hash Anti Join"), while the base keyword
// still drives its color/style.
type joinVariant struct {
	prefix string
	typ    string
}

var joinVariantPrefixes = []joinVariant{
	{"Nested Loop", "nested-loop"},
	{"Merge", "merge-join"},
	{"Hash", "hash-join"},
}

var joinVariantInfixes = []string{" Left", " Right", " Full", " Semi", " Anti"}

// nodeTypeTokens maps a node-type token (longest first within any shared
// prefix) to its internal type keyword. Both spaced and run-together
// spellings are listed side by side for cross-Postgres-version
// compatibility (older/newer EXPLAIN output spells some of these
// differently), matching explain-plan-parser.lisp's *node-type-tokens*.
var nodeTypeTokens = []struct {
	token string
	typ   string
}{
	{"Index Only Scan Backward", "index-only-scan"},
	{"Index Only Scan", "index-only-scan"},
	{"Index Scan Backward", "index-scan"},
	{"Index Scan", "index-scan"},
	{"Bitmap Heap Scan", "bitmap-heap-scan"},
	{"Bitmap Index Scan", "bitmap-index-scan"},
	{"Tid Range Scan", "tid-range-scan"},
	{"Tid Scan", "tid-scan"},
	{"Sample Scan", "sample-scan"},
	{"Function Scan", "function-scan"},
	{"Table Function Scan", "table-function-scan"},
	{"Values Scan", "values-scan"},
	{"Foreign Scan", "foreign-scan"},
	{"Subquery Scan", "subquery-scan"},
	{"CTE Scan", "cte-scan"},
	{"WorkTable Scan", "worktable-scan"},
	{"Named Tuplestore Scan", "named-tuplestore-scan"},
	{"Custom Scan", "custom-scan"},
	{"Seq Scan", "seq-scan"},

	{"Hash Aggregate", "hash-aggregate"},
	{"HashAggregate", "hash-aggregate"},
	{"Group Aggregate", "group-aggregate"},
	{"GroupAggregate", "group-aggregate"},
	{"Mixed Aggregate", "mixed-aggregate"},
	{"MixedAggregate", "mixed-aggregate"},
	{"Aggregate", "aggregate"},
	{"Window Agg", "window-agg"},
	{"WindowAgg", "window-agg"},
	{"Group", "group"},

	{"Incremental Sort", "incremental-sort"},
	{"Sort", "sort"},
	{"Unique", "unique"},
	{"Merge Append", "merge-append"},
	{"Append", "append"},
	{"Materialize", "materialize"},
	{"Material", "materialize"},
	{"Memoize", "memoize"},
	{"Limit", "limit"},
	{"Lock Rows", "lock-rows"},
	{"LockRows", "lock-rows"},
	{"Set Op", "set-op"},
	{"SetOp", "set-op"},
	{"Result", "result"},
	{"Gather Merge", "gather-merge"},
	{"GatherMerge", "gather-merge"},
	{"Gather", "gather"},
	{"Recursive Union", "recursive-union"},
	{"Project Set", "project-set"},
	{"ProjectSet", "project-set"},

	{"ModifyTable", "modify-table"},
	{"Update", "update"},
	{"Insert", "insert"},
	{"Delete", "delete"},

	{"Hash", "hash"},
}

// typeLabelMap gives the human-readable display label for a type keyword
// when no override Label was set by the join-variant matcher.
var typeLabelMap = map[string]string{
	"index-only-scan": "Index Only Scan", "index-scan": "Index Scan",
	"bitmap-heap-scan": "Bitmap Heap Scan", "bitmap-index-scan": "Bitmap Index Scan",
	"tid-range-scan": "Tid Range Scan", "tid-scan": "Tid Scan",
	"sample-scan": "Sample Scan", "function-scan": "Function Scan",
	"table-function-scan": "Table Function Scan", "values-scan": "Values Scan",
	"foreign-scan": "Foreign Scan", "subquery-scan": "Subquery Scan",
	"cte-scan": "CTE Scan", "worktable-scan": "WorkTable Scan",
	"named-tuplestore-scan": "Named Tuplestore Scan", "custom-scan": "Custom Scan",
	"seq-scan":       "Seq Scan",
	"hash-aggregate": "Hash Aggregate", "group-aggregate": "Group Aggregate",
	"mixed-aggregate": "Mixed Aggregate", "aggregate": "Aggregate",
	"window-agg": "Window Agg", "group": "Group",
	"incremental-sort": "Incremental Sort", "sort": "Sort", "unique": "Unique",
	"merge-append": "Merge Append", "append": "Append", "materialize": "Materialize",
	"memoize": "Memoize", "limit": "Limit", "lock-rows": "Lock Rows",
	"set-op": "Set Op", "result": "Result", "gather-merge": "Gather Merge",
	"gather": "Gather", "recursive-union": "Recursive Union",
	"project-set": "Project Set", "modify-table": "ModifyTable",
	"update": "Update", "insert": "Insert", "delete": "Delete",
	"hash": "Hash", "hash-join": "Hash Join", "nested-loop": "Nested Loop",
	"merge-join": "Merge Join", "unknown": "Node",
}

// NodeLabel returns the display label for a node: its own override
// (join variants) if set, else the human-readable form of its type.
func NodeLabel(n *Node) string {
	if n.Label != "" {
		return n.Label
	}
	if lbl, ok := typeLabelMap[n.Type]; ok {
		return lbl
	}
	return "Node"
}

// matchNodeType classifies text (the node line with cost/actual already
// stripped) into (type, prefix, label, remainder) — remainder is
// whatever text followed the type token (used for relation/alias
// parsing on scan nodes), matching match-node-type.
func matchNodeType(text string) (typ, prefix, label, remainder string) {
	for _, p := range []string{"Parallel ", "Partial ", "Finalize "} {
		if strings.HasPrefix(text, p) {
			t, _, l, r := matchNodeType(text[len(p):])
			combinedPrefix := strings.TrimSpace(p)
			return t, combinedPrefix, l, r
		}
	}

	if jt, jl, jr, ok := matchJoinVariant(text); ok {
		return jt, "", jl, jr
	}

	for _, tok := range nodeTypeTokens {
		if strings.HasPrefix(text, tok.token) {
			after := text[len(tok.token):]
			if after == "" || isWordBoundary(after[0]) {
				return tok.typ, "", "", strings.TrimSpace(after)
			}
		}
	}
	return "unknown", "", "", text
}

func isWordBoundary(c byte) bool {
	return c == ' ' || c == '(' || c == '\n' || c == '\t'
}

// matchJoinVariant recognizes "(Nested Loop|Merge|Hash)((?: Left| Right|
// Full| Semi| Anti)+) Join" — the full matched prefix (e.g. "Hash Anti
// Join") becomes the display label, while the base keyword drives
// color/style, matching the Lisp parser's dedicated join-variant scanner.
func matchJoinVariant(text string) (typ, label, remainder string, ok bool) {
	for _, jv := range joinVariantPrefixes {
		if !strings.HasPrefix(text, jv.prefix) {
			continue
		}
		rest := text[len(jv.prefix):]
		matched := jv.prefix
		for {
			advanced := false
			for _, infix := range joinVariantInfixes {
				if strings.HasPrefix(rest, infix) {
					matched += infix
					rest = rest[len(infix):]
					advanced = true
					break
				}
			}
			if !advanced {
				break
			}
		}
		if strings.HasPrefix(rest, " Join") {
			matched += " Join"
			rest = rest[len(" Join"):]
			label = matched
			if label == jv.prefix+" Join" {
				label = "" // no variant infix matched — use the plain type label
			}
			return jv.typ, label, strings.TrimSpace(rest), true
		}
		// Bare "Nested Loop" (PostgreSQL's plain-inner-join spelling) has
		// NO trailing " Join" at all — unlike Hash/Merge, which are never
		// a complete join-node type name without it (bare "Hash" means
		// the build-side Hash node, a different node entirely, and bare
		// "Merge" isn't a real EXPLAIN node type at all). Left unhandled,
		// every plain (non-outer/semi/anti) Nested Loop node — the
		// overwhelming common case — fell all the way through to
		// type="unknown", displaying as a generic "Node" with the wrong
		// (non-join) color. Only accept the bare form here, and only when
		// nothing consumed by an infix (matched == jv.prefix, i.e. no
		// " Left"/" Right"/... already matched) and what follows is a
		// real word boundary, not a coincidental longer type name that
		// happens to start with "Nested Loop".
		if jv.typ == "nested-loop" && matched == jv.prefix && (rest == "" || isWordBoundary(rest[0])) {
			return jv.typ, "", strings.TrimSpace(rest), true
		}
	}
	return "", "", "", false
}
