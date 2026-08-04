with counts as
 (
   select date_trunc('year', date) as year,
          count(*) filter(where position is null) as outs,
          bool_and(position is null) as never_finished
     from drivers
          join results using(driverid)
          join races using(raceid)
 group by date_trunc('year', date), driverid
 )
   select extract(year from year) as season,
          sum(outs) as "#times any driver didn't finish a race"
     from counts
    where never_finished
 group by season
 order by sum(outs) desc
    limit 5;
