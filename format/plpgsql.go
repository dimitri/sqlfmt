package format

import (
	"strings"
)

// PL/pgSQL body formatting.
//
// The scope here is smaller than the language's 109 keywords suggest, and
// PostgreSQL's own grammar says why: pl_gram.y hands every expression,
// every embedded query and every assignment right-hand side to
// read_sql_construct / read_sql_stmt, which scans to a terminator and
// passes the text to the SQL parser. PL/pgSQL itself only ever parses the
// statement skeleton. A formatter can draw the same line: recognise the
// constructs that nest, indent them, and hand everything between them to
// Format.
//
// So this file is a block-structure recogniser, not a PL/pgSQL parser. It
// tracks the ~10 constructs that open and close a block and indents by
// depth; the other statement types need no layout beyond the current
// indent.

// plIndent is the indent step for a nested PL/pgSQL block.
const plIndent = 2

// plOpeners are the words that open a nested block. "case" is absent: a
// CASE inside a PL/pgSQL body is far more often the SQL expression, which
// belongs to the statement Format handles, than the PL/pgSQL CASE
// statement, and getting that wrong indents the rest of the function.
var plOpeners = map[string]bool{
	"begin": true, "declare": true, "loop": true, "if": true,
	"while": true, "for": true, "foreach": true, "exception": true,
}

// plClosers close the innermost block. "end" also closes with a suffix
// ("end if", "end loop", "end case"), which is handled by matching on the
// first word.
var plClosers = map[string]bool{"end": true}

// plMidBlock are the words that dedent for their own line and re-indent
// after it -- the ELSE arm of an IF, the EXCEPTION arm of a BEGIN block.
var plMidBlock = map[string]bool{
	"else": true, "elsif": true, "elseif": true, "exception": true,
}

// FormatPlpgsql lays out a PL/pgSQL function body. Statements are indented
// by block depth, and every statement that is not part of the PL/pgSQL
// skeleton is handed to Format, which is what makes an embedded query come
// out in the same house style as a query anywhere else.
//
// It returns ok=false when the body does not look like PL/pgSQL it can
// safely lay out -- an unbalanced skeleton, most likely because something
// in it is not what this recogniser thinks it is. The caller then leaves
// the body exactly as the author wrote it, which is always a correct
// answer for a formatter.
func FormatPlpgsql(body string, indent int) (string, bool) {
	stmts, ok := splitPlStatements(body)
	if !ok {
		return "", false
	}
	// An explicit stack rather than a depth counter, because the block
	// words do not nest uniformly: DECLARE ... BEGIN ... END is one block,
	// not two, so BEGIN after a DECLARE replaces it rather than opening a
	// level of its own. A counter got that wrong and left the body
	// unbalanced at the end.
	var stack []string
	var out []string

	col := func() int { return indent + len(stack)*plIndent }

	for _, st := range stmts {
		switch {
		case st.blank:
			out = append(out, "")
			continue
		case st.comment != "":
			out = append(out, strings.Repeat(" ", col())+st.comment)
			continue
		}

		lead := strings.ToLower(firstWord(st.text))
		switch {
		case lead == "end":
			if len(stack) == 0 {
				return "", false
			}
			stack = stack[:len(stack)-1]
			out = append(out, renderPlStatement(st.text, col())...)

		case lead == "begin":
			// Closes a preceding DECLARE section and opens the body at the
			// same level.
			if len(stack) > 0 && stack[len(stack)-1] == "declare" {
				stack = stack[:len(stack)-1]
			}
			out = append(out, renderPlStatement(st.text, col())...)
			stack = append(stack, "block")

		case plMidBlock[lead]:
			// ELSE / ELSIF / EXCEPTION dedent for their own line, then the
			// arm they introduce is indented again.
			if len(stack) == 0 {
				return "", false
			}
			stack = stack[:len(stack)-1]
			out = append(out, renderPlStatement(st.text, col())...)
			stack = append(stack, "arm")

		case lead == "declare":
			out = append(out, renderPlStatement(st.text, col())...)
			stack = append(stack, "declare")

		case plEndAtThen[lead] || plEndAtLoop[lead] || lead == "loop":
			out = append(out, renderPlStatement(st.text, col())...)
			stack = append(stack, lead)

		default:
			out = append(out, renderPlStatement(st.text, col())...)
		}
	}
	if len(stack) != 0 {
		return "", false
	}
	return strings.Join(out, "\n"), true
}

// renderPlStatement renders one PL/pgSQL statement at col. A statement the
// SQL formatter can parse -- every embedded query, which is most of a real
// function body -- is formatted by it and re-indented; the skeleton
// statements are emitted as written.
func renderPlStatement(text string, col int) []string {
	pad := strings.Repeat(" ", col)
	lead := strings.ToLower(firstWord(text))
	// ":=" is PL/pgSQL's assignment, and a variable declaration in a
	// DECLARE section has the same shape. Neither is SQL: handing them to
	// the SQL formatter lexes the ":" as a parameter marker and splits the
	// operator into ": =".
	if strings.Contains(text, ":=") {
		return []string{pad + collapseSpaces(text)}
	}
	// PERFORM's argument is a query -- "perform expr" is "select expr" with
	// the result discarded -- and RETURN QUERY's likewise. Format them as
	// the SELECT they are, then put the PL/pgSQL keyword back, so an
	// embedded query gets the same layout as a query anywhere else instead
	// of running to 300 columns on one line.
	if rest, kw, ok := plQueryStatement(text); ok {
		if lines, ok := formatAsSelect(rest, kw, pad); ok {
			return lines
		}
		return []string{pad + collapseSpaces(text)}
	}
	if plSkeleton[lead] {
		return []string{pad + collapseSpaces(text)}
	}
	formatted, err := Format(strings.NewReader(text))
	if err != nil {
		return []string{pad + collapseSpaces(text)}
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(formatted, "\n"), "\n") {
		if l == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, pad+l)
	}
	if len(lines) == 0 {
		return []string{pad + collapseSpaces(text)}
	}
	return lines
}

// plSkeleton are the statement leaders that belong to PL/pgSQL rather than
// to SQL, and so must not be handed to the SQL formatter. Everything else
// -- SELECT, INSERT, UPDATE, DELETE, WITH -- is SQL and is formatted as
// such. The list is pl_gram.y's statement set minus the ones that are
// plain SQL.
var plSkeleton = map[string]bool{
	"begin": true, "declare": true, "end": true, "if": true, "elsif": true,
	"elseif": true, "else": true, "loop": true, "while": true, "for": true,
	"foreach": true, "exception": true, "when": true, "return": true,
	"raise": true, "perform": true, "execute": true, "get": true,
	"open": true, "close": true, "fetch": true, "move": true, "exit": true,
	"continue": true, "assert": true, "null": true, "call": true,
	"commit": true, "rollback": true,
}

type plStatement struct {
	text    string
	comment string
	blank   bool
}

// plStandalone are block words that form a statement on their own, with no
// terminator of their own. PL/pgSQL's skeleton is not semicolon-delimited
// the way its statements are -- BEGIN, DECLARE, ELSE and LOOP each end at
// the end of their own word -- so a splitter that only cut on ";" glued
// them onto whatever followed.
var plStandalone = map[string]bool{
	"begin": true, "declare": true, "else": true, "loop": true,
	"exception": true,
}

// plEndAtThen are the block openers whose header runs to a keyword rather
// than to a semicolon: "if <cond> then", "elsif <cond> then". plEndAtLoop
// is the same for "for ... loop" / "while ... loop" / "foreach ... loop".
var plEndAtThen = map[string]bool{"if": true, "elsif": true, "elseif": true}
var plEndAtLoop = map[string]bool{"for": true, "while": true, "foreach": true}

// splitPlStatements splits a PL/pgSQL body into statements. Statements end
// at a top-level ";"; block words end at themselves or at their own
// terminating keyword (THEN, LOOP). Comments and blank lines are kept as
// their own entries so they survive where the author put them.
//
// It reports false on an unterminated string or dollar-quote, which is the
// signal that this body is not something to take apart.
func splitPlStatements(body string) ([]plStatement, bool) {
	var out []plStatement
	var cur strings.Builder
	sawNewline := false

	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			out = append(out, plStatement{text: t})
		}
		cur.Reset()
	}
	// pendingWord reports the word the buffer would start with, so a block
	// word can be recognised the moment it completes.
	curLead := func() string { return strings.ToLower(firstWord(cur.String())) }

	i := 0
	for i < len(body) {
		c := body[i]
		switch {
		case c == '\n' || c == '\t' || c == ' ':
			if c == '\n' && strings.TrimSpace(cur.String()) == "" {
				cur.Reset()
				// One blank entry per run of empty lines, and never for
				// the newline that merely ends a statement -- otherwise
				// every statement gets a blank line after it.
				if sawNewline {
					out = append(out, plStatement{blank: true})
				}
				sawNewline = true
				i++
				continue
			}
			cur.WriteByte(' ')
			i++
			continue

		case c == '-' && i+1 < len(body) && body[i+1] == '-':
			j := strings.IndexByte(body[i:], '\n')
			if j < 0 {
				j = len(body) - i
			}
			cmt := strings.TrimRight(body[i:i+j], " \t")
			if strings.TrimSpace(cur.String()) == "" {
				cur.Reset()
				out = append(out, plStatement{comment: cmt})
			} else {
				cur.WriteString(" " + cmt)
			}
			i += j
			continue

		case c == '\'':
			j := i + 1
			for j < len(body) {
				if body[j] == '\'' {
					if j+1 < len(body) && body[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= len(body) {
				return nil, false
			}
			cur.WriteString(body[i : j+1])
			i = j + 1
			continue

		case c == '$' && isDollarQuoteStart([]byte(body), i):
			// A nested dollar-quoted string -- an inner anonymous block, a
			// quoted body -- is opaque: copy it whole rather than looking
			// for terminators inside it.
			rest := body[i:]
			k := 1
			for k < len(rest) && isIdentCont(rest[k]) && rest[k] != '$' {
				k++
			}
			if k >= len(rest) {
				return nil, false
			}
			delim := rest[:k+1]
			end := strings.Index(rest[len(delim):], delim)
			if end < 0 {
				return nil, false
			}
			whole := rest[:len(delim)+end+len(delim)]
			cur.WriteString(whole)
			i += len(whole)
			continue

		case c == ';':
			cur.WriteByte(';')
			flush()
			sawNewline = false
			i++
			continue
		}

		// Read a bare word, so block keywords can be spotted.
		if isIdentStart(c) {
			j := i
			for j < len(body) && isIdentCont(body[j]) {
				j++
			}
			word := body[i:j]
			lw := strings.ToLower(word)
			bufEmpty := strings.TrimSpace(cur.String()) == ""

			// A block word at the start of a statement stands alone.
			if bufEmpty && plStandalone[lw] {
				out = append(out, plStatement{text: word})
				sawNewline = false
				i = j
				continue
			}
			// "end", with its optional "if"/"loop"/"case" suffix, up to ";".
			cur.WriteString(word)
			lead := curLead()
			if !bufEmpty || !plEndAtThen[lw] {
				// "if <cond> then" / "for ... loop" terminate on their own
				// keyword, wherever it appears in the header.
				if (plEndAtThen[lead] && lw == "then") || (plEndAtLoop[lead] && lw == "loop") {
					flush()
					sawNewline = false
				}
			}
			i = j
			continue
		}

		cur.WriteByte(c)
		i++
	}
	flush()
	return out, true
}

// plQueryStatement recognises a PL/pgSQL statement whose payload is a
// query, returning that query with a "select" substituted for the PL/pgSQL
// keyword, plus the keyword to restore afterwards.
func plQueryStatement(text string) (rewritten, keyword string, ok bool) {
	t := strings.TrimSpace(text)
	low := strings.ToLower(t)
	switch {
	case strings.HasPrefix(low, "perform "):
		return "select " + t[len("perform "):], "perform", true
	case strings.HasPrefix(low, "return query "):
		return "select " + t[len("return query "):], "return query", true
	}
	return "", "", false
}

// formatAsSelect formats a rewritten query and swaps the leading "select"
// back for the PL/pgSQL keyword, padding the following lines so the query
// body still lines up under its own river.
func formatAsSelect(query, keyword, pad string) ([]string, bool) {
	formatted, err := Format(strings.NewReader(query))
	if err != nil {
		return nil, false
	}
	lines := strings.Split(strings.TrimRight(formatted, "\n"), "\n")
	if len(lines) == 0 {
		return nil, false
	}
	// The river put "select" at some column; replacing it with a longer
	// keyword shifts only that line, so shift the rest to match.
	idx := strings.Index(strings.ToLower(lines[0]), "select")
	if idx < 0 {
		return nil, false
	}
	shift := len(keyword) - len("select")
	out := []string{pad + lines[0][:idx] + keyword + lines[0][idx+len("select"):]}
	for _, l := range lines[1:] {
		if shift > 0 {
			out = append(out, pad+strings.Repeat(" ", shift)+l)
		} else {
			out = append(out, pad+l)
		}
	}
	return out, true
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == ';' {
			return s[:i]
		}
	}
	return s
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
