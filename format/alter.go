package format

// alterSubcommands are the keywords that open a subcommand in the
// comma-separated list an ALTER TABLE carries. Everything before the first
// one is the header -- "alter table [if exists] [only] name".
var alterSubcommands = map[string]bool{
	"add": true, "drop": true, "alter": true, "rename": true,
	"set": true, "reset": true, "owner": true, "enable": true,
	"disable": true, "validate": true, "attach": true, "detach": true,
	"inherit": true, "no": true, "cluster": true, "replica": true,
	"of": true, "not": true, "force": true, "options": true,
}

// layoutAlter lays out ALTER TABLE, whose payload is a comma-separated
// list of subcommands -- exactly the shape layoutCommaList handles, but
// which fell through to the flat default and produced the corpus's single
// worst line, a 236-column ALTER with three "alter ... type ... using"
// clauses on it.
//
// The corpus's own hand formatting is a header line with the subcommands
// indented under it, one per line:
//
//	alter table chinook.invoice
//	  add constraint invoice_currency_notnull
//	      check (currency is not null)
//	      not valid;
//
// and a short one stays whole: "alter table t add column c int;".
func layoutAlter(toks []Token) []string {
	if len(toks) < 2 || toks[1].Lower != "table" {
		return flatStatementLines(toks)
	}
	if !hasLineComment(toks) {
		if flat := flatJoin(toks); cols(flat) <= targetWidth-1 {
			return []string{flat}
		}
	}
	head := alterHeaderEnd(toks)
	if head <= 0 || head >= len(toks) {
		return flatStatementLines(toks)
	}
	items := splitTopLevelComma(trimTokens(toks[head:]))
	if len(items) == 0 {
		return flatStatementLines(toks)
	}
	lines := []string{flatJoin(toks[:head])}
	for i, it := range items {
		it = trimTokens(it)
		if len(it) == 0 {
			continue
		}
		trailing := trailingCommentSuffix(it)
		sub := renderRun(it, 2)
		comma := ","
		if i == len(items)-1 {
			comma = ""
		}
		lines = append(lines, "  "+sub[0]+comma+trailing)
		lines = append(lines, sub[1:]...)
	}
	return lines
}

// alterHeaderEnd returns the index of the first subcommand token, i.e. the
// end of "alter table [if exists] [only] name".
func alterHeaderEnd(toks []Token) int {
	i := 2 // past "alter table"
	for i < len(toks) && (toks[i].Lower == "if" || toks[i].Lower == "exists" ||
		toks[i].Lower == "only") {
		i++
	}
	// The (possibly schema-qualified) table name, and an optional "*".
	if i >= len(toks) {
		return -1
	}
	i++
	for i+1 < len(toks) && toks[i].Text == "." {
		i += 2
	}
	if i < len(toks) && toks[i].Text == "*" {
		i++
	}
	if i >= len(toks) || toks[i].Kind != TokKeyword || !alterSubcommands[toks[i].Lower] {
		return -1
	}
	return i
}
