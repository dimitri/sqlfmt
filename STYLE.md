# Style guide

Reverse-engineered from `TheArtOfPostgreSQL/queries/` (343 `.sql` files, the
book's own hand-formatted example queries), by reading ~50+ files in full
across every chapter plus corpus-wide `grep`/frequency scans to quantify and
catch counter-examples. Column-alignment claims below were verified by
counting characters programmatically, not eyeballed.

**Before using this corpus for anything**: it contains two populations that
must be told apart. The large majority is the author's own hand-formatted
style (what this document describes). A small number of files (~20, listed
in `testdata/excluded/` here) are verbatim quotations of *other* material —
psql session transcripts (prompts, `(N rows)` footers) and literal copies of
PostgreSQL's own documentation examples — and one deliberate "here's ugly
SQL" pedagogical contrast example. None of those represent the target style;
formatting them "correctly" per this guide would be wrong. A formatter's own
test/training data must exclude them.

## The unifying rule

Indentation is not block/fixed-width nesting — it's **alignment-based**
("river" style). At each (sub)query nesting level: find the longest clause
keyword actually used at that level (from `select`, `from`, `where`,
`group by`, `having`, `order by`, `insert into`, `update`, `set`, `delete`,
`returning`), then right-pad every clause keyword at that level so it *ends*
at the same column. This one rule generates the staircase look for
SELECT/FROM/WHERE/GROUP BY/HAVING/ORDER BY simultaneously:

```sql
  select status, count(*)
    from results
         join races using(raceid)
   where date >= :season
group by status
  having count(*) >= 10
order by count(*) desc;
```

Every keyword above ends at the same column (verified: column 7, 0-indexed).
`group by`/`order by` are 8 characters — longer than `select`'s 6 — so at
base indent 0 they end up flush-left; that's a side effect of this rule, not
a separate "GROUP BY is always flush-left" rule. When a query is itself
nested (inside a CTE or subquery), the whole staircase shifts right with it:

```sql
      from results
           join status using(statusid)
           join races using(raceid)
  group by season
```
(`group by` ends at column 9 here, matching `select`/`from` at that same
nesting level.)

**Known real exception**: the book source behind `testdata/corpus/sql-103-04_01.sql`
(originally `04-sql-select/16-sql-103/04_01.sql` in the full sibling corpus)
breaks this rule's own self-consistency — `select`/`from`/`group by`/`having`/
`order by` all end at column 10 in that file, but `where`/`and` end at column
8 in the *same statement*. This is genuine hand-formatting drift in the
source, not a documented rule variant. Don't try to reproduce it; a formatter
should apply the rule uniformly and simply diverge from it. Note that the
committed fixture itself no longer shows this drift — per `format/format_test.go`,
corpus fixtures hold `sqlfmt`'s own canonical output, which normalizes it
away; this note now documents the *original* hand-formatting, not the
fixture's current content.

## Rule list

1. **Keyword casing**: lowercase every SQL keyword and function name, no
   exceptions in genuine author content (1389 lowercase vs. 27 uppercase
   corpus-wide, and 100% of the uppercase hits are inside the excluded
   psql-transcript/docs-quote files). `count(*)`, `coalesce(...)`,
   `row_number() over(...)`, `extract('year' from races.date)`.

2. **Indentation**: spaces only, never tabs (343 files, exactly 1 contains a
   tab, and it's inside a `LANGUAGE xslt` header that also uses uppercase —
   itself `pg_dump`-style output, not hand-typed).

3. **Terminators / blank lines**: every top-level statement ends with `;`.
   In multi-statement scripts, separate top-level statements — including
   around `begin;`/`commit;`/`rollback;` — by exactly one blank line:
   ```sql
   begin;

   create table eav.support_contract_type
    (
      id   serial primary key,
      name text not null
    );

   insert into eav.support_contract_type(name)
        values ('gold'), ('platinum');
   ...
   commit;
   ```

4. **No space** between a function/identifier name and its opening `(`, nor
   before `USING(`. Verified corpus-wide: 203/203 no-space occurrences for
   `count/sum/avg/coalesce/array_agg/max/min/extract/format/substring`.

5. **Literals and operators**: single quotes always for strings. No space
   around `::` casts (`fastestlapspeed::numeric`). Exactly one space around
   `||` and comparison operators (`=`, `<>`, `>=`, `<`), *except* rule 12's
   alignment-padding carve-out.

6. **Column aliases**: always explicit `AS` (226+ occurrences, zero
   counter-examples found). Quote the alias only when needed (reserved word,
   uppercase, spaces): `as "group"`, `as "prev"`, `as "iso year"`.

7. **Table aliases — genuinely ambiguous, pick a default**: explicit `AS` vs.
   bare juxtaposition is a real ~53/47 split corpus-wide (14 vs. 12 sampled
   occurrences). No strong majority. **Default: bare, no `AS`.** Alias naming
   is loosely first-letter/abbreviation (`mainstem m`, `pg_am am`) but not
   rigid — semantic aliases appear freely for clarity (`artist inspired` in a
   self-join). Don't try to mechanically abbreviate; just preserve whatever
   alias the input SQL already uses.

8. **SELECT layout**: `select` on its own line only when there's more than
   one output column/expression (or one long one). Subsequent columns: one
   per line, comma at the **end** of the previous line, aligned under the
   first column (i.e. under the character right after `select `). Leading
   commas essentially don't occur (4 corpus-wide, all in function-argument
   lists, not column lists — not a real pattern). Trivial queries stay
   inline: `select * from races limit 1;`.
   ```sql
   select code,
          format('%s %s', forename, surname) as fullname,
          forename,
          surname
     from drivers;
   ```

9. **FROM/JOIN layout**: `FROM`'s table stays on the `FROM` line. Each `JOIN`
   starts a new line, indented further right than `FROM` — approximate table
   names lining up under FROM's table name where reasonable, but don't force
   it at the expense of rule 1 (the river alignment). Inline `ON`/`USING`
   after the JOIN keyword for a single condition:
   ```sql
       from      drivers
            join results using(driverid)
   ```
   For **multiple AND-ed join predicates**, put `ON` on its own line, and
   right-align `ON` and continuing `AND`/`OR` lines to end at the *same
   column as the JOIN keyword phrase itself* (not ON's own natural width —
   verified exact in every multi-predicate sample):
   ```sql
          left join results
                 on results.raceid = races.raceid
                and results.position = 1
   ```

10. **WHERE layout**: `WHERE` always on its own line, right-aligned per the
    unifying rule. Multiple predicates: one per line, `AND`/`OR` at the
    **start** of the continuation line, right-aligned to end at the same
    column as `WHERE`. When adjacent predicates use comparison operators of
    different width, pad the shorter operator with an extra space so the
    operands line up vertically (real, repeated pattern — not universal but
    common enough to implement):
    ```sql
     where date >= :season
       and date <  :season + interval '1 year'
       and position is null
    ```

11. **GROUP BY / HAVING / ORDER BY**: each on its own line, following the
    exact same right-alignment rule as SELECT/FROM/WHERE (see "unifying
    rule" above — this is a consequence of rule 1, not a separate
    convention). Multiple columns: comma-separated inline if short,
    one-per-line with continuation aligned under the first item if long:
    ```sql
      order by constructors.name is not null,
               drivers.surname is not null,
               points desc;
    ```

12. **Subqueries — the least mechanically rigid area in the whole corpus,
    implement as best-effort**: indent the subquery body under wherever it
    opens; its own internal SELECT/FROM/WHERE gets its own river-alignment
    computation at that new base indent. Closing `)` goes on its own line,
    indented to approximate the *opening construct's* own indent — this is
    visibly hand-tuned per instance in the source and not perfectly
    reproducible mechanically:
    ```sql
    select *
      from get_all_albums(
             (select artist_id
                from artist
               where name = 'Red Hot Chili Peppers')
           );
    ```

13. **CTEs (WITH clause)**: dominant pattern (~80%) is `with name as (` all
    on one line, body indented (2 spaces is the default — see caveat below),
    closing `)` on its own line at the CTE's base indent, next CTE chained
    with a trailing comma:
    ```sql
    with mainstem as (
      select hyriv_id, geom, ord_stra
        from hydrorivers.rivers
       where main_riv = 20446779 and ord_stra >= 6
    ),
    loire_bbox as (
      select st_xmin(bbox) as x0, ...
    ),
    ```
    Real, quantified exceptions — implement the defaults below, don't chase
    100% fidelity:
    - `as` alone on the WITH-name line with `(` starting the next line
      happened in a real minority of the original book source (~2 of ~15
      sampled CTE files, corresponding to what are now
      `testdata/corpus/sql-103-01_02_f1db.decade.races.sql` and
      `business-logic-05_06.sql`). Pick the dominant same-line `as (` form
      as the formatter's output; treat the split form as an acceptable
      input variant, not an error.
    - **CTE body indent is not fixed at 2 spaces** — a recursive CTE, the
      book source behind `testdata/corpus/sql-103-05_07_hydrorivers.recursive.sql`,
      indented its body 7 spaces to make room for a right-aligned
      `union all`. Real, deliberate, author-tuned. **Default to 2 spaces**
      (the clear majority) and accept the formatter won't replicate this
      specific hand-tuned case (the committed fixture itself now holds the
      2-space canonical form, per the corpus's current semantics — see
      `format/format_test.go`).
    - **Subsequent CTE names are usually flush-left at column 0** when
      chained, but the book source behind
      `testdata/corpus/hyperloglog-05_01_tweets.hll.sql` indented
      its second CTE name 4 spaces. Default to flush-left.

14. **Window functions**: short `OVER (...)` stays inline. When
    `PARTITION BY`/`ORDER BY`/a frame clause don't fit, wrap them under the
    opening paren of `OVER(`, with a wrapped frame-clause `AND` right-aligned
    under the boundary it continues:
    ```sql
     array_agg(x) over (order by x
                        rows between unbounded preceding
                                 and current row)
    ```
    `OVER` itself is always lowercase. **`over (` vs `over(` (space before
    the paren) is a genuine, roughly-even split with no dominant convention**
    — pick `over(` (no space) for the formatter's output, to stay consistent
    with rule 4's "no space before `(`" convention elsewhere, even though the
    source itself doesn't consistently do this.

15. **CASE expressions**: short CASE stays inline:
    `case when name = 'France' then '#F2EFE9' else '#ECECEC' end`.
    Longer CASE: `WHEN` stays on/near the `CASE` line, `THEN`/`ELSE` each own
    line aligned under `WHEN`'s condition-start column, `END` dedented near
    `CASE`'s own column:
    ```sql
    set rts = case when NEW.action = 'rt'
                   then rts + 1
                   else rts
              end,
    ```
    **Known source inconsistency, deliberately do NOT reproduce it**: when
    several CASE expressions appear in a list (e.g. a multi-column
    `UPDATE ... SET`), the source sometimes copy-pastes the *same* fixed
    THEN/ELSE/END indentation across all of them even though each CASE
    starts at a different column (verified in
    `07-concurrency/39-triggers/01_01_dream-trigger-daily.sql`, not in this
    repo's `testdata/corpus/` but in the full sibling corpus — worth pulling
    in if implementing this rule). **The formatter should recompute
    alignment independently per CASE instance** — that's the "locally
    correct" behavior, and diverging from the source's copy-paste artifact
    here is intentional, not a bug.

16. **CREATE TABLE / DDL — a different sub-style from the query clauses
    above**: table name on the `CREATE TABLE` line, opening `(` on its own
    line indented 1 space, each column on its own line indented 2 spaces
    (the corpus splits between 2 and 3 — pick 2 consistently).
    **Left-pad every column name to a common width so all the data types
    start in the same column — this alignment is extremely consistent,
    apply it unconditionally.** Separate table-level constraints
    (`primary key(...)`, `unique(...)`, `check(...)`) from the column list
    with one blank line. Closing `)` on its own line at the opening `(`'s
    indent, then `;`. Collapse to one line only for genuinely trivial tables
    that fit under the line-length target (rule 17).

17. **Line length**: soft target ≈ 78–80 characters, not hard-enforced
    (measured: only 2.5% of 3887 corpus lines exceed 70 chars, only 16
    exceed 80, max found anywhere is 96). Wrap continuation content to stay
    near this target where the SQL structure allows; allow longer lines only
    when a single unbreakable token (long string literal, dense expression)
    forces it.

18. **Comments**: rare (~5% of files), always preserved, never discarded.
    A "--" comment sitting on its own source line, not preceded by real
    code on that line, is a leading comment — attached to whatever
    statement, clause, or list item follows it, reindented to that
    context's column, and reflowed to the ~78–80 col line-length target
    (rule 17). A blank source line between two leading comments is kept as
    a paragraph break; a comment line that's nothing but dashes (e.g.
    `-----------`) is a divider and is preserved verbatim, never merged
    into surrounding prose. A comment on the same source line as preceding
    real code is a trailing comment; multiple trailing comments in the same
    contiguous block of lines are padded so they all start at a shared
    column:
    ```sql
      select geom, ord_stra from mainstem          -- 155 high-order channels
    ...
         and r.ord_stra < 6;                       -- 161 direct tributaries
    ```
    Block comments (`/* ... */`) are rewritten into the C style: an opening
    line with only `/*`, each content line reflowed to the line-length
    target and starting with a `*` aligned under `/*`'s own `*` (its second
    character), and a closing line with only `*/` aligned the same way —
    regardless of how the original was formatted, so re-running the
    formatter on its own output is a no-op:
    ```sql
    /*
     * Generate the target month's calendar then LEFT JOIN each day
     * against the factbook dataset, so as to have every day in the
     * result set, whether or not we have a book entry for the day.
     */
    ```
    A formatter's tokenizer must be comment-aware from the start (preserve
    and reposition comments, don't discard them) — this is the main reason
    the engine should be a token-stream formatter, not a generic AST
    pretty-printer built on a parser that discards comments (see
    `DESIGN.md`).

## Summary of genuinely ambiguous areas

Be forgiving on these when validating/parsing input; pick the stated default
when producing output:

| Area | Split | Default to use |
|---|---|---|
| Table alias `AS` vs. bare | ~53/47 | bare (no `AS`) |
| `over (` vs `over(` | ~even | `over(` (no space) |
| CTE body indent | 2-space dominant, real exceptions exist | 2 spaces |
| Chained CTE name indent | flush-left dominant, one exception | flush-left |
| `with name as (` same-line vs. split | same-line dominant | same-line |
| CREATE TABLE column indent | 2 vs. 3 space split | 2 spaces |
| Subquery closing-paren placement | most hand-tuned area in the corpus | best-effort approximation, expect real divergence |
