package format

import (
	"regexp"
	"strings"
)

// commentWidth is the reflow target for comment text (STYLE.md rule 17's
// line-length target applies to comments too).
const commentWidth = 80

// commentMarker is a sentinel inserted between a rendered line's real
// content and a pending trailing comment. alignTrailingComments (run once,
// over the whole formatted statement) replaces it with padding that lines
// up every trailing comment in a contiguous run at a shared column, per
// STYLE.md rule 18. Chosen because it can never appear in real SQL source
// (the lexer never produces it).
const commentMarker = "\x00"

var dashOnlyRe = regexp.MustCompile(`^-+$`)

// attachComments walks the full lexed token stream once and returns an
// equivalent stream with every comment token removed and re-expressed as
// metadata on a neighboring real token: a comment on the same source line
// as the preceding real token becomes that token's TrailingComment: any
// other comment becomes a leading Comments entry on the next real token.
//
// This is what makes it safe for the rest of the layout engine to never see
// a raw TokLineComment/TokBlockComment again -- previously, comments were
// left in the token stream and just echoed inline wherever encountered,
// which for a "--" comment silently absorbed every following token on the
// same physical output line into the comment text (a real correctness bug:
// `select id -- inline\nfrom users` rendered as `select id -- inline from
// users`, an entirely different query).
//
// Comments with no following real token (trailing content at EOF) have
// nowhere to attach; those are returned separately in eofComments, in
// source order, for the caller to render as a trailing block.
func attachComments(toks []Token) (out []Token, eofComments []Token) {
	var pending []Token
	var lastReal *Token

	flushPendingAsEOF := func() {
		eofComments = append(eofComments, pending...)
		pending = nil
	}

	for i := range toks {
		t := toks[i]
		switch t.Kind {
		case TokLineComment, TokBlockComment:
			// A multi-line block comment starting on the same source line
			// as the preceding real token (e.g. "from /*\n * ...\n */") is
			// not a short same-line annotation the way a "--" comment or a
			// one-line "/* ... */" is; treat it as leading on whatever
			// comes next instead. This also sidesteps a real gap: a clause
			// keyword token (e.g. "from") is never retained anywhere past
			// splitClauses (clauseSeg keeps only its name), so a
			// TrailingComment attached to it would be silently
			// unreachable by any renderer.
			sameLine := lastReal != nil && t.Line == lastReal.Line && lastReal.TrailingComment == nil
			if sameLine && strings.Contains(t.Text, "\n") {
				sameLine = false
			}
			if sameLine {
				c := t
				lastReal.TrailingComment = &c
				continue
			}
			pending = append(pending, t)
		case TokEOF:
			flushPendingAsEOF()
			out = append(out, t)
		default:
			if len(pending) > 0 {
				t.Comments = pending
				pending = nil
			}
			out = append(out, t)
			lastReal = &out[len(out)-1]
		}
	}
	return out, eofComments
}

// isDashOnly reports whether a line-comment token's full text is nothing
// but dashes (e.g. "--", "-----------") -- a visual divider, preserved
// verbatim rather than reflowed as prose.
func isDashOnly(text string) bool {
	return dashOnlyRe.MatchString(text)
}

// renderLeadingComments renders a token's attached leading comment group as
// complete, standalone lines at the given indent: line comments reflowed to
// commentWidth (blank source lines and dash-only divider lines preserved,
// not merged into surrounding prose), block comments rewrapped into the
// "/*" / " * text" / " */" C style.
func renderLeadingComments(comments []Token, indent int) []string {
	var out []string
	for i, c := range comments {
		if i > 0 && c.LeadingBlank {
			out = append(out, "")
		}
		switch c.Kind {
		case TokBlockComment:
			out = append(out, formatBlockComment(c, indent)...)
		default:
			out = append(out, formatLineComment(c, indent)...)
		}
	}
	return out
}

// formatLineComment renders one "--" comment token as one or more reflowed
// lines. Dash-only dividers are preserved verbatim (just re-indented).
func formatLineComment(c Token, indent int) []string {
	if isDashOnly(c.Text) {
		return []string{strings.Repeat(" ", indent) + c.Text}
	}
	content := strings.TrimSpace(strings.TrimPrefix(c.Text, "--"))
	if content == "" {
		return []string{strings.Repeat(" ", indent) + "--"}
	}
	return wrapWords(strings.Fields(content), indent, "-- ")
}

// formatBlockComment rewraps a "/* ... */" token into the C-style form: an
// opening line with only "/*", each content line starting with a "*"
// aligned under "/*"'s own "*" (its second character), and a closing line
// with only "*/" similarly aligned. Blank lines inside the original
// comment are preserved as paragraph breaks; any pre-existing "*" bullet
// prefix per line is stripped before rewrapping, so re-running the
// formatter on its own output is a no-op.
func formatBlockComment(c Token, indent int) []string {
	inner := strings.TrimSuffix(strings.TrimPrefix(c.Text, "/*"), "*/")
	out := []string{strings.Repeat(" ", indent) + "/*"}

	var para []string
	pendingBlank := false
	flush := func() {
		if len(para) == 0 {
			return
		}
		out = append(out, wrapWords(para, indent, " * ")...)
		para = nil
	}
	for _, l := range strings.Split(inner, "\n") {
		l = strings.TrimSpace(l)
		l = strings.TrimPrefix(l, "*")
		l = strings.TrimSpace(l)
		if l == "" {
			if len(para) > 0 {
				flush()
				pendingBlank = true
			}
			continue
		}
		// The blank output line is only inserted once it's confirmed there
		// really is another paragraph following it -- otherwise a
		// perfectly ordinary blank line right before the comment's own
		// closing "*/" (every block comment has one) would read as a
		// trailing empty paragraph that was never actually there.
		if pendingBlank {
			out = append(out, "")
			pendingBlank = false
		}
		para = append(para, strings.Fields(l)...)
	}
	flush()
	out = append(out, strings.Repeat(" ", indent)+" */")
	return out
}

// wrapWords greedily word-wraps words onto lines of at most commentWidth
// columns, each line indented spaces then prefix (e.g. "-- " or " * ").
func wrapWords(words []string, indent int, prefix string) []string {
	var lines []string
	var cur []string
	curLen := indent + len(prefix)
	for _, w := range words {
		add := len(w)
		if len(cur) > 0 {
			add++ // separating space
		}
		if curLen+add > commentWidth && len(cur) > 0 {
			lines = append(lines, strings.Repeat(" ", indent)+prefix+strings.Join(cur, " "))
			cur = nil
			curLen = indent + len(prefix)
		}
		if len(cur) > 0 {
			curLen++
		}
		cur = append(cur, w)
		curLen += len(w)
	}
	if len(cur) > 0 {
		lines = append(lines, strings.Repeat(" ", indent)+prefix+strings.Join(cur, " "))
	}
	return lines
}

// trailingCommentText renders a token's TrailingComment (if any) as inline
// comment text with no leading padding of its own -- the caller appends
// commentMarker + this to the line under construction; alignTrailingComments
// supplies the padding later.
func trailingCommentText(tc *Token) string {
	if tc == nil {
		return ""
	}
	if tc.Kind == TokBlockComment {
		// A block comment trailing on the same line as code can't be
		// re-flowed into the multi-line C style without breaking that
		// line's structure; flatten it to a single line instead.
		flat := strings.Join(strings.Fields(tc.Text), " ")
		return flat
	}
	return tc.Text
}

// alignTrailingComments replaces each commentMarker with padding so every
// trailing comment in a maximal run of consecutive comment-bearing lines
// starts at the same column (STYLE.md rule 18), then strips the marker.
func alignTrailingComments(text string) string {
	lines := strings.Split(text, "\n")
	i := 0
	for i < len(lines) {
		if !strings.Contains(lines[i], commentMarker) {
			i++
			continue
		}
		j := i
		maxLen := 0
		for j < len(lines) && strings.Contains(lines[j], commentMarker) {
			content := strings.SplitN(lines[j], commentMarker, 2)[0]
			if len(content) > maxLen {
				maxLen = len(content)
			}
			j++
		}
		col := maxLen + 2
		for k := i; k < j; k++ {
			parts := strings.SplitN(lines[k], commentMarker, 2)
			pad := col - len(parts[0])
			if pad < 1 {
				pad = 1
			}
			lines[k] = parts[0] + strings.Repeat(" ", pad) + parts[1]
		}
		i = j
	}
	return strings.Join(lines, "\n")
}
