// Package explain parses psql's default TEXT-format EXPLAIN output (with
// or without ANALYZE/BUFFERS) into a tree of plan nodes, and renders that
// tree back out in useful shapes.
//
// TEXT format is the point. Machine-readable FORMAT JSON carries strictly
// more information and is far easier to consume, but it is not what
// people have: a plan that reaches a colleague, a ticket, or a mailing
// list is almost always the default text psql printed, pasted verbatim.
// A tool that insists on JSON is a tool nobody can use on the plan
// actually in front of them, so everything here reads the messy real
// thing — psql's box-drawing borders and QUERY PLAN header, a leading
// SET or BEGIN echo, a trailing "(31 rows)", stray indentation.
//
// Two renderings are provided. FormatForWidth reflows a plan so no line
// exceeds a column budget, for fixed-width media. Advice describes the
// plan's STRUCTURE, mirroring the format PostgreSQL 19's pg_plan_advice
// generates — see advice.go for what that buys on servers long
// predating 19.
//
// This package has zero database or process dependencies: it operates
// purely on text that psql already printed.
//
// The parser began as a Go port of the Lisp explain-plan-parser used to
// typeset The Art of PostgreSQL, and keeps that lineage's node-type
// vocabulary.
package explain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Node is one plan-tree node — the Go equivalent of explain-plan-parser.lisp's
// defstruct plan-node. Pointer fields are nil when the corresponding data
// wasn't present in the EXPLAIN output (e.g. no ANALYZE was used).
type Node struct {
	Type         string // "seq-scan", "hash-join", "unknown", ... — see nodeTypeTokens
	Prefix       string // "Parallel" / "Partial" / "Finalize", or ""
	Label        string // display-label override (join variants keep their full text)
	Relation     string // table/index name, scan nodes only
	Alias        string // "" when absent or identical to Relation
	Index        string // index name on "Index Scan using IDX on REL" nodes
	CostStart    *float64
	CostEnd      *float64
	RowsEstimate *int64
	RowsActual   *int64 // nil when ANALYZE wasn't used
	TimeActual   *float64
	Loops        *int64
	// Props are the node's indented property lines in document order.
	// Each carries its raw text plus, where recognised, the JSON-format
	// keys and values EXPLAIN (FORMAT JSON) would have reported for the
	// same line -- see props.go. Prop.Raw is always the exact captured
	// line, so a renderer that prints properties verbatim is unaffected
	// by how much of a line this package understood.
	Props []Prop
	// SubplanName is the name of the subtree this node roots, when it is
	// one: "CTE x", "InitPlan 1 (returns $0)", "SubPlan 2".
	//
	// In TEXT this arrives as a bare line above the node's own "->" line,
	// which used to read as a property of the PARENT -- and was the
	// single largest class of unrecognised property line in PostgreSQL's
	// own regression corpus. It is not a property at all: explain.c
	// prints the same plan_name string here as
	// appendStringInfo("%s\n", plan_name) and in every other format as
	// ExplainPropertyText("Subplan Name", plan_name) on this node.
	// Modelling it as a field puts the TEXT and JSON readings back on
	// the same footing.
	SubplanName string
	Children    []*Node
}

// Plan is the Go equivalent of explain-plan-parser.lisp's defstruct plan.
type Plan struct {
	Root          *Node
	PlanningTime  *float64 // ms
	ExecutionTime *float64 // ms

	// GeneratedAdvice and SuppliedAdvice are the plan-advice blocks
	// contrib/pg_plan_advice prints after the tree:
	//
	//	 Generated Plan Advice:
	//	   JOIN_ORDER(results races drivers)
	//	   SEQ_SCAN(alt_t1 alt_t2@exists_to_any_1)
	//
	// They decorate the whole plan rather than any node in it, so they
	// hang off Plan as a sibling of Root rather than being forced into
	// the tree. Before this they were collected as property lines of
	// whatever node happened to be on the stack when the block started,
	// which put "NO_GATHER(...)" on a Seq Scan -- and made them the
	// largest remaining class of unrecognised property.
	//
	// Supplied advice is what the caller asked for and can carry a
	// per-item annotation ("/* matched */"); generated advice is what
	// the planner would emit to reproduce the plan it chose.
	GeneratedAdvice []AdviceItem
	SuppliedAdvice  []AdviceItem
}

// AdviceItem is one line of a plan-advice block.
type AdviceItem struct {
	AdviceLine
	// Raw is the line as printed, annotation included.
	Raw string
	// Annotation is the text of a trailing /* ... */ comment, without
	// the delimiters -- "matched" on supplied advice the planner was
	// able to honour. Empty when there is none.
	Annotation string
}

// HasPlan reports whether text looks like it contains a captured EXPLAIN
// plan — the same filesystem-inspection heuristic query-metadata.lisp's
// detect-explain-diagram used ("QUERY PLAN" header or a "(cost=" node
// line), so callers can decide whether to invoke Parse at all.
func HasPlan(text string) bool {
	return strings.Contains(text, "QUERY PLAN") || strings.Contains(text, "(cost=")
}

// planLineRE recognizes a plan node line has the general
// "(cost=..)" or "(cost=.. rows=.. width=..)" parenthetical.
var costScanner = regexp.MustCompile(`\(cost=([0-9.]+)\.\.([0-9.]+) rows=(\d+) width=(\d+)\)`)
var actualScanner = regexp.MustCompile(`\(actual time=[0-9.]+\.\.([0-9.]+) rows=(\d+) loops=(\d+)\)`)
var planningTimeRE = regexp.MustCompile(`^Planning Time:\s*([0-9.]+)\s*ms`)
var executionTimeRE = regexp.MustCompile(`^Execution Time:\s*([0-9.]+)\s*ms`)
var arrowRE = regexp.MustCompile(`^(\s*)->\s+`)
var separatorRE = regexp.MustCompile(`^\s*[═=─-]{5,}\s*$`)
var rowsFooterRE = regexp.MustCompile(`^\(\d+ rows?\)`)

// subplanHeaderRE matches the bare name line EXPLAIN prints above a
// subplan's own node line. explain.c builds exactly these three shapes,
// as psprintf("CTE %s"), psprintf("InitPlan %s") and
// psprintf("SubPlan %s").
var subplanHeaderRE = regexp.MustCompile(`^(CTE|InitPlan|SubPlan) `)

// adviceAnnotationRE matches the trailing "/* matched */" pg_plan_advice
// puts on a supplied advice item it was able to honour.
var adviceAnnotationRE = regexp.MustCompile(`\s*/\*\s*(.*?)\s*\*/\s*$`)

// unbalancedParens reports whether s has more "(" than ")", meaning an
// advice item is still incomplete.
func unbalancedParens(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		}
	}
	return depth > 0
}

// parseAdviceItem parses one line of a plan-advice block.
func parseAdviceItem(line string) (AdviceItem, bool) {
	body, annotation := line, ""
	if m := adviceAnnotationRE.FindStringSubmatch(line); m != nil {
		annotation = m[1]
		body = strings.TrimSpace(adviceAnnotationRE.ReplaceAllString(line, ""))
	}
	parsed, ok := ParseAdviceLine(body)
	if !ok {
		return AdviceItem{}, false
	}
	return AdviceItem{AdviceLine: parsed, Raw: line, Annotation: annotation}, true
}

// Parse parses psql's default TEXT-format EXPLAIN output (out, the full
// captured stdout — may include preceding SET/other statement echoes,
// which are skipped) into a Plan.
func Parse(out string) (*Plan, error) {
	lines := extractPlanLines(out)
	if len(lines) == 0 {
		return nil, fmt.Errorf("explainplan: no plan lines found")
	}

	plan := &Plan{}
	type frame struct {
		depth int
		node  *Node
	}
	var stack []frame

	// A subplan header names the node on the NEXT "->" line, so it is
	// held here until that node is built.
	var pendingSubplanName string

	// Set while reading a plan-advice block; points at the slice its
	// items belong to. adviceBuf accumulates an item psql wrapped over
	// several output lines.
	var adviceTarget *[]AdviceItem
	var adviceBuf string

	for _, line := range lines {
		if m := planningTimeRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			plan.PlanningTime = &v
			continue
		}
		if m := executionTimeRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			plan.ExecutionTime = &v
			continue
		}

		if m := arrowRE.FindStringSubmatch(line); m != nil {
			arrowCol := len(m[1])
			depth := (arrowCol + 4) / 6
			text := strings.TrimSpace(line[len(m[0]):])
			node := parseNodeLine(text)

			for len(stack) > 0 && stack[len(stack)-1].depth >= depth {
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 {
				// Root already exists (depth-0 line came first) — attach
				// as a child of the deepest still-open ancestor. In
				// practice depth 1 always follows the depth-0 root line,
				// so this only fires if the root is somehow missing.
				plan.Root = node
			} else {
				parent := stack[len(stack)-1].node
				parent.Children = append(parent.Children, node)
			}
			if pendingSubplanName != "" {
				node.SubplanName = pendingSubplanName
				pendingSubplanName = ""
			}
			stack = append(stack, frame{depth: depth, node: node})
			continue
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if len(stack) == 0 && plan.Root == nil {
			// The very first non-arrow line is the root node.
			plan.Root = parseNodeLine(trimmed)
			stack = append(stack, frame{depth: 0, node: plan.Root})
			continue
		}

		switch trimmed {
		case "Generated Plan Advice:":
			adviceTarget = &plan.GeneratedAdvice
			continue
		case "Supplied Plan Advice:":
			adviceTarget = &plan.SuppliedAdvice
			continue
		}
		if adviceTarget != nil {
			// An advice item can be wider than psql's output column and
			// arrive split across lines, so accumulate until the
			// parentheses balance rather than judging each line alone.
			// A long INDEX_SCAN over a partitioned table routinely wraps
			// three ways.
			if adviceBuf == "" {
				adviceBuf = trimmed
			} else {
				adviceBuf += " " + trimmed
			}
			if item, ok := parseAdviceItem(adviceBuf); ok {
				*adviceTarget = append(*adviceTarget, item)
				adviceBuf = ""
				continue
			}
			if unbalancedParens(adviceBuf) {
				continue // more of this item is still to come
			}
			// Balanced and still not advice: the block is over. Put the
			// line back on the ordinary path.
			adviceTarget = nil
			trimmed = adviceBuf
			adviceBuf = ""
		}

		if subplanHeaderRE.MatchString(trimmed) {
			pendingSubplanName = trimmed
			continue
		}

		if len(stack) > 0 {
			stack[len(stack)-1].node.Props = append(stack[len(stack)-1].node.Props, ParseProp(trimmed))
		}
	}

	if plan.Root == nil {
		return nil, fmt.Errorf("explainplan: failed to find a root plan node")
	}
	return plan, nil
}

// extractPlanLines finds the plan inside whatever psql printed: it skips
// any leading statement echoes and the "QUERY PLAN" header, strips
// exactly one leading space from every collected line, keeps the
// Planning Time / Execution Time lines, and stops at the "(N rows)"
// footer.
//
// Two entry rules, because there are two kinds of plan text. Normally the
// first "(cost=" line begins the plan, which skips echoes and the header
// without having to recognize either. But EXPLAIN (COSTS OFF) — how the
// PostgreSQL test suite, most documentation, and PLAN_ADVICE examples
// print plans — has no "(cost=" anywhere, and looking for one finds
// nothing at all. So when the text contains no costs, fall back to
// starting after the QUERY PLAN header, or at the first non-empty line
// when there is no header either.
//
// The costed path is kept exactly as it was: it is what every existing
// caller feeds this, and it needs to behave identically.
func extractPlanLines(out string) []string {
	costed := strings.Contains(out, "(cost=")

	var result []string
	collecting := false
	inPlanningBlock := false
	sawHeader := false
	hasHeader := strings.Contains(out, "QUERY PLAN")

	for _, raw := range strings.Split(out, "\n") {
		line := raw
		if strings.HasPrefix(line, " ") {
			line = line[1:]
		}
		trimmed := strings.TrimSpace(line)

		if !collecting {
			switch {
			case costed:
				if !strings.Contains(line, "(cost=") {
					continue
				}
				collecting = true
			case hasHeader:
				// Costs are off: the header is the only reliable marker.
				if !sawHeader {
					if trimmed == "QUERY PLAN" {
						sawHeader = true
					}
					continue
				}
				if trimmed == "" || separatorRE.MatchString(line) {
					continue
				}
				collecting = true
			default:
				if trimmed == "" {
					continue
				}
				collecting = true
			}
		}

		if rowsFooterRE.MatchString(trimmed) {
			break
		}
		// EXPLAIN (BUFFERS) prints a "Planning:" block, with its own
		// indented Buffers/I/O Timings lines, BETWEEN the tree and the
		// Planning Time / Execution Time lines. Stopping at "Planning:"
		// (which this did) therefore threw both timings away on every
		// buffered plan — invisible when the tree was all anyone wanted,
		// but the timings are half of what a plan comparison reports.
		// Skip the block's contents, keep scanning for the timings.
		if trimmed == "Planning:" {
			inPlanningBlock = true
			continue
		}
		if planningTimeRE.MatchString(trimmed) || executionTimeRE.MatchString(trimmed) {
			inPlanningBlock = false
			result = append(result, line)
			continue
		}
		if inPlanningBlock {
			continue
		}
		if separatorRE.MatchString(line) {
			continue
		}
		result = append(result, line)
	}
	return result
}

// parseNodeLine parses one plan-node line's body (after the "-> " arrow,
// or the bare root line) into a Node — matching type/prefix/label
// classification, relation+alias extraction, and cost/actual field
// extraction, per explain-plan-parser.lisp.
func parseNodeLine(text string) *Node {
	node := &Node{}

	rest := text
	if m := costScanner.FindStringSubmatch(rest); m != nil {
		start, _ := strconv.ParseFloat(m[1], 64)
		end, _ := strconv.ParseFloat(m[2], 64)
		rows, _ := strconv.ParseInt(m[3], 10, 64)
		node.CostStart = &start
		node.CostEnd = &end
		node.RowsEstimate = &rows
		rest = rest[:strings.Index(rest, m[0])]
	}
	if m := actualScanner.FindStringSubmatch(text); m != nil {
		t, _ := strconv.ParseFloat(m[1], 64)
		rows, _ := strconv.ParseInt(m[2], 10, 64)
		loops, _ := strconv.ParseInt(m[3], 10, 64)
		node.TimeActual = &t
		node.RowsActual = &rows
		node.Loops = &loops
	}

	rest = strings.TrimSpace(rest)
	typ, prefix, label, relAndAlias := matchNodeType(rest)
	node.Type = typ
	node.Prefix = prefix
	node.Label = label
	if relAndAlias != "" {
		node.Relation, node.Alias = parseRelationAndAlias(relAndAlias)
		node.Index = parseIndexName(relAndAlias)
	}
	return node
}

// parseIndexName pulls the index out of "using INDEX on RELATION", the
// shape index and index-only scans print. PostgreSQL 19's plan advice
// names both the relation and the index it was reached through
// (INDEX_SCAN(foo foo_a_idx)), so unlike the original book-typesetting
// parser — which only ever needed the relation and threw this away — the
// index name has to survive parsing.
func parseIndexName(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "using ") {
		return ""
	}
	s = s[len("using "):]
	idx := strings.Index(s, " on ")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(s[:idx])
}

// parseRelationAndAlias handles the two shapes psql prints after a scan
// node's type token: "on RELATION [ALIAS]" (heap scan) and "using INDEX
// on RELATION [ALIAS]" (index scan — the index name itself is discarded,
// matching the Lisp parser, which never stores it).
func parseRelationAndAlias(s string) (relation, alias string) {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, " on "); idx >= 0 {
		s = s[idx+4:]
	} else if !strings.HasPrefix(s, "on ") {
		return "", ""
	} else {
		s = s[3:]
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", ""
	}
	relation = fields[0]
	if len(fields) > 1 && fields[1] != relation {
		alias = fields[1]
	}
	return relation, alias
}
