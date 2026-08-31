package format

import (
	"bytes"
	"strings"
	"testing"
)

func plFmt(t *testing.T, src string) string {
	t.Helper()
	got, err := Format(bytes.NewReader([]byte(src)))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	return got
}

const plFn = `create function tg() returns trigger language plpgsql as $$
declare
channel text := TG_ARGV[0];
begin
if NEW.action = 'rt' then
update counters set n = n + 1 where id = NEW.id;
else
perform pg_notify(channel, NEW.id::text);
end if;
return NEW;
end;
$$;`

// TestPlpgsqlBlockStructure: the body is indented by block depth. The
// interesting case is DECLARE ... BEGIN ... END, which is one block, not
// two -- a depth counter got that wrong and left the body unbalanced.
func TestPlpgsqlBlockStructure(t *testing.T) {
	got := plFmt(t, plFn)
	for _, want := range []string{
		"\ndeclare\n",
		"\n  channel text := TG_ARGV[0];\n",
		"\nbegin\n",
		"\n  if NEW.action = 'rt' then\n",
		"\n    update counters set n = n + 1 where id = NEW.id;\n",
		"\n  else\n",
		"\n  end if;\n",
		"\nend;\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// TestPlpgsqlAssignmentNotSQL: ":=" is PL/pgSQL's assignment; handing it to
// the SQL formatter lexes the ":" as a parameter marker and splits the
// operator into ": =".
func TestPlpgsqlAssignmentNotSQL(t *testing.T) {
	got := plFmt(t, plFn)
	if strings.Contains(got, ": =") {
		t.Errorf("assignment operator was split:\n%s", got)
	}
}

// TestPlpgsqlEmbeddedQueryFormatted: PERFORM's argument is a query and gets
// the same layout as a query anywhere else.
func TestPlpgsqlEmbeddedQueryFormatted(t *testing.T) {
	src := `create function f() returns void language plpgsql as $$
begin
perform (with payload(a, b) as (select NEW.x, NEW.y from t where t.id = NEW.id) select count(*) from payload);
end;
$$;`
	got := plFmt(t, src)
	if !strings.Contains(got, "perform (") {
		t.Errorf("PERFORM keyword lost:\n%s", got)
	}
	if !strings.Contains(got, "\n           select ") && !strings.Contains(got, "\n     select ") {
		t.Errorf("embedded query not formatted:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > 120 {
			t.Errorf("line over 120 columns:\n%s", l)
		}
	}
}

// TestPlpgsqlUnbalancedLeftAlone: a body whose skeleton does not balance is
// not something this recogniser understands, and is preserved exactly.
func TestPlpgsqlUnbalancedLeftAlone(t *testing.T) {
	body := "\nbegin\n  if x then\n    null;\n"
	src := "create function f() returns void language plpgsql as $$" + body + "$$;"
	got := plFmt(t, src)
	if !strings.Contains(got, body) {
		t.Errorf("unbalanced body was rewritten:\n%s", got)
	}
}

// TestPlpgsqlNestedDollarQuoteOpaque: an inner dollar-quoted string must be
// copied whole, not scanned for statement terminators.
func TestPlpgsqlNestedDollarQuoteOpaque(t *testing.T) {
	src := `create function f() returns void language plpgsql as $outer$
begin
execute $inner$ select 1; select 2; $inner$;
end;
$outer$;`
	got := plFmt(t, src)
	if !strings.Contains(got, "$inner$ select 1; select 2; $inner$") {
		t.Errorf("nested dollar quote was taken apart:\n%s", got)
	}
}

// TestPlpgsqlLoop covers the other block opener that ends on a keyword
// rather than a semicolon.
func TestPlpgsqlLoop(t *testing.T) {
	src := `create function f() returns void language plpgsql as $$
begin
for r in select id from t loop
update u set n = n + 1 where id = r.id;
end loop;
end;
$$;`
	got := plFmt(t, src)
	if !strings.Contains(got, "\n  for r in select id from t loop\n") {
		t.Errorf("FOR ... LOOP header not its own line:\n%s", got)
	}
	if !strings.Contains(got, "\n    update u set n = n + 1 where id = r.id;\n") {
		t.Errorf("loop body not indented:\n%s", got)
	}
}

func TestPlpgsqlIdempotent(t *testing.T) {
	for _, src := range []string{plFn} {
		once := plFmt(t, src)
		if twice := plFmt(t, once); once != twice {
			t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", once, twice)
		}
	}
}

// TestOtherLanguagesUntouched: plpython and friends are opaque and must be
// preserved exactly.
func TestOtherLanguagesUntouched(t *testing.T) {
	body := "\nif x:\n    return 1\nreturn 0\n"
	got := plFmt(t, "create function f() returns int language plpython3u as $$"+body+"$$;")
	if !strings.Contains(got, body) {
		t.Errorf("plpython body was altered:\n%s", got)
	}
}
