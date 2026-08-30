explain (analyze, buffers)
 select res.raceid, res.driverid, res.positionorder, res.points
   from f1db.results res
  where res.points in (25, 18, 15, 12, 10, 8, 6, 4, 2, 1);
