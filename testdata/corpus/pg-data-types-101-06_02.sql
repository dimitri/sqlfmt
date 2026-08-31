create sequence tablename_colname_seq;

create table tablename
 (
  colname integer not null default nextval('tablename_colname_seq')
);

alter sequence tablename_colname_seq OWNED by tablename.colname;
