  select surname,
         position as pos,
         row_number()
           over(order by fastestlapspeed::numeric)
           as fast,
         ntile(3) over w as "group",
         lag(code, 1) over w as "prev",
         lead(code, 1) over w as "next"
    from      results
         join drivers using(driverid)
   where raceid = 890
  window w as (order by position)
order by position;
