select album, duration
  from artist,
       lateral get_all_albums(artist_id)
 where artist.name = 'Red Hot Chili Peppers';
