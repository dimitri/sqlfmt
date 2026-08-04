select x,
       array_agg(x) over (rows between current row
                                   and unbounded following)
  from generate_series(1, 3) as t(x);
