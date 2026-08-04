\set start '2017-02-01'

     select cast (calendar.entry as date) as date,
            coalesce(shares, 0) as shares,
            coalesce(trades, 0) as trades,
            to_char(coalesce(dollars, 0), 'L99G999G999G999') as dollars
       from generate_series(date : 'start', date : 'start' + interval '1 month' - interval '1 day', interval '1 day') as calendar(entry)
  left join factbook on factbook.date = calendar.entry
   order by date;
