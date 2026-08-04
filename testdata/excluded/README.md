# Excluded files

These look like corpus examples but are **not** representative of the
book's hand-formatting style — see `STYLE.md`'s intro for why. Keep them
here as documentation/negative-examples only; never use them as formatting
test fixtures (`go test` must not read this directory as input).

- `sql-is-code-01_01.sql` — a deliberate "here's ugly SQL" pedagogical
  contrast example (from the book's "writing-sql-queries / sql-is-code"
  chapter), later reformatted in the same chapter to demonstrate the style.
  Formatting *this* file to match the house style would defeat its own
  point.
- `normalisation-09_01.sql`, `09_02.sql`, `09_04.sql` — verbatim quotes of
  PostgreSQL's own documentation examples (the
  `CREATE TABLE products (... CHECK (price > 0) ...)` example), reproduced
  in the docs' own uppercase/4-space style, not the author's.
- `relations-01_01.sql`, `indexing-strategy-01_02.sql`,
  `05-data-types-23-pg-data-types-101-06_03.sql` — captured `psql` session
  transcripts (prompts, `(N rows)` footers, box-drawing characters), not
  hand-formatted query source. The last one was originally miscategorized
  under `testdata/corpus/` and moved here after being found non-idempotent
  under `sqlfmt` (it isn't parseable SQL at all).

Filenames are `<book-chapter-slug>-<original-basename>`, flattened out of
the book's own chapter/section subdirectories (matching `testdata/corpus/`'s
layout) — the slug alone still identifies which chapter each example came
from.
