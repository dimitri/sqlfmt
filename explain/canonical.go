package explain

import (
	"sort"
	"strings"
)

// Canonicalization puts two advice blocks into a form where they can be
// compared even when they came from different places.
//
// The case that motivates it is an upgrade. You have a plan captured from
// the PostgreSQL 16 server you run today, reconstructed by this package,
// and a plan from a PostgreSQL 19 server printed by pg_plan_advice itself,
// and the question is whether the planner changed its mind. That is
// probably the single most valuable plan comparison anybody makes, and it
// is inherently cross-source.
//
// Compared directly, the two disagree in ways that mean nothing:
//
//   - Relation ordering inside a line. 19 orders by its internal relation
//     numbering, which is stable per QUERY; this package orders by walking
//     the plan tree, which is stable per PLAN. Neither is recoverable from
//     the other, and for a tag like NO_GATHER(a b c) the order carries no
//     meaning at all — it is a set written down in some order.
//   - Schema-qualified index names. 19 prints "public.foo_pkey"; text
//     EXPLAIN prints "foo_pkey" and the schema is not in it.
//
// So canonical form sorts what is a set and strips what is unrecoverable,
// and does neither to anything where the order is load-bearing. The result
// is deliberately NOT valid input to pg_plan_advice — sorting JOIN_ORDER
// would change its meaning, so JOIN_ORDER is left exactly as it was, and
// stripping the schema from an index name makes it ambiguous. This is a
// comparison key, not an advice string. Everything in this file exists to
// make diffs quiet, not to feed a server.

// orderedTags are the tags whose argument order is meaning, not notation.
//
// JOIN_ORDER(t1 t2 t3) says t1 drives and is joined to t2 before t3;
// sorting it would state something different and probably false. Its
// nested groups are ordered for the same reason: the README's
// JOIN_ORDER(t1 (t2 t3)) puts t2 on the outer side of the inner join.
//
// Every other tag this package emits names a set. HASH_JOIN(a b) says
// both a and b sit on the inner side of a hash join; which was written
// first is an artifact of how the plan happened to be walked.
var orderedTags = map[string]bool{
	"JOIN_ORDER": true,
}

// pairedTags take (relation, index) pairs rather than a flat list:
// INDEX_SCAN(foo foo_a_idx bar bar_b_idx). Sorting the tokens would
// scramble relations against indexes, so these sort by pair and keep each
// pair together.
var pairedTags = map[string]bool{
	"INDEX_SCAN":      true,
	"INDEX_ONLY_SCAN": true,
}

// Canonical returns the line in canonical form. The receiver is unchanged.
func (l AdviceLine) Canonical() AdviceLine {
	out := AdviceLine{Kind: l.Kind, Args: make([]string, len(l.Args))}
	copy(out.Args, l.Args)

	if pairedTags[l.Kind] {
		out.Args = canonicalPairs(out.Args)
		return out
	}
	if orderedTags[l.Kind] {
		// Order is meaning: normalize inside groups only, never across.
		for i, a := range out.Args {
			out.Args[i] = canonicalArg(a, true)
		}
		return out
	}
	for i, a := range out.Args {
		out.Args[i] = canonicalArg(a, false)
	}
	sort.Strings(out.Args)
	return out
}

// canonicalPairs sorts (relation, index) pairs by relation, keeping each
// relation next to its index. An odd trailing argument is left in place
// rather than silently paired with nothing: it means the input was not
// the shape this tag is documented to have, and quietly reordering it
// would hide that.
func canonicalPairs(args []string) []string {
	if len(args)%2 != 0 {
		return args
	}
	type pair struct{ rel, idx string }
	pairs := make([]pair, 0, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		pairs = append(pairs, pair{stripSchema(args[i]), stripSchema(args[i+1])})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].rel != pairs[j].rel {
			return pairs[i].rel < pairs[j].rel
		}
		return pairs[i].idx < pairs[j].idx
	})
	out := make([]string, 0, len(args))
	for _, p := range pairs {
		out = append(out, p.rel, p.idx)
	}
	return out
}

// canonicalArg normalizes one argument.
//
// An argument is not always one target. JOIN_ORDER renders its whole
// ordered list into a single pre-rendered string, so "a b (c d)" arrives
// as one Args entry, and a naive stripSchema over it would treat the
// entire string as one dotted name and return "d". So this splits on
// top-level spaces first and recurses, and only ever hands a leaf token —
// something with no spaces and no parentheses — to stripSchema.
//
// A parenthesized group names a join product: "(bletch quux)". Members of
// a group under a set-like tag are themselves a set; under an ordered tag
// they are not, because JOIN_ORDER(t1 (t2 t3)) puts t2 on the outer side.
func canonicalArg(arg string, ordered bool) string {
	if members := splitTopLevelSpace(arg); len(members) > 1 {
		for i, m := range members {
			members[i] = canonicalArg(m, ordered)
		}
		if !ordered {
			sort.Strings(members)
		}
		return strings.Join(members, " ")
	}
	if !strings.HasPrefix(arg, "(") || !strings.HasSuffix(arg, ")") {
		return stripSchema(arg)
	}
	inner := arg[1 : len(arg)-1]
	if inner == "" {
		return arg
	}
	members := splitTopLevelSpace(inner)
	for i, m := range members {
		members[i] = canonicalArg(m, ordered)
	}
	if !ordered {
		sort.Strings(members)
	}
	return "(" + strings.Join(members, " ") + ")"
}

// splitTopLevelSpace splits on spaces that are not inside parentheses, so
// a nested group stays one member.
func splitTopLevelSpace(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ' ':
			if depth == 0 {
				if i > start {
					out = append(out, s[start:i])
				}
				start = i + 1
			}
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// stripSchema drops a schema qualifier, leaving the bare object name.
//
// This is needed in both directions, and PostgreSQL's own source says why.
//
// For index names, pgpa_output_relation_name() in contrib/pg_plan_advice/
// pgpa_output.c writes quote_identifier(namespace) + "." +
// quote_identifier(name) unconditionally — there is no search_path check,
// so 19 ALWAYS prints public.foo_pkey. Text EXPLAIN always prints the bare
// foo_pkey. Neither side can produce the other, so both reduce to bare.
//
// For relation names it is the other way round. 19 identifies relations by
// alias, never schema-qualified — the schema appears only in the partition
// position of alias#occurrence/schema.partition@plan. But EXPLAIN (VERBOSE)
// prints "Seq Scan on f1db.results", so a plan parsed from verbose output
// yields "f1db.results" where 19 would say "results". Stripping also makes
// a VERBOSE and a non-VERBOSE capture of the same plan compare equal, which
// is worth having on its own.
//
// The split is on the last dot at quote depth zero, because either half may
// be quoted: public."weird idx" must lose "public." and keep the quoted
// name, while a single quoted identifier that merely contains a dot —
// "a.b" — is one name and must be left whole.
func stripSchema(name string) string {
	last := -1
	inQuote := false
	for i := 0; i < len(name); i++ {
		switch name[i] {
		case '"':
			// "" inside a quoted identifier is an escaped quote; either
			// way toggling twice returns to the same state.
			inQuote = !inQuote
		case '.':
			if !inQuote {
				last = i
			}
		}
	}
	if last >= 0 && last+1 < len(name) {
		return name[last+1:]
	}
	return name
}

// CanonicalAdvice is Advice with every line in canonical form.
func CanonicalAdvice(p *Plan) []AdviceLine {
	lines := Advice(p)
	out := make([]AdviceLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.Canonical())
	}
	return out
}

// CanonicalAdviceString is AdviceString in canonical form.
func CanonicalAdviceString(p *Plan) string {
	lines := CanonicalAdvice(p)
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.String())
	}
	return strings.Join(out, "\n")
}

// ParseAdviceLine reads one line of advice text back into an AdviceLine —
// "HASH_JOIN(races drivers)" — so that a block printed by PostgreSQL 19
// can be canonicalized and compared against one derived here.
//
// It accepts the leading whitespace 19 indents its advice block with, and
// returns ok=false for anything that is not TAG(args), which is how the
// surrounding "Generated Plan Advice:" header and blank lines are skipped
// by a caller scanning a plan.
func ParseAdviceLine(s string) (AdviceLine, bool) {
	s = strings.TrimSpace(s)
	open := strings.IndexByte(s, '(')
	if open <= 0 || !strings.HasSuffix(s, ")") {
		return AdviceLine{}, false
	}
	kind := s[:open]
	for _, r := range kind {
		if (r < 'A' || r > 'Z') && r != '_' {
			return AdviceLine{}, false
		}
	}
	inner := s[open+1 : len(s)-1]
	// Reject unbalanced parens rather than producing a half-parsed line.
	depth := 0
	for _, r := range inner {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth < 0 {
			return AdviceLine{}, false
		}
	}
	if depth != 0 {
		return AdviceLine{}, false
	}
	var args []string
	if inner != "" {
		args = splitTopLevelSpace(inner)
	}
	return AdviceLine{Kind: kind, Args: args}, true
}

// CanonicalAdviceTextFrom extracts an advice block from text and returns it
// in canonical form.
//
// The text can be a whole EXPLAIN (PLAN_ADVICE) capture: PostgreSQL 19
// prints its block under a "Generated Plan Advice:" header, indented,
// inside the plan output, and anything that is not TAG(args) is skipped.
// It can equally be four bare lines someone pasted. Either way what comes
// back is comparable with CanonicalAdviceString on a plan parsed here,
// which is the point — one side reconstructed, one side authoritative.
//
// A "Supplied Plan Advice" block, which 19 also prints and which carries
// /* matched */ comments, is deliberately not special-cased: its lines end
// in a comment rather than ")" and so are skipped as not-advice.
func CanonicalAdviceTextFrom(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		l, ok := ParseAdviceLine(line)
		if !ok {
			continue
		}
		out = append(out, l.Canonical().String())
	}
	return strings.Join(out, "\n")
}
