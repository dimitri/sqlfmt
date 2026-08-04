select drivers.code, drivers.surname,
       position,
       laps,
       status
  from results
       join drivers using(driverid)
       join status using(statusid)
 where raceid = 972
order by position nulls last,
         laps desc,
         case when status = 'Power Unit'
              then 1
              else 2
          end;
