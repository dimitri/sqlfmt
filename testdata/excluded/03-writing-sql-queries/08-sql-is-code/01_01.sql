SELECT title, name FROM album LEFT JOIN track USING(album_id) WHERE album_id = 1 ORDER BY 2;
