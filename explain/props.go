package explain

import (
	"strings"
)

// Prop is one parsed property line from a plan node -- the indented
// "Filter: ...", "Buffers: ...", "Sort Method: ..." lines beneath a node
// line in psql's TEXT output.
//
// Raw is always populated and always the exact trimmed line as captured.
// Everything else is best-effort enrichment on top of it. That ordering
// is deliberate and load-bearing in two ways:
//
//   - The TEXT renderers measure and print property lines verbatim, so a
//     parse that changed or dropped a line would change typeset output.
//     Raw is what they use.
//   - EXPLAIN is extensible. This corpus alone carries lines core
//     PostgreSQL never emits -- Citus's "Task Count"/"Tasks Shown"/"Node"
//     and PostgreSQL 19's "Supplied Plan Advice"/"Generated Plan Advice"
//     -- and any extension can add more. An unrecognised line is a Prop
//     with Raw set and nothing else. It is never an error, and never
//     dropped.
//
// Fields holds the JSON-format keys this line corresponds to, so that a
// value read out of TEXT is named exactly as EXPLAIN (FORMAT JSON) would
// have named it. For the large majority of properties that is a single
// entry whose key equals Label, because ExplainProperty() prints the same
// qlabel in both formats (see proplabels.go). For the dozen lines
// PostgreSQL assembles by hand in its TEXT branch it is several entries
// whose keys are nothing like the words on the line:
//
//	Buckets: 1024 (originally 1024)  Batches: 1  Memory Usage: 44kB
//	-> {"Hash Buckets": "1024", "Original Hash Buckets": "1024",
//	    "Hash Batches": "1", "Peak Memory Usage": "44"}
type Prop struct {
	Raw    string
	Label  string
	Value  string
	Kind   PropKind
	Fields map[string]string
}

// Known reports whether this line was recognised. An unknown line still
// renders and still round-trips; it just has no structure to query.
func (p Prop) Known() bool { return p.Label != "" }

// Field returns one JSON-named value from the line.
func (p Prop) Field(jsonKey string) (string, bool) {
	v, ok := p.Fields[jsonKey]
	return v, ok
}

// ParseProp parses one already-trimmed property line.
func ParseProp(line string) Prop {
	p := Prop{Raw: line}

	label, rest, ok := strings.Cut(line, ": ")
	if !ok {
		// "Buffers:" with nothing after it never happens, but a bare
		// "Planning:" / "JIT:" / "Settings:" block header does -- those
		// are headers for the indented lines beneath them, not
		// properties, and carry no value of their own.
		if h := strings.TrimSuffix(line, ":"); h != line {
			return Prop{Raw: line, Label: h, Kind: PropText}
		}
		return p
	}
	label, rest = strings.TrimSpace(label), strings.TrimSpace(rest)

	if fn, ok := compositeProps[label]; ok {
		p.Label = label
		p.Fields = fn(rest)
		return p
	}

	spec, ok := LookupPropSpec(label)
	if !ok {
		// Recognised as "Label: value" in shape but not in vocabulary --
		// an extension's property, or a core one added after this table
		// was written. Keep the split, claim no kind.
		return Prop{Raw: line, Label: label, Value: rest, Fields: map[string]string{label: rest}}
	}

	p.Label, p.Kind = spec.Label, spec.Kind
	p.Value = strings.TrimSuffix(rest, spec.Unit)
	p.Value = strings.TrimSpace(p.Value)
	p.Fields = map[string]string{spec.Label: p.Value}
	return p
}

// ParseProps parses a node's raw property lines.
func ParseProps(lines []string) []Prop {
	out := make([]Prop, 0, len(lines))
	for _, l := range lines {
		out = append(out, ParseProp(l))
	}
	return out
}

// compositeProps are the lines PostgreSQL assembles by hand in its TEXT
// branch rather than routing through ExplainProperty(), keyed by the word
// before the first colon. Each returns JSON-format keys.
//
// These are not a long tail. Counted over this book's whole captured
// corpus, the most frequent property lines on the page are composite ones
// -- Buffers (674 occurrences), I/O Timings (136), Sort Method (70),
// Buckets (60) -- against Filter (111) and Index Cond (58) for the
// straightforward kind.
var compositeProps = map[string]func(string) map[string]string{
	"Buffers":           parseBuffers,
	"I/O Timings":       parseIOTimings,
	"WAL":               parseWAL,
	"Sort Method":       parseSortMethod,
	"Buckets":           parseHashInfo,
	"Batches":           parseHashAggInfo,
	"Heap Blocks":       parseHeapBlocks,
	"Hits":              parseMemoizeHits,
	"Estimates":         parseMemoizeEstimates,
	"Prefetch":          parsePrefetch,
	"Storage":           parseStorage,
	"Memory":            parseMemoryCounters,
	"Full-sort Groups":  parseSortGroups("Full-sort"),
	"Pre-sorted Groups": parseSortGroups("Pre-sorted"),
}

// scopedCounters parses show_buffer_usage's and show_wal_usage's shared
// shape: comma-separated scope groups, each a scope word followed by
// space-separated key=value pairs.
//
//	shared hit=412 read=3 dirtied=1, temp read=8 written=8
//
// name maps (scope, key) to the JSON key. A pair whose scope/key
// combination is not in the map is skipped rather than guessed at.
func scopedCounters(rest string, name func(scope, key string) (string, bool)) map[string]string {
	fields := map[string]string{}
	for _, group := range strings.Split(rest, ",") {
		toks := strings.Fields(group)
		if len(toks) == 0 {
			continue
		}
		scope := toks[0]
		// A scope word is bare; anything with "=" in the first token
		// means the line had no scope at all.
		if strings.Contains(scope, "=") {
			scope = ""
			toks = append([]string{""}, toks...)
		}
		for _, tok := range toks[1:] {
			k, v, ok := strings.Cut(tok, "=")
			if !ok {
				continue
			}
			if jsonKey, ok := name(scope, k); ok {
				fields[jsonKey] = v
			}
		}
	}
	return fields
}

// parseBuffers: "Buffers: shared hit=412 read=3, temp read=8 written=8"
// -> Shared Hit Blocks, Shared Read Blocks, Temp Read Blocks, ...
func parseBuffers(rest string) map[string]string {
	return scopedCounters(rest, func(scope, key string) (string, bool) {
		scopeName := map[string]string{"shared": "Shared", "local": "Local", "temp": "Temp"}[scope]
		keyName := map[string]string{
			"hit": "Hit", "read": "Read", "dirtied": "Dirtied", "written": "Written",
		}[key]
		if scopeName == "" || keyName == "" {
			return "", false
		}
		return scopeName + " " + keyName + " Blocks", true
	})
}

// parseIOTimings: "I/O Timings: shared read=1.234 write=0.5"
// -> Shared I/O Read Time, Shared I/O Write Time.
//
// Note "write", not "written" -- show_buffer_usage spells the timing
// counters differently from the block counters on the line just above.
func parseIOTimings(rest string) map[string]string {
	return scopedCounters(rest, func(scope, key string) (string, bool) {
		scopeName := map[string]string{"shared": "Shared", "local": "Local", "temp": "Temp"}[scope]
		keyName := map[string]string{"read": "Read", "write": "Write"}[key]
		if scopeName == "" || keyName == "" {
			return "", false
		}
		return scopeName + " I/O " + keyName + " Time", true
	})
}

// parseWAL: "WAL: records=3 fpi=1 bytes=245".
func parseWAL(rest string) map[string]string {
	return scopedCounters(rest, func(scope, key string) (string, bool) {
		if scope != "" {
			return "", false
		}
		n := map[string]string{
			"records": "WAL Records", "fpi": "WAL FPI",
			"bytes": "WAL Bytes", "full": "WAL Buffers Full",
		}[key]
		return n, n != ""
	})
}

// parseSortMethod: "Sort Method: quicksort  Memory: 25kB".
//
// The second label is the sort space TYPE ("Memory" or "Disk"), which
// JSON reports as a separate "Sort Space Type" string alongside a
// "Sort Space Used" number -- so the word on the TEXT line is a value in
// JSON, not a key.
func parseSortMethod(rest string) map[string]string {
	fields := map[string]string{}
	method, space, ok := cutDoubleSpace(rest)
	if !ok {
		fields["Sort Method"] = strings.TrimSpace(rest)
		return fields
	}
	fields["Sort Method"] = strings.TrimSpace(method)
	if spaceType, used, ok := strings.Cut(space, ": "); ok {
		fields["Sort Space Type"] = strings.TrimSpace(spaceType)
		fields["Sort Space Used"] = trimUnit(used)
	}
	return fields
}

// parseHashInfo: "Buckets: 1024 (originally 1024)  Batches: 1 (originally 1)  Memory Usage: 44kB".
//
// Five JSON keys, none of them spelled the way the line spells them.
func parseHashInfo(rest string) map[string]string {
	fields := map[string]string{}
	assign := func(label, value string) {
		v, orig := splitOriginally(value)
		switch label {
		case "Buckets":
			fields["Hash Buckets"] = v
			if orig != "" {
				fields["Original Hash Buckets"] = orig
			}
		case "Batches":
			fields["Hash Batches"] = v
			if orig != "" {
				fields["Original Hash Batches"] = orig
			}
		case "Memory Usage":
			fields["Peak Memory Usage"] = trimUnit(v)
		}
	}
	// The first segment has no label of its own -- the line's own
	// "Buckets" prefix was already consumed by the caller.
	segs := splitDoubleSpace(rest)
	if len(segs) > 0 {
		assign("Buckets", segs[0])
	}
	for _, seg := range segs[1:] {
		if l, v, ok := strings.Cut(seg, ": "); ok {
			assign(strings.TrimSpace(l), v)
		}
	}
	return fields
}

// parseHashAggInfo: "Batches: 1  Memory Usage: 105kB  Disk Usage: 32kB",
// the standalone HashAggregate form (show_hashagg_info) -- distinct from
// the "Batches" that appears inside a Hash node's Buckets line above.
func parseHashAggInfo(rest string) map[string]string {
	fields := map[string]string{}
	segs := splitDoubleSpace(rest)
	if len(segs) > 0 {
		fields["HashAgg Batches"] = strings.TrimSpace(segs[0])
	}
	for _, seg := range segs[1:] {
		l, v, ok := strings.Cut(seg, ": ")
		if !ok {
			continue
		}
		switch strings.TrimSpace(l) {
		case "Memory Usage":
			fields["Peak Memory Usage"] = trimUnit(v)
		case "Disk Usage":
			fields["Disk Usage"] = trimUnit(v)
		}
	}
	return fields
}

// parseHeapBlocks: "Heap Blocks: exact=1234 lossy=0".
func parseHeapBlocks(rest string) map[string]string {
	return scopedCounters(rest, func(scope, key string) (string, bool) {
		if scope != "" {
			return "", false
		}
		n := map[string]string{"exact": "Exact Heap Blocks", "lossy": "Lossy Heap Blocks"}[key]
		return n, n != ""
	})
}

// parseMemoizeHits: "Hits: 5  Misses: 2  Evictions: 0  Overflows: 0  Memory Usage: 1kB".
func parseMemoizeHits(rest string) map[string]string {
	fields := map[string]string{}
	segs := splitDoubleSpace(rest)
	if len(segs) > 0 {
		fields["Cache Hits"] = strings.TrimSpace(segs[0])
	}
	for _, seg := range segs[1:] {
		l, v, ok := strings.Cut(seg, ": ")
		if !ok {
			continue
		}
		switch strings.TrimSpace(l) {
		case "Misses":
			fields["Cache Misses"] = strings.TrimSpace(v)
		case "Evictions":
			fields["Cache Evictions"] = strings.TrimSpace(v)
		case "Overflows":
			fields["Cache Overflows"] = strings.TrimSpace(v)
		case "Memory Usage":
			fields["Peak Memory Usage"] = trimUnit(v)
		}
	}
	return fields
}

// parseMemoizeEstimates: "Estimates: capacity=8 distinct keys=4 lookups=10 hit percent=60.00%".
//
// Space-separated key=value where the keys themselves contain spaces, so
// this cannot use scopedCounters: it scans for the known key names.
func parseMemoizeEstimates(rest string) map[string]string {
	fields := map[string]string{}
	for _, k := range []struct{ text, json string }{
		{"capacity=", "Estimated Capacity"},
		{"distinct keys=", "Estimated Distinct Lookup Keys"},
		{"lookups=", "Estimated Lookups"},
		{"hit percent=", "Estimated Hit Percent"},
	} {
		i := strings.Index(rest, k.text)
		if i < 0 {
			continue
		}
		v := rest[i+len(k.text):]
		if j := strings.IndexByte(v, ' '); j >= 0 {
			v = v[:j]
		}
		fields[k.json] = strings.TrimSuffix(v, "%")
	}
	return fields
}

// parsePrefetch: "Prefetch: avg=2.00 max=8 capacity=16".
func parsePrefetch(rest string) map[string]string {
	return scopedCounters(rest, func(scope, key string) (string, bool) {
		if scope != "" {
			return "", false
		}
		n := map[string]string{
			"avg": "Average Prefetch Distance", "max": "Max Prefetch Distance",
			"capacity": "Prefetch Capacity",
		}[key]
		return n, n != ""
	})
}

// parseStorage: "Storage: Memory  Maximum Storage: 32kB".
func parseStorage(rest string) map[string]string {
	fields := map[string]string{}
	storage, max, ok := cutDoubleSpace(rest)
	fields["Storage"] = strings.TrimSpace(storage)
	if ok {
		if l, v, ok := strings.Cut(max, ": "); ok && strings.TrimSpace(l) == "Maximum Storage" {
			fields["Maximum Storage"] = trimUnit(v)
		}
	}
	return fields
}

// parseMemoryCounters: "Memory: used=120kB  allocated=256kB".
func parseMemoryCounters(rest string) map[string]string {
	fields := map[string]string{}
	for _, seg := range splitDoubleSpace(rest) {
		for _, tok := range strings.Fields(seg) {
			k, v, ok := strings.Cut(tok, "=")
			if !ok {
				continue
			}
			switch k {
			case "used":
				fields["Memory Used"] = trimUnit(v)
			case "allocated":
				fields["Memory Allocated"] = trimUnit(v)
			}
		}
	}
	return fields
}

// parseSortGroups handles both incremental-sort group lines:
//
//	Full-sort Groups: 2  Sort Method: quicksort  Average Memory: 26kB  Peak Memory: 26kB
//
// The leading word ("Full-sort" / "Pre-sorted") selects which group JSON
// object the values belong to, so it is kept as a prefix rather than
// flattened -- the two can and do appear on the same node.
func parseSortGroups(kind string) func(string) map[string]string {
	return func(rest string) map[string]string {
		fields := map[string]string{}
		segs := splitDoubleSpace(rest)
		if len(segs) > 0 {
			fields[kind+" Group Count"] = strings.TrimSpace(segs[0])
		}
		for _, seg := range segs[1:] {
			l, v, ok := strings.Cut(seg, ": ")
			if !ok {
				continue
			}
			switch strings.TrimSpace(l) {
			case "Sort Method", "Sort Methods Used":
				fields[kind+" Sort Methods Used"] = strings.TrimSpace(v)
			case "Average Memory":
				fields[kind+" Average Sort Space Used"] = trimUnit(v)
			case "Peak Memory":
				fields[kind+" Peak Sort Space Used"] = trimUnit(v)
			}
		}
		return fields
	}
}

// splitDoubleSpace splits on the two-space separator PostgreSQL uses
// between the segments of a composite TEXT line. Single spaces are
// ordinary -- "external merge" and "top-N heapsort" are one value each.
func splitDoubleSpace(s string) []string {
	parts := strings.Split(s, "  ")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cutDoubleSpace(s string) (before, after string, found bool) {
	segs := splitDoubleSpace(s)
	if len(segs) < 2 {
		return s, "", false
	}
	return segs[0], strings.Join(segs[1:], "  "), true
}

// splitOriginally splits "1024 (originally 512)" into value and original.
func splitOriginally(s string) (value, original string) {
	s = strings.TrimSpace(s)
	i := strings.Index(s, " (originally ")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSuffix(s[i+len(" (originally "):], ")")
}

// trimUnit drops the unit suffix ExplainProperty appends in TEXT and
// omits in JSON, so a TEXT-parsed value compares equal to a JSON one.
func trimUnit(s string) string {
	s = strings.TrimSpace(s)
	for _, u := range []string{"kB", "ms", "bytes", "%"} {
		s = strings.TrimSuffix(s, u)
	}
	return strings.TrimSpace(s)
}
