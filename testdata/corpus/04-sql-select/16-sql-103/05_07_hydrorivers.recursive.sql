-- Loire: full basin via WITH RECURSIVE (6,297 reaches).
-- Automates the ring-by-ring approach indefinitely.
with recursive loire as (

       select hyriv_id, geom, ord_stra            -- base case: the outlet
         from hydrorivers.rivers
        where hyriv_id = 20446779

    union all

       select r.hyriv_id, r.geom, r.ord_stra       -- recursive term: one step upstream
         from hydrorivers.rivers as r
              join loire on r.next_down = loire.hyriv_id
)
select count(*) from loire;
