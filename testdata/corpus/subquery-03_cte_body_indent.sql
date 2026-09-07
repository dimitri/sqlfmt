-- A CTE body is indented two columns from the CTE's own base (STYLE.md
-- rule 13), whether or not it fits on one line.
with last_standings as (
    select ds.raceid, max(ds.points) as top_points
      from f1db.driverstandings ds
      join f1db.races r using(raceid)
     where r.year = 2017
  group by ds.raceid
),
podium as (
  select raceid, driverid
    from f1db.results
   where positionorder <= 3
)
  select ls.raceid, ls.top_points, count(p.driverid) as podium_drivers
    from last_standings ls
    join podium p using(raceid)
group by ls.raceid, ls.top_points
order by ls.raceid;
