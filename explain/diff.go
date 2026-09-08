package explain

import (
	"fmt"
	"strings"
)

// Diff compares two plans of the same query and reports what changed.
//
// The problem it solves is that a plain diff of two EXPLAIN outputs is
// useless: every line carries a cost or a timing, so every line differs,
// and the one structural change that actually explains the difference is
// buried in numeric noise. pganalyze, who ship the only comparable tool,
// describe the same difficulty — node alignment between two differently
// shaped trees is the hard part, and standard diff output is dominated by
// cost and timing variation.
//
// The way out is to compare the ADVICE rather than the plan text. Advice
// names the planner's decisions and mentions no numbers at all, so two
// runs of the same plan produce identical advice while any real
// structural change shows up as a changed line. That sidesteps node
// alignment entirely: advice is keyed by tag, not by position in a tree.
//
// Numbers are still reported, separately and clearly labelled, because
// "did it get faster?" is the other half of the question. They are never
// mixed into the structural comparison.
type Diff struct {
	Structural []AdviceChange // structural differences, empty when the shape held
	Before     *Plan
	After      *Plan

	// BeforeName and AfterName label the two sides in the ---/+++ header,
	// the way diff(1) names its files. Empty falls back to "before" and
	// "after", so a caller comparing two in-memory plans still gets a
	// well-formed header.
	BeforeName string
	AfterName  string

	// Canonical records that both sides were canonicalized before
	// comparison, so the rendered diff can say so: a reader looking at a
	// quiet diff deserves to know whether it is quiet because nothing
	// changed or because differences were normalized away.
	Canonical bool
}

// AdviceChange is one structural difference: an advice tag whose targets
// changed, appeared, or went away.
type AdviceChange struct {
	Kind   string // the advice tag
	Before string // rendered line before, "" when the tag was absent
	After  string // rendered line after, "" when the tag went away
}

// SameStructure reports whether the planner made the same decisions in
// both plans. When true, any difference between the two runs is numeric.
func (d *Diff) SameStructure() bool { return len(d.Structural) == 0 }

// DiffPlans compares two plans. Either may be nil, which is reported as a
// wholesale appearance or disappearance rather than treated as an error.
func DiffPlans(before, after *Plan) *Diff {
	return diffPlans(before, after, false)
}

// DiffPlansCanonical is DiffPlans with both sides put into canonical form
// first, so that advice orderings and schema-qualified index names do not
// show up as differences. Use it when the two plans did not come from the
// same source — comparing a plan from the server you run today against one
// from a PostgreSQL 19 server, for instance, where the two agree on the
// decisions but not on how they write them down.
func DiffPlansCanonical(before, after *Plan) *Diff {
	return diffPlans(before, after, true)
}

func diffPlans(before, after *Plan, canonical bool) *Diff {
	d := &Diff{Before: before, After: after, Canonical: canonical}

	type entry struct{ before, after string }
	// Insertion-ordered so the report follows advice emission order —
	// join order first, then methods, then scans, then parallelism —
	// which reads as most-structural-first.
	var order []string
	seen := map[string]*entry{}
	take := func(lines []AdviceLine, isAfter bool) {
		for _, l := range lines {
			e, ok := seen[l.Kind]
			if !ok {
				e = &entry{}
				seen[l.Kind] = e
				order = append(order, l.Kind)
			}
			if isAfter {
				e.after = l.String()
			} else {
				e.before = l.String()
			}
		}
	}
	advice := Advice
	if canonical {
		advice = CanonicalAdvice
	}
	take(advice(before), false)
	take(advice(after), true)

	for _, kind := range order {
		e := seen[kind]
		if e.before != e.after {
			d.Structural = append(d.Structural, AdviceChange{
				Kind: kind, Before: e.before, After: e.after,
			})
		}
	}
	return d
}

// String renders the diff as unified-diff text.
//
// Unified diff rather than a bespoke layout, because the format is the
// interoperability. Every pager, editor, review tool and syntax
// highlighter already colours "-" and "+" lines, so this output drops
// into `less -R`, `delta`, `bat`, a GitHub comment or a typeset listing
// and comes out looking right with nothing else being taught how. A
// custom shape would have to earn all of that back, and would still not
// compose with anything.
//
// The ---/+++ header names what was compared, which a diff that omits it
// leaves the reader to remember. The "@@ plan structure @@" hunk header
// says which of the two questions this section answers.
//
// The timing lines are ordinary context lines, deliberately: they are
// reported alongside the structural comparison and must never be mistaken
// for part of it, and context is exactly the unified-diff idea of "shown,
// not changed".
func (d *Diff) String() string {
	beforeName, afterName := d.BeforeName, d.AfterName
	if beforeName == "" {
		beforeName = "before"
	}
	if afterName == "" {
		afterName = "after"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n", beforeName)
	fmt.Fprintf(&b, "+++ %s\n", afterName)
	if d.Canonical {
		b.WriteString("@@ plan structure (canonical) @@\n")
	} else {
		b.WriteString("@@ plan structure @@\n")
	}

	if d.SameStructure() {
		b.WriteString(" structure unchanged - the planner made the same decisions\n")
	} else {
		for _, c := range d.Structural {
			if c.Before != "" {
				fmt.Fprintf(&b, "-%s\n", c.Before)
			}
			if c.After != "" {
				fmt.Fprintf(&b, "+%s\n", c.After)
			}
		}
	}

	if nums := d.numberLines(); len(nums) > 0 {
		b.WriteString("\n")
		for _, line := range nums {
			fmt.Fprintf(&b, " %s\n", line)
		}
	}
	return b.String()
}

// numberLines renders the timing comparison, or nothing at all when
// either plan came from an EXPLAIN without ANALYZE. Inventing a zero for
// a missing timing would read as a catastrophic change.
func (d *Diff) numberLines() []string {
	var out []string
	if s := deltaMS("execution time", execTime(d.Before), execTime(d.After)); s != "" {
		out = append(out, s)
	}
	if s := deltaMS("planning time", planTime(d.Before), planTime(d.After)); s != "" {
		out = append(out, s)
	}
	return out
}

// AdviceText renders one plan's structure as plain text — the same
// vocabulary the diff compares, for showing a single plan on its own.
func AdviceText(p *Plan) string {
	var b strings.Builder
	for _, l := range Advice(p) {
		b.WriteString(l.String())
		b.WriteString("\n")
	}
	return b.String()
}

func execTime(p *Plan) *float64 {
	if p == nil {
		return nil
	}
	return p.ExecutionTime
}

func planTime(p *Plan) *float64 {
	if p == nil {
		return nil
	}
	return p.PlanningTime
}

// deltaMS renders one millisecond comparison, with the percentage change
// that is usually the only part anyone reads. Returns "" when either side
// is missing, since EXPLAIN without ANALYZE reports no timings and an
// invented zero would read as a catastrophic regression.
func deltaMS(label string, before, after *float64) string {
	if before == nil || after == nil {
		return ""
	}
	b, a := *before, *after
	s := fmt.Sprintf("%-15s %9.3f ms -> %9.3f ms", label, b, a)
	if b > 0 {
		pct := (a - b) / b * 100
		sign := "+"
		if pct < 0 {
			sign = ""
		}
		s += fmt.Sprintf("   (%s%.1f%%)", sign, pct)
	}
	return s
}
