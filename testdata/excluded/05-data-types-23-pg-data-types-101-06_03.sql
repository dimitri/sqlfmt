create table seq(id serial);
CREATE TABLE

select setval('public.seq_id_seq'::regclass, 2147483647);
   setval   
════════════
 2147483647
(1 row)

yesql# insert into public.seq values (default);
ERROR:  integer out of range
