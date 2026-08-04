\set start '2017-02-01'

  select cast(calendar.entry as date) as date,
         coalesce(shares, 0) as shares,
         coalesce(trades, 0) as trades,
         to_char(
             coalesce(dollars, 0),
             'L99G999G999G999'
         ) as dollars
    from /*
          * Generate the target month's calendar then LEFT JOIN
          * each day against the factbook dataset, so as to have
          * every day in the result set, whether or not we have a
          * book entry for the day.
          */
         generate_series(date :'start',
                         date :'start' + interval '1 month'
                                       - interval '1 day',
                         interval '1 day'
         )
         as calendar(entry)
         left join factbook
                on factbook.date = calendar.entry
order by date;
