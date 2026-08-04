# sqlfmt

A `gofmt`-style command-line formatter for PostgreSQL SQL, implementing the
specific hand-formatting convention used throughout Dimitri Fontaine's
*The Art of PostgreSQL* — a "river"-aligned style where clause keywords
(`select`/`from`/`where`/`group by`/`having`/`order by`) are right-padded so
they all end at the same column:

```sql
  select status, count(*)
    from results
         join races using(raceid)
   where date >= :season
group by status
  having count(*) >= 10
order by count(*) desc;
```

The goal is a tool that can be run automatically over SQL embedded in the
book and courses (so hand-pasted or generated examples always come out in
house style) and used standalone anywhere else SQL needs consistent
formatting — a real `go install`-able binary, not a website-only tool.

**Status: working implementation.** The formatting rules were reverse-engineered
from the book's own query corpus (see `STYLE.md`); the lexer, river-alignment
layout engine, `format.Format` library entry point, and `sqlfmt` CLI are all
implemented and covered by tests. The hardest, most hand-tuned areas of the
style (deeply nested subqueries, exotic upsert/DDL forms) remain best-effort,
as `STYLE.md` itself expects. See `DESIGN.md` for the architecture and open
engineering notes.

## Usage

Modeled directly on `gofmt`'s CLI, since that's a UX Go developers already know:

```console
$ sqlfmt query.sql              # print formatted output to stdout
$ sqlfmt -w query.sql           # rewrite the file in place
$ sqlfmt -l queries/**/*.sql    # list files whose formatting would change
$ sqlfmt -d query.sql           # show a unified diff instead of full output
$ cat query.sql | sqlfmt        # stdin -> stdout, pipeable
```

As a library: `import "github.com/dimitri/sqlfmt/format"` (module path TBD —
not yet published), `format.Format(io.Reader) (string, error)`, so callers
like `app.taop.xyz`'s `cmd/sqlbuild` book-build tool can format embedded
queries in-process without shelling out to the binary. The library lives in
its own `format/` subpackage (rather than at the module root) specifically
so it stays easily importable on its own, independent of the CLI.

## Repository layout

```
STYLE.md                  — the extracted formatting rules (the spec implemented against)
DESIGN.md                 — architecture, engineering notes, parser-library research
Makefile                   — build/test entry points
format/                    — package format, the library (import "github.com/dimitri/sqlfmt/format")
  lexer.go                 — tokenizer
  layout.go                — river-alignment layout engine
  format.go                — the library entry point (Format)
  format_test.go           — corpus round-trip test (reads ../testdata/corpus)
cmd/sqlfmt/main.go         — the CLI binary
testdata/corpus/           — flat directory of real book queries, each one run through
                             sqlfmt and reviewed — the round-trip/regression fixtures,
                             also used by `make test`'s CLI-level check
testdata/excluded/          — files that look like corpus examples but aren't
                             (psql transcripts, verbatim docs quotes) — kept for
                             reference, must NOT be used as formatting fixtures
```

`testdata/` sits at the repo root rather than under `format/` even though
only the `format` package's tests read it: it's also what `make test`'s
CLI-level check runs the built `sqlfmt` binary against, so it's shared
between the Go test suite and the Makefile rather than being package-private
(Go's tooling ignores any directory literally named `testdata` regardless of
nesting, so this doesn't affect `go build`/`go vet`).

## The source corpus

The full corpus this style was derived from lives in a sibling repository:
`/Users/dim/dev/TAOP/TheArtOfPostgreSQL/queries/` — 343 `.sql` files organized
by book chapter. `testdata/corpus/` here is a flat, renamed ~48-file subset
(originally curated to cover every formatting pattern documented in
`STYLE.md` — simple SELECTs, multi-predicate WHERE, JOINs with single- and
multi-condition ON clauses, CTEs, window functions, CASE expressions, CREATE
TABLE, multi-statement `begin;`/`commit;` scripts, and comments) without
requiring the full sibling checkout to exist for `go test` to run.

Each fixture holds `sqlfmt`'s own canonical output over that query, not the
book's original hand-formatting byte-for-byte — the two usually agree, but
where a real file used one of `STYLE.md`'s documented "genuinely ambiguous"
variants (e.g. `over (` with a space, a 3-space CREATE TABLE indent), the
fixture reflects the tool's picked default instead. `go test`'s corpus check
is therefore chiefly an idempotency/regression guard, not an independent
verifier of `STYLE.md` fidelity — if `sqlfmt` regresses on a construct, the
fixture stops matching; it won't catch the formatter agreeing with itself on
something `STYLE.md` didn't actually ask for. If deeper validation against
the entire 343-file sibling corpus is wanted, point `SQLFMT_CORPUS_DIR` at
that path (see `format/format_test.go`).

## License

The PostgreSQL Licence — see [`LICENSE`](LICENSE).
