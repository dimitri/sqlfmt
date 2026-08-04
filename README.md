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

## Editor integration

### Emacs

`editors/emacs/sqlfmt.el` provides `sqlfmt-mode`, a minor mode for
`sql-mode` buffers:

```elisp
(add-to-list 'load-path "~/dev/PostgreSQL/sqlfmt/editors/emacs")
(add-hook 'sql-mode-hook #'sqlfmt-mode)
```

With it enabled, `C-M-h` (`mark-defun`, as in `python-mode`) selects the SQL
statement at point, and `TAB` on that selection reformats it via `sqlfmt` —
`indent-for-tab-command` already calls `indent-region` whenever the region
is active, and `sqlfmt-mode` sets `indent-region-function` to use `sqlfmt`,
so this needs no new keybinding for `TAB` itself. `sqlfmt-buffer` and
`sqlfmt-region` are also plain interactive commands, and
`sqlfmt-before-save-hook` can be added to `before-save-hook` to format on
save. See the commentary at the top of `sqlfmt.el` for details.

### Vim

`editors/vim/ftplugin/sql.vim` wires `sqlfmt` into Vim's `formatprg`/
`equalprg` options — since `sqlfmt` with no arguments already reads stdin
and writes formatted SQL to stdout, no plugin logic beyond setting those two
options is needed. Add the directory to your `runtimepath`:

```vim
set runtimepath+=~/dev/PostgreSQL/sqlfmt/editors/vim
```

or copy/symlink `sql.vim` to `~/.vim/ftplugin/sql.vim`. This enables:

```
gqip / gqap / gqG    " reformat a paragraph/block/the whole buffer (gq)
=ap / =G / gg=G       " same, via the = operator
:%!sqlfmt              " reformat the whole buffer directly
```

Set `g:sqlfmt_command` before the file loads to point at a non-`$PATH`
binary.

## WebAssembly build

`wasm/` compiles the `format` library to WebAssembly for in-browser use,
exposing a single global JS function:

```js
sqlfmt.format(sql)
//  -> { output: string } on success
//  -> { error: string }  on a real parse/format error
```

Built with [TinyGo](https://tinygo.org) (`tinygo build -target=wasm -no-debug
-opt=z`) plus a [Binaryen](https://github.com/WebAssembly/binaryen)
`wasm-opt -Oz` pass, rather than the standard `go build` toolchain: the
standard js/wasm target always statically links its full runtime and GC with
no way to drop it, landing around **2.9MB** for this program; the same
source via TinyGo + wasm-opt comes in around **330KB** — roughly 9x smaller,
and confirmed to still round-trip correctly (see `wasm/smoketest.mjs`).
Requires `tinygo` and `wasm-opt` on `PATH` (`brew install tinygo binaryen`
on macOS; CI installs both via
[`acifani/setup-tinygo`](https://github.com/acifani/setup-tinygo)).

Build it locally with `make wasm` (outputs `dist/wasm/sqlfmt.wasm`, the
matching `wasm_exec.js` glue it needs — from TinyGo's own target support
files, not the standard Go toolchain's — plus a pre-compressed
`sqlfmt.wasm.gz` copy, see below), or `make wasm-test` to additionally run
`wasm/smoketest.mjs`, which loads the module under Node and exercises
`sqlfmt.format` the same way a browser page would.

CI publishes all of this on every green push to `main`, at stable,
always-latest-HEAD URLs — the same pattern used for
[pgloader's v4 JAR releases](https://github.com/dimitri/pgloader/releases/tag/v4-dev):

```
https://github.com/dimitri/sqlfmt/releases/download/wasm-dev/sqlfmt.wasm
https://github.com/dimitri/sqlfmt/releases/download/wasm-dev/wasm_exec.js
https://github.com/dimitri/sqlfmt/releases/download/wasm-dev/sqlfmt.wasm.gz
```

See `.github/workflows/ci.yml`'s `wasm` and `publish-wasm-dev` jobs.

### Pre-compressed copy (`.gz`)

`sqlfmt.wasm` is ~330KB; gzipped it's ~130KB. GitHub Releases doesn't serve
this with a `Content-Encoding` header (verified — a plain `fetch` gets the
literal compressed bytes back, not auto-decompressed by the browser), but it
can still be decompressed client-side with no extra dependency, via the
standard [Compression Streams
API](https://developer.mozilla.org/en-US/docs/Web/API/Compression_Streams_API)
(confirmed working end-to-end in a real browser):

```js
const response = await fetch("sqlfmt.wasm.gz");
const bytes = await new Response(
  response.body.pipeThrough(new DecompressionStream("gzip")),
).arrayBuffer();
const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
```

(Brotli was tried too but dropped: pre-compressing to `.br` would only pay
off if the file were served with a transparent `Content-Encoding: br` by
whatever server ultimately hosts it, which is outside this repo's control —
and unlike gzip, there's no `DecompressionStream("br")` to decompress it
client-side; that throws `Unsupported compression format` even on a current
Chrome. Not worth a second artifact for a path nothing here can use.)

Plain **`sqlfmt.wasm`** remains the zero-effort default: works directly with
`WebAssembly.instantiateStreaming(fetch(...))`, no decompression code at
all, at the cost of the larger transfer.

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
wasm/main.go                — WebAssembly build (globalThis.sqlfmt.format), see "WebAssembly build"
wasm/smoketest.mjs          — Node smoke test for the built wasm module
wasm/compress.mjs           — produces sqlfmt.wasm.gz from the built module
editors/emacs/sqlfmt.el     — sql-mode minor mode (mark-defun/indent-region integration)
editors/vim/ftplugin/sql.vim — formatprg/equalprg integration
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

The full corpus this style was derived from lives in
[dimitri/TheArtOfPostgreSQL](https://github.com/dimitri/TheArtOfPostgreSQL)'s
`queries/` directory — 343 `.sql` files organized by book chapter.
`testdata/corpus/` here is a flat, renamed ~48-file subset
(originally curated to cover every formatting pattern documented in
`STYLE.md` — simple SELECTs, multi-predicate WHERE, JOINs with single- and
multi-condition ON clauses, CTEs, window functions, CASE expressions, CREATE
TABLE, multi-statement `begin;`/`commit;` scripts, and comments) without
requiring a checkout of that repo to exist for `go test` to run.

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
