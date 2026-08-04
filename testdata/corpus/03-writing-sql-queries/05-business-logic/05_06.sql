with four_albums as
(
   select artist_id
     from album
 group by artist_id
   having count(*) = 4
)
  select artist.name, album, duration
    from four_albums
         join artist using(artist_id),
         lateral get_all_albums(artist_id)
order by artist_id, duration desc;
