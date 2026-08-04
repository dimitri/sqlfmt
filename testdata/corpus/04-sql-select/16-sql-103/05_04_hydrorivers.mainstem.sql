-- Loire main channels: flat query on stream order.
-- Seed for the manual ring-by-ring approach that leads to WITH RECURSIVE.
-- 155 reaches: the trunk and its biggest branches.
select hyriv_id, geom, ord_stra
  from hydrorivers.rivers
 where main_riv = 20446779   -- the Loire basin
   and ord_stra >= 6;        -- trunk and major tributaries only
