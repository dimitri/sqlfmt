package explain

// The property vocabulary EXPLAIN emits, extracted from PostgreSQL
// (REL_19_BETA1) src/backend/commands/explain.c + explain_dr.c and from
// Citus's src/backend/distributed/planner/multi_explain.c.
//
// The reason one table can serve both the TEXT parser and the JSON
// reader is ExplainProperty() in explain_format.c:
//
//	case EXPLAIN_FORMAT_TEXT:
//	    appendStringInfo(es->str, "%s: %s\n", qlabel, value);
//	case EXPLAIN_FORMAT_JSON:
//	    escape_json(es->str, qlabel); ... value
//
// The label printed before the colon in TEXT is byte-for-byte the key
// written in JSON. So every property routed through ExplainProperty* --
// the large majority of them -- needs no translation at all, and a
// mismatch between what this package calls a field in one format and in
// the other is impossible by construction rather than by discipline.
//
// The exceptions are the dozen-odd lines assembled by hand with
// appendStringInfo in the TEXT branch, which pack several values onto
// one line using words that are NOT the JSON keys. Those live in
// props.go's composite parsers, and this table is what they map onto.

// PropKind is how a property's value should be read.
type PropKind int

const (
	// PropText is a plain string: "Sort Method", "Node", "Cache Mode".
	PropText PropKind = iota
	// PropExpression is a deparsed SQL expression -- "Filter",
	// "Index Cond", "Hash Cond". Distinguished from PropText because it
	// is the only kind worth handing to a SQL formatter, and because it
	// is what prose most often points at.
	PropExpression
	// PropInteger and PropFloat are numeric. EXPLAIN prints Actual Rows
	// as a float since PG18 (fractional rows under loops>1), so kinds
	// follow the source's own ExplainPropertyInteger/Float choice rather
	// than what a value happens to look like.
	PropInteger
	PropFloat
	// PropBool is "true"/"false" in JSON; in TEXT the property is
	// usually only printed when true.
	PropBool
	// PropList is a comma-separated list in TEXT, a JSON array in JSON:
	// "Output", "Sort Key", "Group Key", "Presorted Key".
	PropList
)

// PropSpec describes one property label.
//
// Label is both the TEXT label and the JSON key -- see the package
// comment above for why those are the same string. Unit is the suffix
// ExplainProperty appends in TEXT ("kB", "ms") and omits in JSON, so a
// value parsed from TEXT has to have it stripped to equal the JSON one.
type PropSpec struct {
	Label string
	Kind  PropKind
	Unit  string
}

// propSpecs is keyed by label. Deliberately not exhaustive over every
// label EXPLAIN can emit -- it covers what the TEXT format actually
// prints as its own indented property lines, which is the input this
// package parses. Node-level scalars that only ever appear inside the
// node line itself (Startup Cost, Plan Rows, Actual Total Time, ...) are
// parsed by parse.go's own scanners and modelled as Node fields, so they
// are not repeated here.
var propSpecs = buildPropSpecs()

func buildPropSpecs() map[string]PropSpec {
	m := map[string]PropSpec{}
	add := func(kind PropKind, unit string, labels ...string) {
		for _, l := range labels {
			m[l] = PropSpec{Label: l, Kind: kind, Unit: unit}
		}
	}

	// Deparsed expressions -- show_qual / show_scan_qual /
	// show_upper_qual / show_expression / show_sort_group_keys.
	add(PropExpression, "",
		"Filter", "Index Cond", "Recheck Cond", "Hash Cond", "Merge Cond",
		"TID Cond", "Join Filter", "One-Time Filter", "Conflict Filter",
		"Order By", "Run Condition", "Cache Key", "Hash Key", "Hash Keys",
		"Function Call", "Table Function Call", "Index Recheck",
		"Filter Removed by Join Filter",
	)

	// Key lists -- comma-separated in TEXT, arrays in JSON.
	add(PropList, "",
		"Output", "Sort Key", "Group Key", "Presorted Key", "Hash Key",
		"Grouping Sets", "Group Keys", "Sort Methods Used",
		"Conflict Arbiter Indexes", "Sampling Parameters",
	)

	// show_instrumentation_count counters.
	add(PropFloat, "",
		"Rows Removed by Filter", "Rows Removed by Join Filter",
		"Rows Removed by Index Recheck", "Rows Removed by Conflict Filter",
		"Heap Fetches", "Conflicting Tuples", "Tuples Inserted",
		"Tuples Updated", "Tuples Deleted", "Tuples Skipped",
	)

	add(PropInteger, "",
		"Workers Planned", "Workers Launched", "Worker Number",
		"Subplans Removed", "Index Searches", "Query Identifier",
		"Exact Heap Blocks", "Lossy Heap Blocks",
		"Planned Partitions", "Group Count",
	)

	add(PropInteger, "kB",
		"Sort Space Used", "Peak Memory Usage", "Memory Used",
		"Memory Allocated", "Maximum Storage", "Disk Usage",
		"Average Sort Space Used", "Peak Sort Space Used",
	)

	add(PropText, "",
		"Sort Method", "Sort Space Type", "Storage", "Cache Mode",
		"Scan Direction", "Index Name", "Relation Name", "Alias", "Schema",
		"Join Type", "Partial Mode", "Parent Relationship", "Subplan Name",
		"Command", "Strategy", "Operation", "Node Type", "Settings",
		"Sampling Method", "Repeatable Seed", "Custom Plan Provider",
		"Constraint Name", "Trigger Name", "CTE Name", "Function Name",
		"Table Function Name", "Tuplestore Name", "Window",
	)

	// Pre-PostgreSQL-18 unscoped I/O timing keys, kept so captures from
	// those servers still read.
	add(PropFloat, "", "I/O Read Time", "I/O Write Time")

	add(PropBool, "",
		"Parallel Aware", "Async Capable", "Inner Unique", "Single Copy",
		"Disabled", "Inlining", "Optimization", "Expressions", "Deforming",
	)

	// Citus (multi_explain.c). Same ExplainProperty* path, so the same
	// label-is-the-key rule holds; listed separately only because a
	// reader looking for why "Task Count" is understood should find it
	// attributed.
	add(PropInteger, "", "Task Count", "Map Task Count", "Merge Task Count")
	add(PropText, "",
		"Tasks Shown", "Node", "Query", "Error",
		"Result destination", "INSERT/SELECT method", "MERGE INTO ... method",
	)
	add(PropFloat, "ms", "Subplan Duration")
	add(PropInteger, "bytes",
		"Intermediate Data Size", "Tuple data received from nodes",
		"Tuple data received from node",
	)

	return m
}

// LookupPropSpec returns the spec for a property label, and whether it is
// one this package knows. An unknown label is not an error anywhere: see
// ParseProp.
func LookupPropSpec(label string) (PropSpec, bool) {
	s, ok := propSpecs[label]
	return s, ok
}
