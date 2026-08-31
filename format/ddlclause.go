package format

import "strings"

// Statement-level DDL whose continuation clauses the corpus writes
// indented under the header rather than rivered.
//
// layoutDDL's river suits CREATE FUNCTION and CREATE INDEX, where the
// clause keywords are short and of similar length. These statements are
// written differently by hand:
//
//	create table lab.invoice_2022
//	  partition of lab.invoice_by_year
//	  for values from (2022) to (2023);
//
//	create database chinook_prod
//	  owner chinook_owner
//	  encoding 'UTF8'
//
//	grant select (invoice_id, customer_id, invoice_date,
//	              billing_address, billing_city, total)
//	  on chinook.invoice to chinook_ro;
//
// so they get the same shape: header, then one clause per line at indent
// 2. Without any of this they fell to flatJoin -- the GRANT above came
// back as a single 205-column line, and the partitioned tables at 95-96
// columns each.

// clauseStarter is a sequence of words that opens a continuation clause.
type clauseStarter []string

var (
	partitionClauses = []clauseStarter{
		{"partition", "of"}, {"for", "values"}, {"default"}, {"tablespace"},
	}
	databaseClauses = []clauseStarter{
		{"owner"}, {"template"}, {"encoding"}, {"strategy"}, {"locale"},
		{"lc_collate"}, {"lc_ctype"}, {"tablespace"}, {"allow_connections"},
		{"connection", "limit"}, {"is_template"}, {"oid"},
	}
	grantClauses = []clauseStarter{
		{"on"}, {"to"}, {"from"}, {"with", "grant", "option"}, {"granted", "by"},
	}
	defaultPrivClauses = []clauseStarter{
		{"in", "schema"}, {"grant"}, {"revoke"}, {"on"}, {"to"}, {"from"},
	}
	// Inside one ALTER TABLE subcommand: "add constraint c check (...) not
	// valid" is three lines by hand, not one.
	alterInnerClauses = []clauseStarter{
		{"check"}, {"not", "valid"}, {"using"}, {"references"},
		{"for", "values"}, {"default"},
	}
)

// matchesAt reports whether cs's words sit at toks[i:].
func (cs clauseStarter) matchesAt(toks []Token, i int) bool {
	if i+len(cs) > len(toks) {
		return false
	}
	for k, w := range cs {
		if toks[i+k].Lower != w {
			return false
		}
	}
	return true
}

// clauseIndexes returns the token indexes at paren depth zero where one of
// starts begins, skipping index 0 -- a clause word that opens the
// statement is part of its header, not a continuation.
func clauseIndexes(toks []Token, starts []clauseStarter) []int {
	var out []int
	depth := 0
	for i := range toks {
		switch toks[i].Text {
		case "(":
			depth++
			continue
		case ")":
			depth--
			continue
		}
		if depth != 0 || i == 0 {
			continue
		}
		for _, cs := range starts {
			if cs.matchesAt(toks, i) {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// layoutIndentedClauses renders toks as a header line followed by one
// clause per line at the given indent, or as a single line when it fits.
func layoutIndentedClauses(toks []Token, starts []clauseStarter, indent int) ([]string, bool) {
	toks = trimTokens(toks)
	if !hasLineComment(toks) {
		if flat := flatJoin(toks); cols(flat) <= targetWidth-1 {
			return []string{flat}, true
		}
	}
	idx := clauseIndexes(toks, starts)
	if len(idx) == 0 {
		return nil, false
	}
	pad := strings.Repeat(" ", indent)
	lines := renderRun(toks[:idx[0]], 0)
	for n, at := range idx {
		end := len(toks)
		if n+1 < len(idx) {
			end = idx[n+1]
		}
		sub := renderRun(trimTokens(toks[at:end]), indent)
		lines = append(lines, pad+sub[0])
		lines = append(lines, sub[1:]...)
	}
	return lines, true
}
