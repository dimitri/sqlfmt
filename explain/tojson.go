package explain

import (
	"encoding/json"
	"strconv"
)

// ToJSON renders a Plan as EXPLAIN (FORMAT JSON) output.
//
// It emits only what the plan actually carries. A plan parsed from TEXT
// has been through a format that prints strictly less than JSON does --
// no Output list without VERBOSE, no buffer counters without BUFFERS, no
// "Parent Relationship", no "Plan Width" -- and inventing those keys so
// the result looks like a real server's would make it a forgery rather
// than a conversion. The PostgreSQL docs say the non-text formats
// "contain the same information as the text format", which is true in
// the direction TEXT -> JSON only for what TEXT actually printed.
//
// So: round-tripping JSON -> Plan -> JSON is lossy (it keeps what TEXT
// would have shown), while TEXT -> Plan -> JSON -> Plan -> TEXT is
// faithful. The second is the useful one -- it is what lets a pasted
// plan be handed to a tool that only speaks JSON.
func ToJSON(p *Plan) ([]byte, error) {
	if p == nil || p.Root == nil {
		return []byte("[]"), nil
	}
	top := map[string]any{"Plan": nodeToJSON(p.Root)}
	if p.PlanningTime != nil {
		top["Planning Time"] = *p.PlanningTime
	}
	if p.ExecutionTime != nil {
		top["Execution Time"] = *p.ExecutionTime
	}
	return json.MarshalIndent([]any{top}, "", "  ")
}

func nodeToJSON(n *Node) map[string]any {
	m := map[string]any{"Node Type": NodeLabel(n)}
	if n.Prefix != "" {
		if n.Prefix == "Parallel" {
			m["Parallel Aware"] = true
		} else {
			m["Partial Mode"] = n.Prefix
		}
	}
	if n.SubplanName != "" {
		m["Subplan Name"] = n.SubplanName
	}
	if n.Relation != "" {
		m["Relation Name"] = n.Relation
	}
	if n.Alias != "" {
		m["Alias"] = n.Alias
	}
	if n.Index != "" {
		m["Index Name"] = n.Index
	}
	if n.CostStart != nil {
		m["Startup Cost"] = *n.CostStart
	}
	if n.CostEnd != nil {
		m["Total Cost"] = *n.CostEnd
	}
	if n.RowsEstimate != nil {
		m["Plan Rows"] = *n.RowsEstimate
	}
	if n.RowsActual != nil {
		m["Actual Rows"] = *n.RowsActual
	}
	if n.TimeActual != nil {
		m["Actual Total Time"] = *n.TimeActual
	}
	if n.Loops != nil {
		m["Actual Loops"] = *n.Loops
	}

	// Every property line contributes its JSON-named fields. This is
	// where the label table pays for itself: a composite TEXT line like
	// "Buckets: 1024  Batches: 1  Memory Usage: 44kB" fans back out into
	// the five separate keys a server would have written, with no
	// special-casing here at all.
	for _, p := range n.Props {
		for k, v := range p.Fields {
			if spec, ok := LookupPropSpec(k); ok {
				m[k] = typedJSONValue(spec, v)
				continue
			}
			m[k] = numberOrString(v)
		}
	}

	if len(n.Children) > 0 {
		kids := make([]any, 0, len(n.Children))
		for _, c := range n.Children {
			kids = append(kids, nodeToJSON(c))
		}
		m["Plans"] = kids
	}
	return m
}

func typedJSONValue(spec PropSpec, v string) any {
	switch spec.Kind {
	case PropInteger:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	case PropFloat:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	case PropBool:
		return v == "true"
	}
	return v
}

// numberOrString keeps an unrecognised property's value as a number when
// it plainly is one, and a string otherwise -- the best that can be done
// for a key this package has no spec for (an extension's, say).
func numberOrString(v string) any {
	if i, err := strconv.ParseInt(v, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	return v
}
