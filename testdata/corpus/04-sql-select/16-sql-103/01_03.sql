with counts as
 (
   select driverid, forename, surname,
          count(*) as races,
          bool_and(position is null) as never_finished
     from drivers
          join results using(driverid)
          join races using(raceid)
 group by driverid
 )
   select driverid, forename, surname, races
     from counts
    where never_finished
 order by races desc;
