create table test
 (
  id serial,
  f1 text default 'unknown'
);

insert into test(f1) values (default), (null), ('foo');

table test;
