 select x,
        array_agg(x) over (order by x
                           rows between unbounded preceding
                                    and current row)
   from generate_series(1, 3) as t(x);
