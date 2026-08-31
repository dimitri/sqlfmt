package format

import (
	"strings"
	"testing"
)

// Each of these fell to flatJoin: the GRANT came back as one 205-column
// line, the partitioned tables at 95-96 each. The expected shape is the
// corpus's own -- header, then one clause per line at indent 2.
func TestIndentedDDLClauses(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"partition of",
			"create table lab.invoice_2022 partition of lab.invoice_by_year for values from (2022) to (2023);\n",
			[]string{"create table lab.invoice_2022", "  partition of lab.invoice_by_year", "  for values from (2022) to (2023);"}},
		{"create database",
			"create database chinook_prod owner chinook_owner encoding 'UTF8' lc_collate 'en_US.UTF-8' lc_ctype 'en_US.UTF-8' template template0;\n",
			[]string{"create database chinook_prod", "  owner chinook_owner", "  encoding 'UTF8'", "  template template0;"}},
		{"grant",
			"grant select (invoice_id, customer_id, invoice_date, billing_address, billing_city, billing_state, billing_country, billing_postal_code, total) on chinook.invoice to chinook_ro;\n",
			[]string{"grant select (invoice_id, customer_id, invoice_date, billing_address,", "  on chinook.invoice", "  to chinook_ro;"}},
		{"alter default privileges",
			"alter default privileges in schema chinook grant select, insert, update, delete on tables to chinook_rw;\n",
			[]string{"alter default privileges", "  in schema chinook", "  grant select, insert, update, delete", "  on tables"}},
		{"alter subcommand inner clauses",
			"alter table chinook.invoice add constraint invoice_currency_notnull check (currency is not null) not valid;\n",
			[]string{"alter table chinook.invoice", "  add constraint invoice_currency_notnull", "      check (currency is not null)", "      not valid;"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mustFormat(t, c.src)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("want line %q in:\n%s", w, got)
				}
			}
			for _, l := range strings.Split(got, "\n") {
				if cols(l) > targetWidth {
					t.Errorf("line over the margin (%d cols):\n%s", cols(l), got)
				}
			}
		})
	}
}

// A statement that fits is left whole; only the clause words that open a
// continuation are break points, never one that opens the statement.
func TestIndentedDDLKeepsShortStatementsWhole(t *testing.T) {
	for _, src := range []string{
		"grant usage on schema chinook to chinook_ro;\n",
		"create database sandbox;\n",
		"alter table t add column c int;\n",
	} {
		got := mustFormat(t, src)
		if strings.Count(strings.TrimRight(got, "\n"), "\n") != 0 {
			t.Errorf("short statement was broken up:\n%s", got)
		}
	}
}

// The ALTER/GRANT/CREATE DATABASE words are keywords now, so rule 1
// lowercases the whole construct rather than whichever word was already
// in the table: "ATTACH partition", "create DATABASE", "GRANT select".
func TestDDLVocabularyIsLowercased(t *testing.T) {
	got := mustFormat(t,
		"ALTER TABLE t ATTACH PARTITION p FOR VALUES FROM (1) TO (2);\n"+
			"CREATE DATABASE d OWNER o ENCODING 'UTF8';\n"+
			"GRANT SELECT ON t TO r;\n")
	for _, bad := range []string{"ATTACH", "DATABASE", "OWNER", "GRANT", "SELECT", "PARTITION"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q left uppercased:\n%s", bad, got)
		}
	}
}

// A constraint's continuation keywords are right-aligned, not indented,
// so every clause's value starts at the same column: "geoname" lands
// under the "(" of the key it qualifies, not under "isocode".
func TestConstraintClausesAreRiverAligned(t *testing.T) {
	src := "create table geoname.city (id int, isocode text, regcode text, discode text," +
		" foreign key (isocode, regcode, discode) references geoname.district(isocode, regcode, discode));\n"
	got := mustFormat(t, src)
	var keyCol, refCol int
	for _, l := range strings.Split(got, "\n") {
		if i := strings.Index(l, "foreign key ("); i >= 0 {
			keyCol = i + len("foreign key ")
		}
		if i := strings.Index(l, "references "); i >= 0 {
			refCol = i + len("references ")
		}
	}
	if keyCol == 0 || refCol == 0 {
		t.Fatalf("constraint not broken:\n%s", got)
	}
	if keyCol != refCol {
		t.Errorf("values at columns %d and %d, want them aligned:\n%s", keyCol, refCol, got)
	}
}
