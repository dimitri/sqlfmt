# Excluded files

These look like corpus examples but are **not** representative of the
book's hand-formatting style — see `STYLE.md`'s intro for why. Keep them
here as documentation/negative-examples only; never use them as formatting
test fixtures (`go test` must not read this directory as input).

- `03-writing-sql-queries/08-sql-is-code/01_01.sql` — a deliberate "here's
  ugly SQL" pedagogical contrast example, later reformatted in the same
  chapter to demonstrate the style. Formatting *this* file to match the
  house style would defeat its own point.
- `06-data-modeling/29-normalisation/09_01.sql`, `09_02.sql`, `09_04.sql` —
  verbatim quotes of PostgreSQL's own documentation examples (the
  `CREATE TABLE products (... CHECK (price > 0) ...)` example), reproduced
  in the docs' own uppercase/4-space style, not the author's.
- `04-sql-select/19-relations/01_01.sql`,
  `03-writing-sql-queries/09-indexing-strategy/01_02.sql` — captured `psql`
  session transcripts (prompts, `(N rows)` footers), not hand-formatted
  query source.
