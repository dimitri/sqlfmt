select *
  from get_all_albums(
         (select artist_id
            from artist
           where name = 'Red Hot Chili Peppers')
       );
