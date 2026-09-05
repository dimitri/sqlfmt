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
	d := &Diff{Before: before, After: after}

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
	take(Advice(before), false)
	take(Advice(after), true)

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

// String renders the diff for a terminal.
func (d *Diff) String() string {
	var b strings.Builder

	if d.SameStructure() {
		b.WriteString("structure unchanged — the planner made the same decisions\n")
	} else {
		fmt.Fprintf(&b, "STRUCTURE (%s)\n", plural(len(d.Structural), "change", "changes"))
		for _, c := range d.Structural {
			if c.Before != "" {
				fmt.Fprintf(&b, "  - %s\n", c.Before)
			}
			if c.After != "" {
				fmt.Fprintf(&b, "  + %s\n", c.After)
			}
		}
	}

	var nums []string
	if s := deltaMS("execution time", execTime(d.Before), execTime(d.After)); s != "" {
		nums = append(nums, s)
	}
	if s := deltaMS("planning time", planTime(d.Before), planTime(d.After)); s != "" {
		nums = append(nums, s)
	}
	if len(nums) > 0 {
		b.WriteString("\nNUMBERS\n")
		for _, n := range nums {
			fmt.Fprintf(&b, "  %s\n", n)
		}
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
	s := fmt.Sprintf("%-15s %9.3f ms → %9.3f ms", label, b, a)
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

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
