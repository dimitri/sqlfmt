# Design notes

## Architecture decision: token-stream formatter, not AST-based

Two real, mature options exist for getting a genuine PostgreSQL-grade parse
of SQL from Go (research below) — but the recommendation is to **not** build
the core formatting engine on top of either, for reasons specific to what a
*formatter* needs versus what a *query analyzer* needs. Build a hand-written
tokenizer + lightweight paren-depth/clause-tracking layout engine instead
(no formal grammar, no AST). Reasoning:

1. **Comments.** PostgreSQL's own parser discards comments during parsing —
   they never make it into a parse tree at all. Rule 18 in `STYLE.md`
   requires the formatter to preserve and reposition comments (header
   comments, aligned trailing comments). An AST built from either library
   below would need a *separate* comment-recovery pass reconciling original
   source positions against the AST anyway — at which point most of the
   value of "let the parser handle structure" is already gone, since you
   still need a token-stream/position-aware pass as the source of truth for
   layout.

2. **The style is about token layout, not deep semantics.** River-alignment,
   line-breaking, and re-indentation are fundamentally decisions about where
   tokens go on the page, not about the query's semantic structure. A formal
   AST is generic (built for query *analysis*: rewriting, validation,
   optimization) and would need to be fought back into this specific,
   idiosyncratic hand-crafted alignment style regardless — the "layout
   engine" work is roughly the same size whether it reads from a hand-rolled
   token stream or from a foreign AST's node types. This is also why mature
   production SQL formatters (`pg_format`, `sqlfluff`, `prettier-plugin-sql`)
   are built this way rather than as AST pretty-printers.

3. **Robustness to partial/pasted input.** The website widget (JS side, see
   the parent conversation this repo was spun out of) needs to gracefully
   handle whatever a visitor pastes — not always a complete, syntactically
   perfect statement. A token-stream approach degrades gracefully; a formal
   parser either accepts or rejects.

### What the two real parser libraries are still good for

Found via research (not yet vetted hands-on — verify before depending on
either):

- **[`pganalyze/pg_query_go`](https://github.com/pganalyze/pg_query_go)** —
  wraps `libpg_query` (the actual PostgreSQL C parser, extracted as a
  standalone library) via cgo. Returns a protobuf AST with `Location` fields
  (character offsets into the original source). Mature, widely used
  (pganalyze's own product depends on it), tracks real Postgres versions.
  Also ships `Fingerprint()`/`Normalize()` — a semantic hash that ignores
  formatting and literal values.

  **Best use here: a correctness oracle in the test suite, not the runtime
  engine.** `Fingerprint(input) == Fingerprint(sqlfmt.Format(input))` is a
  strong, cheap regression check — confirms formatting never silently
  changes what a query *means*, using the actual Postgres parser as ground
  truth. Requires cgo (build-time C compilation of parts of Postgres,
  reportedly ~3 minutes) — acceptable for a test-only dependency, more
  friction as a runtime dependency for a `go install`-able CLI users expect
  to be a fast, simple static binary.

- **[`pgplex/pgparser`](https://github.com/pgplex/pgparser)** — directly
  answers the "Flex/Bison-compatible Go package, ported from Postgres's real
  grammar" question: it's a `goyacc` port of Postgres's actual `gram.y`, with
  `scan.l`'s lexer hand-reimplemented in Go. Pure Go, no cgo, targets
  PG 17.7 (`REL_17_STABLE`), Apache-2.0, claims 99.6% pass rate against
  Postgres's own regression suite (~45k statements). Confirms this kind of
  port is *possible* without redoing Postgres's own multi-year grammar work
  from scratch — but it's a newer/smaller project (31 stars, 34 commits at
  time of research), and its docs don't mention comment-preservation or
  lossless source-position round-tripping, which is the actual hard part for
  a formatter (see point 1 above). Not recommended as the core engine for
  the reasons above, but worth knowing about, and worth a second look if the
  token-stream approach hits real correctness limits on exotic syntax.

- Passed over: `auxten/postgresql-parser` / `cockroachdb-parser` — pure Go,
  goyacc-based, but is CockroachDB's own grammar (forked from Postgres at
  v20.1.11 and diverged since), not Postgres's actual current grammar. Not a
  precise match for "the Postgres parser."

If robustness against obscure PostgreSQL syntax becomes a real problem for
the hand-written tokenizer later, `pg_query_go`'s `Fingerprint` safety net
(point above) is the first thing to add — it catches semantic-meaning bugs
cheaply without requiring a rewrite of the formatting engine itself.

## Module layout

```
sqlfmt.go        — package sqlfmt: Format(io.Reader) (string, error), the public API
lexer.go         — tokenizer: strings ('...' with '' escaping), dollar-quoting
                   ($$...$$ / $tag$...$tag$), identifiers (quoted "..." and bare),
                   numbers, multi-char operators (::, ||, ->, ->>, @>, <@, etc.),
                   comments (-- and /* */ block, with nesting awareness since
                   Postgres block comments do nest), parens/brackets, commas,
                   semicolons, keywords (case-insensitive against a keyword table)
layout.go        — the layout engine:
                     - split input into top-level statements on semicolons,
                       respecting string/comment/paren/dollar-quote context
                     - track paren depth; each new depth (subquery, CTE body)
                       gets its own river-alignment computation (STYLE.md rule
                       "unifying rule")
                     - recognize clause keywords at the current statement's
                       depth-0: select/from/where/group by/having/order by/
                       insert into/update/set/delete/returning/with/values/
                       union [all]/intersect/except
                     - JOIN handling (STYLE.md rule 9)
                     - CTE handling (rule 13)
                     - window function OVER(...) wrapping (rule 14)
                     - CASE/WHEN/THEN/ELSE/END (rule 15, per-instance realignment)
                     - CREATE TABLE column-alignment sub-mode (rule 16, a
                       genuinely different formatting mode from the query
                       clauses — column-name-width alignment, not clause-keyword
                       alignment)
sqlfmt_test.go   — corpus round-trip test: format() every file under
                   testdata/corpus/, diff against the file's own content.
                   Files under testdata/excluded/ must NOT be included.
                   An env var (e.g. SQLFMT_CORPUS_DIR) can optionally point at
                   the full sibling corpus
                   (/Users/dim/dev/TAOP/TheArtOfPostgreSQL/queries) for deeper
                   local validation beyond the committed testdata/ subset —
                   skip that check gracefully when the env var is unset so CI
                   on other machines doesn't depend on a sibling checkout existing.
cmd/sqlfmt/
  main.go        — CLI, gofmt-shaped flags (see below)
```

## CLI spec (`cmd/sqlfmt`)

Modeled directly on `gofmt`:

- `sqlfmt` (no args) — read stdin, write formatted result to stdout.
- `sqlfmt file.sql` — print formatted output to stdout.
- `sqlfmt -w file.sql ...` — rewrite file(s) in place.
- `sqlfmt -l file.sql ...` — list files whose formatted output differs from
  their current content (don't print full content) — the CI-check flag.
- `sqlfmt -d file.sql` — print a unified diff instead of full content.
- Directory arguments: walk recursively for `*.sql` files (same idea as
  `gofmt`'s directory handling for `*.go`).
- Exit codes: 0 on success; non-zero on a real parse/format error. `-l`
  finding differing files is not itself a failure exit — same convention as
  `gofmt -l`, callers check whether its output is non-empty.

## Testing strategy

The corpus is unusually good test data: `testdata/corpus/` is *already*
correctly formatted (real book examples), so `Format(file) == file` should
hold for nearly every file. Any divergence is either a real formatter bug or
one of the documented ambiguous areas in `STYLE.md`'s summary table — worth
distinguishing explicitly in test failure output (e.g. tag known-divergent
files rather than silently skipping or silently failing on them).
`testdata/corpus/04-sql-select/16-sql-103/04_01.sql` is a known, deliberate
exception (see `STYLE.md`'s "unifying rule" section) — expect it not to
round-trip and don't try to make it pass.

## Open questions for whoever picks this up

- Go module path / GitHub org for publishing (placeholder used in README:
  `github.com/dimitri/sqlfmt`).
- License (should probably match the book's own code license — check what
  `TheArtOfPostgreSQL` itself uses).
- Whether to add the `pg_query_go` `Fingerprint` safety-net test once the
  core engine is stable (see architecture decision above) — not a blocker
  for a first working version.
- The JS implementation (for the website's SQL Formatter tool) is a
  deliberately separate, independent implementation — simpler, not a full
  parser, lower correctness bar than this Go engine. It is *not* planned to
  share code with this repo (see architecture decision above re: why a
  shared engine wasn't chosen — same reasoning applies to JS/Go as applied
  to the earlier Elisp-wrapper option that was considered and dropped for
  this project). Out of scope for this repo; happens separately.
