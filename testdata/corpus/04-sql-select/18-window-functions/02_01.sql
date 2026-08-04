select surname,
         constructors.name,
         position,
         format('%s / %s',
                row_number()
                  over(partition by constructorid
                           order by position nulls last),

                count(*) over(partition by constructorid)
               )
            as "pos same constr"
    from      results
         join drivers using(driverid)
         join constructors using(constructorid)
   where raceid = 890
order by position;
