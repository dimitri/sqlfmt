package format

import (
	"bytes"
	"strings"
	"testing"
)

// twoWordLeaders are leading words that, on a rendered line, are always
// followed by a second word still part of the same keyword/JOIN phrase
// ("order by", "group by", "left join", ...).
var twoWordLeaders = map[string]bool{
	"left": true, "right": true, "full": true, "inner": true, "outer": true, "cross": true,
	"order": true, "group": true,
}

// riverEndCol returns the column (0-indexed) that the leading clause
// keyword (or JOIN phrase, e.g. "left join") on a rendered line ends at, or
// -1 for a blank line.
func riverEndCol(line string) int {
	trimmed := strings.TrimLeft(line, " ")
	if trimmed == "" {
		return -1
	}
	indent := len(line) - len(trimmed)
	fields := strings.Fields(trimmed)
	phrase := fields[0]
	if twoWordLeaders[phrase] && len(fields) > 1 {
		phrase += " " + fields[1]
	}
	return indent + len(phrase)
}

// TestJoinRiverAlignment is a regression test for a real formatter bug: a
// JOIN phrase longer than every other clause keyword (e.g. "left join")
// must widen the whole clause river so select/from/where/group by/order by
// all still end at the same column as the JOIN line -- not just pad itself
// wider in isolation while the other clause keywords stay narrow.
func TestJoinRiverAlignment(t *testing.T) {
	src := "select title, name from album left join track using(album_id) where album_id = 1 order by 2;"
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines (select/from/left join/where/order by), got %d:\n%s", len(lines), got)
	}

	var cols []int
	for _, l := range lines {
		col := riverEndCol(l)
		if col < 0 {
			t.Fatalf("line has no leading keyword: %q", l)
		}
		cols = append(cols, col)
	}
	for i := 1; i < len(cols); i++ {
		if cols[i] != cols[0] {
			t.Errorf("river misaligned: line %d (%q) ends its keyword at column %d, line 0 (%q) ends at column %d\nfull output:\n%s",
				i, lines[i], cols[i], lines[0], cols[0], got)
		}
	}
}
