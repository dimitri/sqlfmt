package explain

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// explainLineRE splits one EXPLAIN/EXPLAIN ANALYZE output line into its
// leading indentation+label (group 1) and its trailing run of one or more
// parenthetical groups — "(cost=0.00..29.20 rows=857 width=44)",
// "(actual time=0.008..0.150 rows=857 loops=1)", "(never executed)" —
// which is the part that makes these lines routinely wider than a PDF
// page's fixed-width verbatim column count (unlike HTML/EPUB, which just
// reflow). Parenthetical groups here are always flat (no nested parens),
// so a simple non-greedy split is safe.
var explainLineRE = regexp.MustCompile(`^(\s*(?:->\s*)?.*?)((?:\s+\([^()]*\))+)\s*$`)

var explainGroupRE = regexp.MustCompile(`\s*\(([^()]*)\)`)

// FormatForWidth reformats raw EXPLAIN/EXPLAIN ANALYZE text so no single
// line exceeds maxWidth columns, by moving each trailing "(cost=...)"/
// "(actual time=...)"/"(never executed)" parenthetical group of an
// over-width line onto its own continuation line, indented four spaces
// past that line's own indentation — the fixed-width LaTeX `verbatim`
// environment book/course Course Companion PDFs typeset psql output in
// never wraps on its own (see build/pandoc/templates/tablet.latex), so a
// long ANALYZE line otherwise runs straight off the page edge instead of
// truncating or wrapping. Lines that already fit, and lines with no
// parenthetical group to split (e.g. "Planning Time: 0.045 ms"), pass
// through unchanged. Byte-for-byte round-trippable back into the same
// groups if maxWidth is later widened — this only ever *moves* text, it
// never truncates or drops anything.
func FormatForWidth(text string, maxWidth int) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if utf8.RuneCountInString(line) <= maxWidth {
			out = append(out, line)
			continue
		}
		m := explainLineRE.FindStringSubmatch(line)
		if m == nil {
			out = append(out, wrapByCommas(line, maxWidth)...)
			continue
		}
		head, groupsPart := m[1], m[2]
		groups := explainGroupRE.FindAllStringSubmatch(groupsPart, -1)
		if len(groups) == 0 {
			out = append(out, wrapByCommas(line, maxWidth)...)
			continue
		}
		out = append(out, head)
		contIndent := leadingWhitespace(head) + "    "
		for _, g := range groups {
			out = append(out, contIndent+"("+g[1]+")")
		}
	}
	reindentHeader(out)
	return strings.Join(out, "\n")
}

// wrapByCommas is FormatForWidth's fallback for an over-width line with
// no "(cost=...)"-shaped group to split — e.g. "Settings: search_path =
// '"$user", public, chinook, ...', enable_seqscan = 'off'" (EXPLAIN's own
// SETTINGS line, or a long "Output:"/"Group Key:" list of columns) —
// greedily packs ", "-separated items onto each line up to maxWidth,
// same indented-continuation convention as the cost-group split above
// (four spaces past the line's own leading indentation), only breaking at
// an actual ", " boundary so no identifier or value is ever split
// mid-token. A line with fewer than two ", "-separated items (nothing to
// break on) passes through unchanged, same as a non-EXPLAIN line with no
// parenthetical group.
func wrapByCommas(line string, maxWidth int) []string {
	parts := strings.Split(line, ", ")
	if len(parts) < 2 {
		return []string{line}
	}
	contIndent := leadingWhitespace(line) + "    "
	var out []string
	cur := parts[0]
	for _, p := range parts[1:] {
		candidate := cur + ", " + p
		if utf8.RuneCountInString(candidate) > maxWidth && cur != "" {
			out = append(out, cur+",")
			cur = contIndent + p
		} else {
			cur = candidate
		}
	}
	out = append(out, cur)
	return out
}

func leadingWhitespace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

// reindentHeader re-centers psql's own "QUERY PLAN" title line and
// resizes its separator line (a run of U+2550 "═", this repo's own
// psqlrc box-drawing convention — see build/psql/psqlrc) to match
// whatever width the plan's content lines actually ended up at after the
// loop above split their "(cost=...)"/"(actual...)" groups onto their
// own continuation lines. Splitting those groups is the whole point —
// every content line is now narrower than psql originally laid it out
// for — so left as-is, the header/separator (sized for the ORIGINAL,
// wider plan) would overhang every line beneath them, no longer
// centering anything or marking the real table width.
//
// The title+separator pair isn't always lines[0]/lines[1] — a query
// preceded by \set/SET/BEGIN (e.g. one that disables enable_seqscan for
// the duration of the EXPLAIN) prints that statement's own "SET"/"BEGIN"
// echo first, pushing QUERY PLAN down a line or more — so this scans for
// the pair wherever it actually starts rather than assuming the very
// first two lines. No-op if no such pair is found at all (a raw
// non-EXPLAIN psql output).
func reindentHeader(lines []string) {
	for i := 0; i+1 < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "QUERY PLAN" || !isSeparatorLine(lines[i+1]) {
			continue
		}
		title := strings.TrimSpace(lines[i])
		width := utf8.RuneCountInString(title)
		for _, l := range lines[i+2:] {
			if n := utf8.RuneCountInString(l); n > width {
				width = n
			}
		}
		lines[i] = centerText(title, width)
		lines[i+1] = strings.Repeat(explainSeparatorRune, width)
		return
	}
}

const explainSeparatorRune = "═"

// isSeparatorLine reports whether s (trailing newline already stripped
// by strings.Split) is entirely explainSeparatorRune repeated — psql's
// own header-separator shape — tolerant of nothing else, since that's
// exactly what psql emits for this line and nothing else should ever
// match it.
func isSeparatorLine(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if string(r) != explainSeparatorRune {
			return false
		}
	}
	return true
}

// centerText pads s with spaces to width, extra padding (if width-len(s)
// is odd) going on the right — same convention psql itself uses to
// center "QUERY PLAN" over the separator line's width.
func centerText(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	total := width - n
	left := total / 2
	right := total - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}
