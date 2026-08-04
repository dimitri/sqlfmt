with t(query) as (
  select format('(%s)',
                  array_to_string(array_agg(rowid), '&')
         )::query_int as query
    from tags
     where tag = 'blues' or tag = 'rhythm and blues'
)
   select track.tid,
          left(track.artist, 26) 
            || case when length(track.artist) > 26 then '…' else '' end
          as artist,
          left(track.title, 26)
            || case when length(track.title) > 26 then '…' else '' end
          as title
     from      track_tags tt
          join tids on tt.tid = tids.rowid
          join t on tt.tags @@ t.query
          join lastfm.track on tids.tid = track.tid
 order by artist;
