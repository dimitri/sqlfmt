  select album.title as album, sum(milliseconds) * interval '1 ms' as duration
    from album
       join artist using(artist_id)
  left join track using(album_id)
   where artist.name = :name
group by album
order by album;
