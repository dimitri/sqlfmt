-- A scalar subquery in the select list opens with its own paren, so the
-- body is indented inside it and the closing paren returns to it; an IN
-- on the right of a predicate brackets under the IN instead.
  select ds.driverid,
         ds.points,
         (
           select count(*) + 1
             from f1db.driverstandings inner_ds
            where inner_ds.raceid = ds.raceid
              and inner_ds.points > ds.points
         ) as rank
    from f1db.driverstandings ds
   where ds.raceid in
                   (
                     select r.raceid
                       from f1db.races r
                      where r.year = 2017
                        and r.round > 10
                   )
order by rank;
