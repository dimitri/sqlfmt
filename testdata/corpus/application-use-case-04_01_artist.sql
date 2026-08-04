  select artist.name, count(*) as albums
    from artist
  left join album using(artist_id)
group by artist.name
order by albums desc
   limit :n;
