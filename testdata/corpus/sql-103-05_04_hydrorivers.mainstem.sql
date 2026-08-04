select hyriv_id, geom, ord_stra
  from hydrorivers.rivers
 where main_riv = 20446779
   and ord_stra >= 6;
