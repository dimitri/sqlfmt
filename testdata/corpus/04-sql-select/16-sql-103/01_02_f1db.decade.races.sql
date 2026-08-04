with races_per_decade
as (
      select extract('year'
                     from
                     date_trunc('decade', date))
             as decade,
             count(*) as nbraces
        from races
      group by decade
      order by decade
   )
select decade, nbraces,
       case
         when lag(nbraces, 1)
               over(order by decade) is null
         then ''

         when nbraces - lag(nbraces, 1)
                      over(order by decade)
              < 0
         then format('-%3s',
                lag(nbraces, 1)
               over(order by decade)
               - nbraces)

         else format('+%3s',
                nbraces
              - lag(nbraces, 1)
               over(order by decade))

        end as evolution
  from races_per_decade;
