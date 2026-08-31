package format

import "unicode/utf8"

// cols is the display width of s in columns, which is what targetWidth
// and every column measure in the layout are expressed in. len() is a byte
// count, and the two part company the moment a line carries anything
// outside ASCII: a box-drawing rule of 66 characters measures 198 bytes,
// so a comment well inside the margin was wrapped as if it were far past
// it, and an accented identifier pulled its line's break several columns
// early.
//
// Counting runes rather than grapheme clusters or East Asian widths is
// deliberate: the corpus is Latin text with the occasional box-drawing
// character, where one rune is one column.
func cols(s string) int { return utf8.RuneCountInString(s) }
