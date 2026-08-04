\set season 'date ''1978-01-01'''

    select drivers.surname as driver,
           constructors.name as constructor,
           sum(points) as points
      
      from results
           join races using(raceid)
           join drivers using(driverid)
           join constructors using(constructorid)
   
   where date >= :season
     and date <  :season + interval '1 year'
  
  group by grouping sets((drivers.surname),
                         (constructors.name))
    having sum(points) > 20
  
  order by constructors.name is not null,
           drivers.surname is not null,
           points desc;
