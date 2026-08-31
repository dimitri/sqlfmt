package format

import "strings"

// A Wadler/Lindig pretty-printing IR, used for expression layout.
//
// The greedy line-filler this replaces decided each break where it stood,
// with no knowledge of what came after. That is fine for a clause river,
// where the break points are fixed by the grammar, but wrong inside an
// expression, where the decisions interact: whether to break a function
// call's arguments depends on whether the enclosing comparison is going to
// break at its operator, which depends on whether the call broke. Three
// attempts to patch that greedily each improved some files and regressed
// others.
//
// In this IR a Group is a unit that is laid out flat if it fits on the
// remaining line and broken otherwise, and the decision is made for the
// whole group at once, outermost first. "Break the outer group only if the
// inner ones still do not fit" is then a property of the algorithm rather
// than something hand-coded per construct.
//
// The vocabulary is the standard one:
//
//	Text  literal characters, never broken
//	Line  a space when its group is flat, a newline + indent when broken
//	Soft  nothing when flat, a newline + indent when broken
//	Group a unit whose flat/broken choice is made together
//	Nest  increases the indent applied by Line/Soft inside it
//	Concat sequence
type docKind int

const (
	docText docKind = iota
	docLine
	docSoft
	docGroup
	docNest
	docConcat
	// docHard is a break that is always taken, and which forces every
	// enclosing group to break too -- what a comment attached mid-
	// expression needs, since it cannot share a line with what follows.
	docHard
	// docFill packs its parts onto as few lines as fit, deciding each
	// separator on its own rather than all-or-nothing like a Group. A
	// function call's arguments want this: breaking a five-argument call
	// onto five lines when two would do is not what the corpus does, and
	// reads worse. A "||" chain wants Group instead, because its parts are
	// whole clauses of a template and the author writes them one per line.
	docFill
	// docDefer holds a sub-expression the clause layer already knows how
	// to lay out -- a CASE, an OVER(...), a subquery -- as a function of
	// the column it ends up at. Without it the Doc layer had to decline
	// any run containing one, which in practice meant declining the
	// figure-generating calls that most needed it, since they pass a CASE
	// as an argument.
	docDefer
)

type Doc struct {
	kind   docKind
	text   string
	indent int
	parts  []Doc
	// render is set for docDefer: it lays the sub-expression out at the
	// column it is actually placed at. flat is its single-line form, used
	// for width decisions, or "" when it has none.
	render func(col int) []string
	flat   string
}

// defer_ wraps an already-understood sub-expression. flat is its one-line
// form ("" if it cannot be rendered on one line).
func defer_(flat string, render func(col int) []string) Doc {
	return Doc{kind: docDefer, flat: flat, render: render}
}

func text(s string) Doc     { return Doc{kind: docText, text: s} }
func line() Doc             { return Doc{kind: docLine} }
func soft() Doc             { return Doc{kind: docSoft} }
func hard() Doc             { return Doc{kind: docHard} }
func group(d Doc) Doc       { return Doc{kind: docGroup, parts: []Doc{d}} }
func nest(i int, d Doc) Doc { return Doc{kind: docNest, indent: i, parts: []Doc{d}} }
func concat(ds ...Doc) Doc  { return Doc{kind: docConcat, parts: ds} }

// fill packs parts, separated by sep, onto as few lines as fit.
func fill(sep Doc, parts ...Doc) Doc {
	ds := make([]Doc, 0, len(parts)*2)
	for i, p := range parts {
		if i > 0 {
			ds = append(ds, sep)
		}
		ds = append(ds, p)
	}
	return Doc{kind: docFill, parts: ds}
}

// hasHard reports whether d contains a hard break, which forces every
// group containing it to break.
func hasHard(d Doc) bool {
	if d.kind == docHard {
		return true
	}
	for _, p := range d.parts {
		if hasHard(p) {
			return true
		}
	}
	return false
}

// flatWidth returns the width d would occupy laid out flat, or -1 when it
// contains a hard break and therefore has no flat form.
func flatWidth(d Doc) int {
	switch d.kind {
	case docText:
		return cols(d.text)
	case docLine:
		return 1
	case docSoft:
		return 0
	case docHard:
		return -1
	case docDefer:
		if d.flat == "" {
			return -1
		}
		return cols(d.flat)
	}
	total := 0
	for _, p := range d.parts {
		w := flatWidth(p)
		if w < 0 {
			return -1
		}
		total += w
	}
	return total
}

// renderDoc lays d out starting at column col, with lines wrapped to
// targetWidth. The first line carries no indent of its own -- the caller
// has already placed it -- and continuation lines are indented by col plus
// whatever Nest adds.
func renderDoc(d Doc, col int) []string {
	return renderDocTail(d, col, 0)
}

// renderDocTail is renderDoc told what follows: tail is the number of
// columns the caller will still write on d's last line.
func renderDocTail(d Doc, col, tail int) []string {
	r := &docRenderer{col: col, out: []string{""}}
	r.render(d, col, false, tail)
	return r.out
}

// headWidth returns how much of d is guaranteed to land on the line it
// starts on -- its width up to the first point where a break could be
// taken -- and whether it stopped at such a point.
//
// This is what a group's fits check needs to know about what follows it.
// Wadler's fits measures the flattened group *together with its
// continuation*; measuring the group alone lets the text after it push the
// line past the margin, which is how "format(...) as elem" overflowed
// while the call itself was judged to fit. Stopping at the first break
// opportunity keeps the measure a lower bound: we never invent pressure
// that a later break would have relieved.
func headWidth(d Doc) (int, bool) {
	switch d.kind {
	case docText:
		return cols(d.text), false
	case docLine, docSoft, docHard:
		return 0, true
	case docDefer:
		if d.flat == "" {
			return 0, true
		}
		return cols(d.flat), false
	}
	total := 0
	for _, p := range d.parts {
		w, stop := headWidth(p)
		total += w
		if stop {
			return total, true
		}
	}
	return total, false
}

// tailOf measures the siblings that follow on the same line, falling back
// to the enclosing tail once none of them can break.
func tailOf(rest []Doc, outer int) int {
	total := 0
	for _, p := range rest {
		w, stop := headWidth(p)
		total += w
		if stop {
			return total
		}
	}
	return total + outer
}

type docRenderer struct {
	col int
	out []string
}

func (r *docRenderer) write(s string) {
	r.out[len(r.out)-1] += s
	r.col += cols(s)
}

func (r *docRenderer) newline(indent int) {
	if indent < 0 {
		indent = 0
	}
	r.out = append(r.out, strings.Repeat(" ", indent))
	r.col = indent
}

// render emits d. broken says whether the enclosing group chose to break;
// indent is the column continuation lines start at; tail is how many
// columns the caller still writes on d's last line.
func (r *docRenderer) render(d Doc, indent int, broken bool, tail int) {
	switch d.kind {
	case docText:
		r.write(d.text)
	case docLine:
		if broken {
			r.newline(indent)
		} else {
			r.write(" ")
		}
	case docSoft:
		if broken {
			r.newline(indent)
		}
	case docHard:
		r.newline(indent)
	case docNest:
		r.render(d.parts[0], indent+d.indent, broken, tail)
	case docConcat:
		for i, p := range d.parts {
			r.render(p, indent, broken, tailOf(d.parts[i+1:], tail))
		}
	case docDefer:
		// Flat if it fits where we are, otherwise let the clause layer lay
		// it out at this column.
		if d.flat != "" && r.col+cols(d.flat)+tail <= targetWidth {
			r.write(d.flat)
			return
		}
		sub := d.render(r.col)
		if len(sub) == 0 {
			r.write(d.flat)
			return
		}
		r.write(sub[0])
		for _, l := range sub[1:] {
			r.out = append(r.out, l)
			r.col = cols(l)
		}
	case docFill:
		// Each separator decides on its own: stay on this line while the
		// next part still fits, break when it does not. That is what makes
		// a filled list pack rather than explode one item per line.
		prevBroke := false
		for i := 0; i < len(d.parts); i++ {
			part := d.parts[i]
			if part.kind == docConcat && len(part.parts) == 2 &&
				(part.parts[1].kind == docLine || part.parts[1].kind == docSoft) {
				r.render(part.parts[0], indent, broken, 0)
				// An item that took more than one line ends on a line of
				// its own -- the closing paren of a deferred OVER, say --
				// and packing the next item after it runs two list items
				// together on that line, which reads as one. Once a part
				// has broken, its separator breaks too.
				if prevBroke {
					r.newline(indent)
					continue
				}
				w := 0
				if i+1 < len(d.parts) {
					w = flatWidth(d.parts[i+1])
				}
				// Only the last item carries the enclosing tail: what
				// follows the whole list -- ") as elem" -- lands on its line.
				nextTail := 0
				if i+1 == len(d.parts)-1 {
					nextTail = tail
				}
				sepW := 0
				if part.parts[1].kind == docLine {
					sepW = 1
				}
				if w >= 0 && r.col+sepW+w+nextTail <= targetWidth {
					r.render(part.parts[1], indent, false, 0)
				} else {
					r.newline(indent)
				}
				continue
			}
			before := len(r.out)
			r.render(part, indent, broken, tailOf(d.parts[i+1:], tail))
			prevBroke = len(r.out) > before
		}
	case docGroup:
		// The whole group's decision, made here and once: flat if what it
		// would occupy, plus what follows it on the line, fits in what is
		// left.
		w := flatWidth(d.parts[0])
		fits := w >= 0 && r.col+w+tail <= targetWidth
		r.render(d.parts[0], indent, !fits, tail)
	}
}
