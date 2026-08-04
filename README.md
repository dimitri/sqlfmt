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

**Status: early development.** The formatting rules have been reverse-engineered
from the book's own query corpus (see `STYLE.md`) and a curated test corpus has
been assembled (`testdata/`), but the lexer/layout engine itself has not been
written yet. See `DESIGN.md` for the architecture plan and open engineering
decisions.

## Planned usage

Modeled directly on `gofmt`'s CLI, since that's a UX Go developers already know:

```console
$ sqlfmt query.sql              # print formatted output to stdout
$ sqlfmt -w query.sql           # rewrite the file in place
$ sqlfmt -l queries/**/*.sql    # list files whose formatting would change
$ sqlfmt -d query.sql           # show a unified diff instead of full output
$ cat query.sql | sqlfmt        # stdin -> stdout, pipeable
```

As a library: `import "github.com/dimitri/sqlfmt"` (module path TBD — not
yet published), `sqlfmt.Format(io.Reader) (string, error)`, so callers like
`app.taop.xyz`'s `cmd/sqlbuild` book-build tool can format embedded queries
in-process without shelling out to the binary.

## Repository layout

```
STYLE.md              — the extracted formatting rules (the spec to implement against)
DESIGN.md             — architecture plan, engineering notes, parser-library research
testdata/corpus/      — curated real examples from the book, already correctly
                         formatted — the primary regression/round-trip test fixtures
testdata/excluded/     — files that look like corpus examples but aren't
                         (psql transcripts, verbatim docs quotes) — kept for
                         reference, must NOT be used as formatting fixtures
lexer.go               — (not yet written) tokenizer
layout.go              — (not yet written) river-alignment layout engine
sqlfmt.go              — (not yet written) package sqlfmt, the library entry point
sqlfmt_test.go          — (not yet written) corpus round-trip test
cmd/sqlfmt/main.go     — (not yet written) the CLI binary
```

## The source corpus

The full training corpus this style was derived from lives in a sibling
repository: `/Users/dim/dev/TAOP/TheArtOfPostgreSQL/queries/` — 343 `.sql`
files organized by book chapter. `testdata/corpus/` here is a curated ~49-file
subset chosen to cover every formatting pattern documented in `STYLE.md`
(simple SELECTs, multi-predicate WHERE, JOINs with single- and multi-condition
ON clauses, CTEs including the documented indentation exceptions, window
functions, CASE expressions, CREATE TABLE, multi-statement `begin;`/`commit;`
scripts, and comments) without requiring the full sibling checkout to exist
for `go test` to run. If deeper validation against the entire 343-file corpus
is wanted later, point at that path directly rather than committing all of it
here.

## License

TBD — should probably match the book's own code license. Not yet decided.
