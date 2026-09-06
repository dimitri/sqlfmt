package explain

import "strings"

// Advice renders a plan's STRUCTURE in the format PostgreSQL 19's
// pg_plan_advice generates:
//
//	JOIN_ORDER(f d)
//	MERGE_JOIN_PLAIN(d)
//	INDEX_SCAN(f join_fact_dim_id d join_dim_pkey)
//	NO_GATHER(f d)
//
// Why mirror a format from a version almost nobody runs yet: because of
// what it leaves out. There is no cost in it, no row estimate, no timing
// — only the decisions the planner made about join order, join method,
// access method, and parallelism. That makes it the one rendering of a
// plan that is stable across runs, and therefore the answer to the
// question a plain diff of two EXPLAIN outputs drowns in noise: did the
// SHAPE change, or did the numbers just move?
//
// The tag vocabulary, argument conventions and emission order here were
// read off contrib/pg_plan_advice in the PostgreSQL source rather than
// guessed: tags from pgpa_cstring_advice_tag() in pgpa_ast.c, the
// join-strategy distinctions from pgpa_join.c, argument shapes from the
// module README, and line order from pgpa_output_advice().
//
// # What this can and cannot do
//
// PostgreSQL 19 generates advice inside the planner, which knows the
// query's whole structure. This reconstructs it from EXPLAIN's rendering
// of the finished plan, on any version. Most of it survives that
// translation exactly. Some of it cannot, and the gaps are named rather
// than papered over:
//
//   - Index names are NOT schema-qualified. Real generated advice prints
//     "public.join_dim_pkey"; text EXPLAIN only ever prints the bare
//     index name, and the schema is not recoverable from it.
//   - Relation identifiers carry no @plan_name subquery qualifier and no
//     /schema.partition qualifier. Both come from planner internals that
//     EXPLAIN does not print. Repeated aliases DO get 19's #occurrence
//     numbering, which is recoverable.
//   - DO_NOT_SCAN, PARTITIONWISE, SEMIJOIN_UNIQUE, SEMIJOIN_NON_UNIQUE
//     and FOREIGN_JOIN are never emitted. They describe decisions that
//     leave no distinguishable trace in plan text.
//   - JOIN_ORDER's "{a b}" unordered-group syntax is never emitted; it
//     marks joins whose sides are undefined, which is planner knowledge.
//     Parenthesized groups for non-outer-deep trees ARE emitted, since
//     tree shape is exactly what EXPLAIN shows.
//
// So: this is a COMPARISON KEY, dependable for telling whether two plans
// of the same query are structurally the same. It is not a
// round-trippable advice string — do not feed it to pg_plan_advice and
// expect it to apply.
func Advice(p *Plan) []AdviceLine {
	if p == nil || p.Root == nil {
		return nil
	}
	names := newNamer()
	rels := baseRelations(p.Root)
	for _, n := range rels {
		names.assign(n)
	}

	var lines []AdviceLine

	// Emission order follows pgpa_output_advice(): join order, then join
	// methods, then scans, then the gather decision.
	if root := topJoin(p.Root); root != nil {
		if s := joinOrderTerm(root, names); s != "" {
			lines = append(lines, AdviceLine{Kind: "JOIN_ORDER", Args: []string{s}})
		}
	} else if len(rels) > 0 {
		lines = append(lines, AdviceLine{Kind: "JOIN_ORDER", Args: names.namesOf(rels)})
	}

	for _, jm := range joinMethods(p.Root, names) {
		lines = append(lines, AdviceLine{Kind: jm.kind, Args: jm.args})
	}
	for _, sm := range scanStrategies(p.Root, names) {
		lines = append(lines, AdviceLine{Kind: sm.kind, Args: sm.args})
	}

	// GATHER/GATHER_MERGE name what runs in parallel; NO_GATHER names
	// everything when nothing does.
	gathered, gatherKind := gatherScans(p.Root, names)
	if len(gathered) > 0 {
		lines = append(lines, AdviceLine{Kind: gatherKind, Args: gathered})
	} else if len(rels) > 0 {
		lines = append(lines, AdviceLine{Kind: "NO_GATHER", Args: names.namesOf(rels)})
	}
	return lines
}

// AdviceLine is one advice item: a tag applied to a list of targets.
type AdviceLine struct {
	Kind string   // "JOIN_ORDER", "HASH_JOIN", "SEQ_SCAN", "NO_GATHER", ...
	Args []string // targets, already rendered (a group is one "(a b)" arg)
}

func (l AdviceLine) String() string {
	return l.Kind + "(" + strings.Join(l.Args, " ") + ")"
}

// AdviceString renders Advice as the multi-line block, one item per line,
// without a trailing newline.
func AdviceString(p *Plan) string {
	lines := Advice(p)
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.String())
	}
	return strings.Join(out, "\n")
}

// namer assigns 19's relation identifiers. The README's general form is
// alias#occurrence/schema.partition@plan; of those, alias and occurrence
// are recoverable from plan text and the rest are not. Occurrence numbers
// start at 1 and the first is omitted, so a self-join reads "foo foo#2".
type namer struct {
	seen  map[string]int
	byPtr map[*Node]string
}

func newNamer() *namer {
	return &namer{seen: map[string]int{}, byPtr: map[*Node]string{}}
}

func (nm *namer) assign(n *Node) string {
	if s, ok := nm.byPtr[n]; ok {
		return s
	}
	base := n.Relation
	if n.Alias != "" {
		base = n.Alias
	}
	nm.seen[base]++
	s := base
	if c := nm.seen[base]; c > 1 {
		s = base + "#" + itoa(c)
	}
	nm.byPtr[n] = s
	return s
}

func (nm *namer) nameOf(n *Node) string { return nm.byPtr[n] }

func (nm *namer) namesOf(ns []*Node) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		if s := nm.byPtr[n]; s != "" {
			out = append(out, s)
		}
	}
	return out
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// isBaseRelation reports whether n contributes a relation identifier.
//
// Bitmap Index Scan is excluded deliberately: it prints "on <index>", so
// the parser stores an INDEX name in Relation — the only node type where
// that field is not a relation. The Bitmap Heap Scan above it names the
// real table, and 19's BITMAP_HEAP_SCAN tag takes no index argument.
func isBaseRelation(n *Node) bool {
	return n.Relation != "" && n.Type != "bitmap-index-scan"
}

// baseRelations lists the relation-bearing nodes in plan order (outer
// side first), which is also the order 19 lists them in JOIN_ORDER.
func baseRelations(n *Node) []*Node {
	var out []*Node
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if isBaseRelation(n) {
			out = append(out, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(n)
	return out
}

func isJoin(n *Node) bool {
	switch n.Type {
	case "hash-join", "nested-loop", "merge-join":
		return true
	}
	return false
}

// topJoin finds the outermost join node, the root of the join problem.
func topJoin(n *Node) *Node {
	if n == nil {
		return nil
	}
	if isJoin(n) {
		return n
	}
	for _, c := range n.Children {
		if j := topJoin(c); j != nil {
			return j
		}
	}
	return nil
}

// joinOrderTerm renders a join subtree the way JOIN_ORDER wants it. The
// canonical structure is an outer-deep tree, written flat: JOIN_ORDER(t1
// t2 t3) means t1 is the driving table, joined to t2 and then to t3. A
// subtree that is NOT outer-deep — a join on the inner side — is
// parenthesized, per the README's JOIN_ORDER(t1 (t2 t3)).
func joinOrderTerm(n *Node, names *namer) string {
	parts := joinOrderParts(n, names)
	return strings.Join(parts, " ")
}

func joinOrderParts(n *Node, names *namer) []string {
	if n == nil {
		return nil
	}
	if !isJoin(n) {
		// A non-join node contributes whatever relations hang under it,
		// in order; Sort/Hash/Aggregate wrappers are transparent here.
		return names.namesOf(baseRelations(n))
	}
	if len(n.Children) < 2 {
		return names.namesOf(baseRelations(n))
	}
	outer := joinOrderParts(n.Children[0], names)
	innerNode := joinInner(n)
	var inner []string
	if innerNode != nil && isJoin(innerNode) {
		// Inner-side join: a bushy tree, which gets its own parentheses.
		inner = []string{"(" + joinOrderTerm(innerNode, names) + ")"}
	} else {
		inner = names.namesOf(baseRelations(n.Children[1]))
	}
	return append(outer, inner...)
}

// joinInner returns the node that really sits on a join's inner side,
// seeing through the wrappers the executor puts there. pgpa_join.c does
// the same descent: a Hash Join's inner child is always a Hash, a merge
// join's inner is routinely Sort or Incremental Sort, and Material and
// Memoize wrap the inner side of nested loops.
func joinInner(n *Node) *Node {
	if len(n.Children) < 2 {
		return nil
	}
	cur := n.Children[1]
	for cur != nil {
		switch cur.Type {
		case "hash", "sort", "incremental-sort", "materialize", "memoize":
			if len(cur.Children) == 0 {
				return cur
			}
			cur = cur.Children[0]
			continue
		}
		return cur
	}
	return nil
}

// joinStrategy names the join method tag, making the same PLAIN /
// MATERIALIZE / MEMOIZE distinction pgpa_join.c makes from the node
// directly beneath the join on its inner side. Merge joins look past a
// Sort first, since sorting the inner input is orthogonal to whether it
// was also materialized.
func joinStrategy(n *Node) string {
	inner := (*Node)(nil)
	if len(n.Children) >= 2 {
		inner = n.Children[1]
	}
	switch n.Type {
	case "hash-join":
		return "HASH_JOIN"
	case "merge-join":
		for inner != nil && (inner.Type == "sort" || inner.Type == "incremental-sort") {
			if len(inner.Children) == 0 {
				break
			}
			inner = inner.Children[0]
		}
		if inner != nil && inner.Type == "materialize" {
			return "MERGE_JOIN_MATERIALIZE"
		}
		return "MERGE_JOIN_PLAIN"
	case "nested-loop":
		if inner != nil {
			switch inner.Type {
			case "materialize":
				return "NESTED_LOOP_MATERIALIZE"
			case "memoize":
				return "NESTED_LOOP_MEMOIZE"
			}
		}
		return "NESTED_LOOP_PLAIN"
	}
	return ""
}

type strategyLine struct {
	kind string
	args []string
}

// joinMethods emits one advice item per join method used, naming what
// sits on each join's INNER side. Per the README: for an N-table join
// problem there are N-1 join-method items, and the outermost table needs
// none, because a method tag says "this belongs on the inner side of a
// join of this kind".
func joinMethods(root *Node, names *namer) []strategyLine {
	var out []strategyLine
	idx := map[string]int{}
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
		kind := joinStrategy(n)
		if kind == "" {
			return
		}
		innerRels := baseRelations(n.Children[1])
		if len(innerRels) == 0 {
			return
		}
		var arg string
		if inner := joinInner(n); inner != nil && isJoin(inner) {
			// The inner side is itself a join: one grouped target, as in
			// the README's NESTED_LOOP_PLAIN(x (y z)).
			arg = "(" + strings.Join(names.namesOf(innerRels), " ") + ")"
		} else {
			arg = strings.Join(names.namesOf(innerRels), " ")
		}
		if arg == "" {
			return
		}
		if i, ok := idx[kind]; ok {
			out[i].args = append(out[i].args, arg)
			return
		}
		idx[kind] = len(out)
		out = append(out, strategyLine{kind: kind, args: []string{arg}})
	}
	walk(root)
	return out
}

// scanTag maps a parsed node type to 19's scan advice tag. The list is
// deliberately short and matches pgpa_ast.c: most scan types get no
// advice at all, because there is only one way to perform them. A
// subquery is always scanned with a subquery scan, so there is nothing
// to advise.
func scanTag(typ string) string {
	switch typ {
	case "seq-scan":
		return "SEQ_SCAN"
	case "index-scan":
		return "INDEX_SCAN"
	case "index-only-scan":
		return "INDEX_ONLY_SCAN"
	case "bitmap-heap-scan":
		return "BITMAP_HEAP_SCAN"
	case "tid-scan":
		return "TID_SCAN"
	}
	return ""
}

// scanStrategies groups relations by how they are read. Index and
// index-only scans name the index as well as the relation, in
// relation/index pairs — INDEX_SCAN(foo foo_a_idx bar bar_b_idx) — while
// bitmap heap scans take no index, matching the README.
func scanStrategies(root *Node, names *namer) []strategyLine {
	var out []strategyLine
	idx := map[string]int{}
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if isBaseRelation(n) {
			if tag := scanTag(n.Type); tag != "" {
				i, ok := idx[tag]
				if !ok {
					i = len(out)
					idx[tag] = i
					out = append(out, strategyLine{kind: tag})
				}
				out[i].args = append(out[i].args, names.nameOf(n))
				if n.Index != "" && (tag == "INDEX_SCAN" || tag == "INDEX_ONLY_SCAN") {
					out[i].args = append(out[i].args, n.Index)
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// gatherScans reports what runs in parallel. A Gather covering a join
// product is one grouped target — 19 prints GATHER((f d)) — while
// separate Gathers over separate relations print as GATHER(f d).
func gatherScans(root *Node, names *namer) ([]string, string) {
	var args []string
	kind := "GATHER"
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type == "gather" || n.Type == "gather-merge" {
			if n.Type == "gather-merge" {
				kind = "GATHER_MERGE"
			}
			rels := names.namesOf(baseRelations(n))
			switch {
			case len(rels) == 0:
			case len(rels) == 1:
				args = append(args, rels[0])
			default:
				args = append(args, "("+strings.Join(rels, " ")+")")
			}
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return args, kind
}
