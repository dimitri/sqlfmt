begin;

create table moma.audit
 (
  change_date timestamptz default now(),
  before      hstore,
  after       hstore
 );

commit;
