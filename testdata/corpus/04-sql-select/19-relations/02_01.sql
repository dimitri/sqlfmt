   select results.positionorder as position,
          drivers.code,
          count(behind.*) as behind

    from results
              join drivers using(driverid)

         left join results behind
                on results.raceid = behind.raceid
               and results.positionorder < behind.positionorder

   where results.raceid = 972
     and results.positionorder <= 3

group by results.positionorder, drivers.code
order by results.positionorder;
