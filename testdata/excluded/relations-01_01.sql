~# create table relation(id integer, f1 text, f2 date, f3 point);
CREATE TABLE

~# insert into relation
        values(1,
               'one',
               current_date,
               point(2.349014, 48.864716)
              );
INSERT 0 1

~# select relation from relation;
                 relation                  
═══════════════════════════════════════════
 (1,one,2017-07-04,"(2.349014,48.864716)")
(1 row)
