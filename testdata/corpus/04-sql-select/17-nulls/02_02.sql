drop table test;
create table test(id serial, f1 text not null default 'unknown');
insert into test(f1) values(DEFAULT),(NULL),('foo');
ERROR:  null value in column "f1" violates not-null constraint
DETAIL:  Failing row contains (2, null).
