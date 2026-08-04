with points as
 (
    select year as season, driverid, constructorid,
           sum(points) as points
      from results join races using(raceid)
  group by grouping sets((year, driverid),
                         (year, constructorid))
    having sum(points) > 0
  order by season, points desc
 ),
 tops as
 (
    select season,
           max(points) filter(where driverid is null) as ctops,
           max(points) filter(where constructorid is null) as dtops
      from points
  group by season
  order by season, dtops, ctops
 ),
 champs as
 (
    select tops.season,
           champ_driver.driverid,
           champ_driver.points,
           champ_constructor.constructorid,
           champ_constructor.points
   
      from tops
           join points as champ_driver
             on champ_driver.season = tops.season
            and champ_driver.constructorid is null
            and champ_driver.points = tops.dtops
   
           join points as champ_constructor
             on champ_constructor.season = tops.season
            and champ_constructor.driverid is null
            and champ_constructor.points = tops.ctops
  )
  select season,
         format('%s %s', drivers.forename, drivers.surname)
            as "Driver's Champion",
         constructors.name
            as "Constructor's champion"
    from champs
         join drivers using(driverid)
         join constructors using(constructorid)
order by season;
