package explain

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ParseJSON reads EXPLAIN (FORMAT JSON) output into the same *Plan that
// Parse builds from TEXT.
//
// Why this direction first, and why it matters that it is the same type:
// a caller that has a live server connection has the JSON, a caller that
// has a pasted plan has the TEXT, and until now those were two worlds --
// this repo's own consumers ran EXPLAIN twice, once per format, because
// nothing could turn one into the other. One *Plan means one renderer,
// one advice implementation, one diff.
//
// The node classification deliberately goes through matchNodeType, the
// same function the TEXT parser uses, rather than a second switch over
// "Node Type": reconstructing the label TEXT would have printed and
// handing it to the existing classifier is what makes "Nested Loop Left
// Join" mean the identical thing on both paths. TestJSONMatchesText
// pins that.
//
// Props are synthesised as the lines TEXT would have printed, so a plan
// read from JSON renders identically to the same plan read from TEXT --
// including the composite lines, which are reassembled from their
// several JSON keys (Shared Hit Blocks + Shared Read Blocks + ... ->
// "Buffers: shared hit=N read=N"). Keys that TEXT never prints as a
// property line are skipped rather than invented into one.
func ParseJSON(data []byte) (*Plan, error) {
	var top []map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		// A single object rather than the array psql returns is common
		// enough from tools and copy-paste to be worth accepting.
		var one map[string]any
		if err2 := json.Unmarshal(data, &one); err2 != nil {
			return nil, fmt.Errorf("explain: not EXPLAIN (FORMAT JSON) output: %w", err)
		}
		top = []map[string]any{one}
	}
	if len(top) == 0 {
		return nil, fmt.Errorf("explain: empty EXPLAIN (FORMAT JSON) output")
	}

	root, ok := top[0]["Plan"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("explain: EXPLAIN (FORMAT JSON) output has no \"Plan\" object")
	}

	plan := &Plan{Root: jsonNode(root)}
	if v, ok := jsonFloat(top[0], "Planning Time"); ok {
		plan.PlanningTime = &v
	}
	if v, ok := jsonFloat(top[0], "Execution Time"); ok {
		plan.ExecutionTime = &v
	}
	return plan, nil
}

func jsonNode(m map[string]any) *Node {
	n := &Node{}

	typ, prefix, label, _ := matchNodeType(jsonNodeLabel(m))
	n.Type, n.Prefix, n.Label = typ, prefix, label

	n.Relation, _ = m["Relation Name"].(string)
	n.Index, _ = m["Index Name"].(string)
	if alias, _ := m["Alias"].(string); alias != "" && alias != n.Relation {
		n.Alias = alias
	}

	if v, ok := jsonFloat(m, "Startup Cost"); ok {
		n.CostStart = &v
	}
	if v, ok := jsonFloat(m, "Total Cost"); ok {
		n.CostEnd = &v
	}
	if v, ok := jsonInt(m, "Plan Rows"); ok {
		n.RowsEstimate = &v
	}
	if v, ok := jsonInt(m, "Actual Rows"); ok {
		n.RowsActual = &v
	}
	if v, ok := jsonFloat(m, "Actual Total Time"); ok {
		n.TimeActual = &v
	}
	if v, ok := jsonInt(m, "Actual Loops"); ok {
		n.Loops = &v
	}

	n.Props = jsonProps(m)

	if kids, ok := m["Plans"].([]any); ok {
		for _, k := range kids {
			if km, ok := k.(map[string]any); ok {
				n.Children = append(n.Children, jsonNode(km))
			}
		}
	}
	return n
}

// jsonNodeLabel rebuilds the node label TEXT would have printed, so that
// matchNodeType classifies a JSON node exactly as it classifies a TEXT
// one.
//
// JSON splits what TEXT joins: "Nested Loop Left Join" arrives as
// {"Node Type": "Nested Loop", "Join Type": "Left"}, and "Parallel Seq
// Scan" as {"Node Type": "Seq Scan", "Parallel Aware": true}. An Inner
// join is the unmarked case and TEXT prints no join word for it.
func jsonNodeLabel(m map[string]any) string {
	nodeType, _ := m["Node Type"].(string)

	if jt, _ := m["Join Type"].(string); jt != "" && jt != "Inner" {
		switch nodeType {
		case "Nested Loop":
			nodeType = "Nested Loop " + jt + " Join"
		case "Hash Join":
			nodeType = "Hash " + jt + " Join"
		case "Merge Join":
			nodeType = "Merge " + jt + " Join"
		}
	}

	var prefix string
	if pm, _ := m["Partial Mode"].(string); pm == "Partial" || pm == "Finalize" {
		prefix = pm + " "
	}
	if pa, _ := m["Parallel Aware"].(bool); pa {
		prefix += "Parallel "
	}
	return prefix + nodeType
}

// jsonPropOrder is the order EXPLAIN's TEXT branch prints property lines
// in, as far as it matters for reproducing a capture: the deparsed
// conditions first, then the counters they explain, then the per-node
// execution detail, then the resource usage blocks last. Only keys
// listed here are turned into property lines -- every other JSON key is
// either already a Node field or something TEXT does not print as its
// own line ("Parallel Aware", "Node Type", "Plans", ...).
var jsonPropOrder = []string{
	"Output",
	"Index Cond", "Recheck Cond", "TID Cond", "Merge Cond", "Hash Cond",
	"Join Filter", "One-Time Filter", "Filter", "Run Condition",
	"Rows Removed by Index Recheck", "Rows Removed by Join Filter", "Rows Removed by Filter",
	"Heap Fetches", "Index Searches", "Order By",
	"Sort Key", "Presorted Key", "Group Key", "Hash Key", "Cache Key", "Cache Mode",
	"Subplans Removed", "Workers Planned", "Workers Launched",
	"Function Call", "Table Function Call",
	// Citus.
	"Task Count", "Tasks Shown", "Node",
}

// jsonComposites reassembles the TEXT lines PostgreSQL builds by hand,
// from the several JSON keys each of them spans. Order matters the same
// way jsonPropOrder's does; these come after it.
var jsonComposites = []func(map[string]any) (string, bool){
	compHeapBlocks, compHashInfo, compHashAggInfo, compSortMethod,
	compMemoize, compStorage, compBuffers, compIOTimings, compWAL, compMemory,
}

func jsonProps(m map[string]any) []Prop {
	var props []Prop
	emit := func(line string) { props = append(props, ParseProp(line)) }

	for _, key := range jsonPropOrder {
		v, ok := m[key]
		if !ok {
			continue
		}
		text := jsonScalarText(v)
		if text == "" {
			continue
		}
		spec, known := LookupPropSpec(key)
		if known && spec.Unit != "" {
			text += spec.Unit
		}
		emit(key + ": " + text)
	}

	for _, comp := range jsonComposites {
		if line, ok := comp(m); ok {
			emit(line)
		}
	}
	return props
}

// jsonScalarText renders a JSON value the way TEXT would print it: a
// list joined with ", ", a number without trailing zeros, a bool only
// when true (TEXT omits a false flag rather than printing "false").
func jsonScalarText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if !t {
			return ""
		}
		return "true"
	case float64:
		return formatJSONNumber(t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			if s := jsonScalarText(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

func formatJSONNumber(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// counterLine assembles one scope-grouped composite line: for each scope,
// the counters present, in order, as "scope k=v k=v", joined with ", ".
//
// Zero counters are skipped, because that is what the server does --
// show_buffer_usage guards every single counter with `if (usage->
// shared_blks_hit > 0)` and the whole scope group with a has_shared /
// has_local / has_temp precondition. JSON has no such filter and reports
// every counter including the zeros, so reproducing the TEXT line means
// dropping them here or every plan would gain a "read=0 dirtied=0
// written=0" its capture never had.
func counterLine(m map[string]any, label string, scopes []string, counters [][2]string, key func(scope, counter string) string) (string, bool) {
	var groups []string
	for _, scope := range scopes {
		var parts []string
		for _, c := range counters {
			if v, ok := jsonNumberText(m, key(scope, c[0])); ok && v != "0" {
				parts = append(parts, c[1]+"="+v)
			}
		}
		if len(parts) > 0 {
			groups = append(groups, strings.TrimSpace(strings.ToLower(scope)+" "+strings.Join(parts, " ")))
		}
	}
	if len(groups) == 0 {
		return "", false
	}
	return label + ": " + strings.Join(groups, ", "), true
}

func compBuffers(m map[string]any) (string, bool) {
	return counterLine(m, "Buffers",
		[]string{"Shared", "Local", "Temp"},
		[][2]string{{"Hit", "hit"}, {"Read", "read"}, {"Dirtied", "dirtied"}, {"Written", "written"}},
		func(scope, c string) string { return scope + " " + c + " Blocks" })
}

// compIOTimings also accepts the pre-PostgreSQL-18 spelling. Before the
// per-scope split, a plan reported one unscoped pair, "I/O Read Time" /
// "I/O Write Time", which TEXT printed under the shared scope; captures
// from those servers are still in circulation and still worth reading.
func compIOTimings(m map[string]any) (string, bool) {
	if line, ok := counterLine(m, "I/O Timings",
		[]string{"Shared", "Local", "Temp"},
		[][2]string{{"Read", "read"}, {"Write", "write"}},
		func(scope, c string) string { return scope + " I/O " + c + " Time" }); ok {
		return line, true
	}
	return counterLine(m, "I/O Timings", []string{"Shared"},
		[][2]string{{"Read", "read"}, {"Write", "write"}},
		func(_, c string) string { return "I/O " + c + " Time" })
}

func compWAL(m map[string]any) (string, bool) {
	return counterLine(m, "WAL", []string{""},
		[][2]string{{"Records", "records"}, {"FPI", "fpi"}, {"Bytes", "bytes"}, {"Buffers Full", "full"}},
		func(_, c string) string { return "WAL " + c })
}

func compHeapBlocks(m map[string]any) (string, bool) {
	return counterLine(m, "Heap Blocks", []string{""},
		[][2]string{{"Exact", "exact"}, {"Lossy", "lossy"}},
		func(_, c string) string { return c + " Heap Blocks" })
}

func compPrefetch(m map[string]any) (string, bool) {
	return counterLine(m, "Prefetch", []string{""},
		[][2]string{{"Average Prefetch Distance", "avg"}, {"Max Prefetch Distance", "max"}, {"Prefetch Capacity", "capacity"}},
		func(_, c string) string { return c })
}

func compHashInfo(m map[string]any) (string, bool) {
	buckets, ok := jsonNumberText(m, "Hash Buckets")
	if !ok {
		return "", false
	}
	seg := "Buckets: " + buckets
	if orig, ok := jsonNumberText(m, "Original Hash Buckets"); ok && orig != buckets {
		seg += " (originally " + orig + ")"
	}
	if batches, ok := jsonNumberText(m, "Hash Batches"); ok {
		seg += "  Batches: " + batches
		if orig, ok := jsonNumberText(m, "Original Hash Batches"); ok && orig != batches {
			seg += " (originally " + orig + ")"
		}
	}
	if mem, ok := jsonNumberText(m, "Peak Memory Usage"); ok {
		seg += "  Memory Usage: " + mem + "kB"
	}
	return seg, true
}

func compHashAggInfo(m map[string]any) (string, bool) {
	batches, ok := jsonNumberText(m, "HashAgg Batches")
	if !ok {
		return "", false
	}
	seg := "Batches: " + batches
	if mem, ok := jsonNumberText(m, "Peak Memory Usage"); ok {
		seg += "  Memory Usage: " + mem + "kB"
	}
	if disk, ok := jsonNumberText(m, "Disk Usage"); ok {
		seg += "  Disk Usage: " + disk + "kB"
	}
	return seg, true
}

func compSortMethod(m map[string]any) (string, bool) {
	method, _ := m["Sort Method"].(string)
	if method == "" {
		return "", false
	}
	seg := "Sort Method: " + method
	spaceType, _ := m["Sort Space Type"].(string)
	if used, ok := jsonNumberText(m, "Sort Space Used"); ok && spaceType != "" {
		seg += "  " + spaceType + ": " + used + "kB"
	}
	return seg, true
}

func compMemoize(m map[string]any) (string, bool) {
	hits, ok := jsonNumberText(m, "Cache Hits")
	if !ok {
		return "", false
	}
	seg := "Hits: " + hits
	for _, p := range [][2]string{
		{"Cache Misses", "Misses"}, {"Cache Evictions", "Evictions"}, {"Cache Overflows", "Overflows"},
	} {
		if v, ok := jsonNumberText(m, p[0]); ok {
			seg += "  " + p[1] + ": " + v
		}
	}
	if v, ok := jsonNumberText(m, "Peak Memory Usage"); ok {
		seg += "  Memory Usage: " + v + "kB"
	}
	return seg, true
}

func compStorage(m map[string]any) (string, bool) {
	storage, _ := m["Storage"].(string)
	if storage == "" {
		return "", false
	}
	seg := "Storage: " + storage
	if v, ok := jsonNumberText(m, "Maximum Storage"); ok {
		seg += "  Maximum Storage: " + v + "kB"
	}
	return seg, true
}

func compMemory(m map[string]any) (string, bool) {
	used, ok := jsonNumberText(m, "Memory Used")
	if !ok {
		return "", false
	}
	seg := "Memory: used=" + used + "kB"
	if v, ok := jsonNumberText(m, "Memory Allocated"); ok {
		seg += "  allocated=" + v + "kB"
	}
	return seg, true
}

func jsonNumberText(m map[string]any, key string) (string, bool) {
	v, ok := m[key].(float64)
	if !ok {
		return "", false
	}
	return formatJSONNumber(v), true
}

func jsonFloat(m map[string]any, key string) (float64, bool) {
	v, ok := m[key].(float64)
	return v, ok
}

func jsonInt(m map[string]any, key string) (int64, bool) {
	v, ok := m[key].(float64)
	if !ok {
		return 0, false
	}
	return int64(v), true
}
