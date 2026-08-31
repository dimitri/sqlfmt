package format

// Top-level binary operators as break points.
//
// splitTopLevelConcat handled "||" and nothing else, so an expression
// whose outermost structure was any other operator arrived at
// exprAtomDoc as one unbreakable Text:
//
//	count(*) filter(where action = 'rt') - count(*) filter(where action = 'de-rt') as rts
//
// The "-" is the only sensible break in that line and the layer could not
// see it. Splitting there is the single largest remaining source of
// over-margin select-list items in the corpus.

// binaryLevels are the operators to break at, lowest binding power first,
// so a chain breaks at its loosest joint: "a - b * c" breaks at "-", not
// at "*".
//
// "and"/"or" are deliberately absent. They are clause structure, and the
// river above this layer already breaks a WHERE at them; competing for
// the same break point would produce two different alignments for the
// same operator. "::" is absent because a cast binds tighter than
// anything here and splitTrailingCast peels it instead.
var binaryLevels = [][]string{
	{"||"},
	{"+", "-"},
	{"*", "/", "%"},
}

// predicateLevels adds comparison to binaryLevels, for a run the caller
// knows is a condition rather than a value: a WHERE or a JOIN ... ON.
//
// Comparison cannot go in binaryLevels itself, because an UPDATE's
//
//	set de_favs = case when ... end
//
// is a select-list-shaped item whose "=" is clause structure, not an
// operator: breaking there gives "de_favs" a line of its own under a
// hanging "=", which is not what an assignment means. The two shapes are
// indistinguishable from the tokens alone, so the caller, which knows
// which clause it is in, picks the set.
var predicateLevels = append([][]string{
	{"=", "<>", "!=", "<", ">", "<=", ">="},
}, binaryLevels...)

// endsOperand reports whether t can be the last token of an operand, which
// is what distinguishes a binary operator from a unary sign: the "-" in
// "a - b" follows an identifier, the one in "(-1)" follows "(".
func endsOperand(t Token) bool {
	switch t.Kind {
	case TokIdent, TokNumber, TokString, TokDollarString, TokParam:
		return true
	}
	switch t.Text {
	case ")", "]", "*":
		return true
	}
	// "end" closes a CASE, and a keyword like "null" or "true" is a value.
	return t.Kind == TokKeyword && (t.Lower == "end" || t.Lower == "null" ||
		t.Lower == "true" || t.Lower == "false")
}

// splitTopLevelBinary cuts toks at every occurrence of the loosest-binding
// operator present at paren depth zero, returning the operands and the
// operators between them. CASE spans are stepped over whole: an operator
// inside a branch belongs to that branch, and splitting there would leave
// the CASE without its END.
func splitTopLevelBinary(toks []Token, levels [][]string) ([][]Token, []string) {
	for _, level := range levels {
		want := make(map[string]bool, len(level))
		for _, op := range level {
			want[op] = true
		}
		var parts [][]Token
		var ops []string
		depth, start := 0, 0
		for i := 0; i < len(toks); i++ {
			t := toks[i]
			if t.Kind == TokKeyword && t.Lower == "case" {
				if end := matchCaseEnd(toks, i); end > i {
					i = end
					continue
				}
			}
			switch t.Text {
			case "(":
				depth++
				continue
			case ")":
				depth--
				continue
			}
			if depth != 0 || !want[t.Text] || i == start {
				continue
			}
			if !endsOperand(toks[i-1]) {
				continue
			}
			parts = append(parts, toks[start:i])
			ops = append(ops, t.Text)
			start = i + 1
		}
		if len(parts) > 0 && start < len(toks) {
			return append(parts, toks[start:]), ops
		}
	}
	return nil, nil
}
