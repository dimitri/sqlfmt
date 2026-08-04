> create table test(id integer unique);
CREATE TABLE
Time: 68.775 ms

> \d test
     Table "public.test"
 Column |  Type   | Modifiers 
--------+---------+-----------
 id     | integer | 
Indexes:
    "test_id_key" UNIQUE CONSTRAINT, btree (id)
