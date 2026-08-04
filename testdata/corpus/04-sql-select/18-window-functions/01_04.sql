select x,
       array_agg(x) over () as frame,
       sum(x) over () as sum,
       x::float/sum(x) over () as part
  from generate_series(1, 3) as t(x);
