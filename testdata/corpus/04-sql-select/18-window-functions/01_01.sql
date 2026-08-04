 select x, array_agg(x) over (order by x)
   from generate_series(1, 3) as t(x);
