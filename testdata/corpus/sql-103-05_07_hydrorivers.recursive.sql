with loire as (
    select hyriv_id, geom, ord_stra
      from hydrorivers.rivers
     where hyriv_id = 20446779 union all
    select r.hyriv_id, r.geom, r.ord_stra
      from hydrorivers.rivers as r
      join loire on r.next_down = loire.hyriv_id
)
select count(*) from loire;
