-- Subquery layout: an EXISTS and a NOT EXISTS whose bodies are too wide
-- to stay inline. Both bracket under the operator that introduces them.
  select d.surname, d.nationality
    from f1db.drivers d
   where exists
         (
           select 1
             from f1db.results res
             join f1db.races r on r.raceid = res.raceid
            where res.driverid = d.driverid
              and r.year = 2017
              and res.positionorder = 1
         )
     and not exists
         (
           select 1
             from f1db.results dnf
            where dnf.driverid = d.driverid
              and dnf.statusid <> 1
              and dnf.positionorder > 15
         )
order by d.surname;
