package format

import (
	"strings"
	"testing"
)

// A SELECT's row-locking clause is a clause like ORDER BY or LIMIT, and
// starts its own river line when the statement does not fit on one. Before
// it had a bound of its own it ran on at the end of whatever preceded it:
//
//	limit 1 for update skip locked;
//	and valid_period && daterange(...) for update;
//
// the second of which reads as though FOR UPDATE belonged to the AND.
//
// The clause is named "for", not "for update": the river is as wide as its
// longest clause keyword, and "for no key update" would push every other
// clause nine columns to the right. The strength and the OF / NOWAIT /
// SKIP LOCKED words are the body.
func TestSelectLockingClauseStartsItsOwnLine(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"after limit",
			"select id from jobs where done = false order by id limit 1 for update skip locked;\n",
			[]string{"   limit 1\n     for update skip locked;"}},
		{"after an AND predicate",
			"select * from demo_contract where driverid = 1 and valid_period && daterange('2011-01-01', '2012-01-01') for update;\n",
			[]string{"\n   and valid_period && daterange('2011-01-01', '2012-01-01')\n   for update;"}},
		{"for no key update, of, nowait",
			"select a, b from t where a = 1 for no key update of t nowait;\n",
			[]string{" where a = 1\n   for no key update of t nowait;"}},
		{"for share",
			"select id, payload from jobs where state = 'queued' order by id limit 10 for share of jobs skip locked;\n",
			[]string{"     for share of jobs skip locked;"}},
		{"for key share",
			"select id, payload from jobs where state = 'queued' for key share;\n",
			[]string{"\n   for key share;"}},
		{"several locking clauses, one each",
			"select a.id, b.id from a join b using(id) where a.x = 1 for update of a for share of b;\n",
			[]string{"\n   for update of a\n   for share of b;"}},
		{"inside a CTE",
			"with j as (select id from jobs where done = false order by id limit 1 for update skip locked) delete from jobs using j where jobs.id = j.id returning jobs.id;\n",
			[]string{"     limit 1\n       for update skip locked\n)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mustFormat(t, c.src)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("want %q in:\n%s", w, got)
				}
			}
			for _, l := range strings.Split(got, "\n") {
				if cols(l) > targetWidth {
					t.Errorf("line over the margin (%d cols):\n%s", cols(l), got)
				}
			}
			if squash(got) != squash(c.src) {
				t.Errorf("content lost:\nwant %s\ngot  %s", squash(c.src), squash(got))
			}
			if twice := mustFormat(t, got); twice != got {
				t.Errorf("not idempotent:\nonce:\n%s\ntwice:\n%s", got, twice)
			}
		})
	}
}

// The word after "for" says which for this is. A statement short enough to
// stay flat stays flat, and the FORs that are not locking clauses --
// FOR EACH ROW, FOR VALUES, a policy's FOR UPDATE -- are left as they were.
func TestLockingClauseLeavesOtherForsAlone(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"short select stays flat",
			"select * from t where a = 1 for update;\n",
			"select * from t where a = 1 for update;\n"},
		{"trigger",
			"create trigger trg before update on t for each row execute function f();\n",
			"create trigger trg before update on t for each row execute function f();\n"},
		{"policy command",
			"create policy p on t for update using (owner = current_user);\n",
			"create policy p on t for update using(owner = current_user);\n"},
		{"partition bound",
			"create table t1 partition of t for values from (1) to (2);\n",
			"create table t1 partition of t for values from (1) to (2);\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mustFormat(t, c.src); got != c.want {
				t.Errorf("want:\n%s\ngot:\n%s", c.want, got)
			}
		})
	}
}

// INSERT ... ON CONFLICT DO SELECT takes its own optional FOR UPDATE, and it
// belongs to the conflict action: DO SELECT is followed by no select list, so
// there is no select clause for a locking clause to attach to, and the bound
// must not fire. It has to stay on the conflict line, before any WHERE and
// RETURNING, in the flat case and the broken one alike.
func TestDoSelectKeepsItsForUpdate(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"flat",
			"insert into t (a) values (1) on conflict (a) do select for update returning a;\n",
			[]string{"do select for update returning a;"}},
		{"with where and returning",
			"insert into demo_driver_seen(driverid, surname) values (1, 'Hamilton') on conflict (driverid) do select for update where surname = 'Hamilton' returning driverid, surname, first_seen_at;\n",
			[]string{"on conflict (driverid) do select for update\n", "      where surname = 'Hamilton'\n", "  returning driverid, surname, first_seen_at;"}},
		{"for no key update",
			"insert into demo_driver_seen(driverid, surname) values (1, 'Hamilton') on conflict (driverid) do select for no key update returning driverid, surname, first_seen_at;\n",
			[]string{"do select for no key update"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mustFormat(t, c.src)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("want %q in:\n%s", w, got)
				}
			}
			if strings.Contains(got, "\n     for ") || strings.Contains(got, "\n   for ") {
				t.Errorf("DO SELECT's FOR UPDATE became a clause of its own:\n%s", got)
			}
			if squash(got) != squash(c.src) {
				t.Errorf("content lost:\nwant %s\ngot  %s", squash(c.src), squash(got))
			}
			if twice := mustFormat(t, got); twice != got {
				t.Errorf("not idempotent:\nonce:\n%s\ntwice:\n%s", got, twice)
			}
		})
	}
}

// The SELECT feeding an INSERT ... ON CONFLICT can carry a locking clause
// too, and it is that SELECT's, not the conflict action's: there is a select
// clause for it to lock, and no DO SELECT in sight.
func TestLockingClauseOnAnInsertSelect(t *testing.T) {
	src := "insert into t (a) select a from s where a > 1 order by a limit 5 for update skip locked on conflict (a) do nothing;\n"
	got := mustFormat(t, src)
	lines := strings.Split(got, "\n")
	found := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "for update skip locked") {
			found = i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1]), "limit 5")
		}
	}
	if !found {
		t.Errorf("the source SELECT's locking clause did not get its own line under limit:\n%s", got)
	}
	if squash(got) != squash(src) {
		t.Errorf("content lost:\nwant %s\ngot  %s", squash(src), squash(got))
	}
	if twice := mustFormat(t, got); twice != got {
		t.Errorf("not idempotent:\nonce:\n%s\ntwice:\n%s", got, twice)
	}
}
