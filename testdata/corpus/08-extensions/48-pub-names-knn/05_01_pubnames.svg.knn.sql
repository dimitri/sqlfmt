-- The 10 nearest pubs to Holborn (03_01, extended to limit 10) rendered as a
-- single <svg> document: OSM streets from osm_london.roads for context, the
-- search point in red, the nearest pubs in blue with their names.
with search as (
  select point(-0.12, 51.516) as pt
),
nearest as (
  select id, name, pos,
         row_number() over (order by pos <-> (select pt from search)) as rn
    from pubnames
order by pos <-> (select pt from search)
   limit 10
),
bbox as (
  select least(min((pos)[0]), min((select (pt)[0] from search))) - 0.003 as x0,
         greatest(max((pos)[0]), max((select (pt)[0] from search))) + 0.003 as x1,
         least(min((pos)[1]), min((select (pt)[1] from search))) - 0.002 as y0,
         greatest(max((pos)[1]), max((select (pt)[1] from search))) + 0.002 as y1
    from nearest
),
proj as (
  -- project lon/lat degrees onto a plain ~800-unit canvas before drawing
  -- anything, so every SVG number (stroke-width, radius, font-size) is an
  -- ordinary integer instead of a sub-0.001 fraction — some renderers don't
  -- reliably handle attribute values at that scale.
  select x0, y0, x1, y1, 800.0 / greatest(x1 - x0, y1 - y0) as scale
    from bbox
),
win as (
  select st_makeenvelope(x0, y0, x1, y1, 4326) as env from bbox
),
roads as (
  select st_transscale(st_intersection(r.geom, win.env),
                        -proj.x0, -proj.y0, proj.scale, proj.scale) as geom
    from osm_london.roads r, win, proj
   where st_intersects(r.geom, win.env)
),
layers as (
  select 1 as z, '<path d="' || st_assvg(geom, 0, 1) ||
         '" fill="none" stroke="#C0B8AE" stroke-width="2"/>' as elem
    from roads
  union all
  select 2, '<circle cx="' || (((pt)[0]-proj.x0)*proj.scale)::text || '" cy="' ||
         (-(((pt)[1]-proj.y0)*proj.scale))::text ||
         '" r="9" fill="#B04020" stroke="#F2EFE9" stroke-width="2"/>' as elem
    from search, proj
  union all
  select 3,
         '<circle cx="' || (((pos)[0]-proj.x0)*proj.scale)::text || '" cy="' ||
         (-(((pos)[1]-proj.y0)*proj.scale))::text ||
         '" r="6" fill="#5B8DB8" stroke="#F2EFE9" stroke-width="1.5"/>' ||
         '<text x="' || (((pos)[0]-proj.x0)*proj.scale + 9)::text || '" y="' ||
         (-(((pos)[1]-proj.y0)*proj.scale) + (case when rn % 2 = 0 then 9 else -4 end))::text ||
         '" font-size="13" fill="#2C2820">' ||
         replace(replace(name, '&', '&amp;'), '<', '&lt;') || '</text>' as elem
    from nearest, proj
)
select '<svg viewBox="0 ' || (-((proj.y1-proj.y0)*proj.scale)) || ' ' ||
       ((proj.x1-proj.x0)*proj.scale) || ' ' || ((proj.y1-proj.y0)*proj.scale) ||
       '" xmlns="http://www.w3.org/2000/svg">' ||
       '<rect x="0" y="' || (-((proj.y1-proj.y0)*proj.scale)) || '" width="' ||
       ((proj.x1-proj.x0)*proj.scale) || '" height="' || ((proj.y1-proj.y0)*proj.scale) ||
       '" fill="#F2EFE9"/>' ||
       string_agg(layers.elem, '' order by layers.z) || '</svg>' as svg
  from layers, proj
 group by proj.x0, proj.y0, proj.x1, proj.y1, proj.scale;
