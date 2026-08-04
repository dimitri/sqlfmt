begin;

create temp table batch
 (
        like moma.artist
   including all
 )
 on commit drop;

\copy batch from 'artists/artists.2017-07-01.csv' with csv header delimiter ','

with upd as
(
     update moma.artist
        set (name, bio, nationality, gender, begin, "end", wiki_qid, ulan)
        
          = (batch.name, batch.bio, batch.nationality,
             batch.gender, batch.begin, batch."end",
             batch.wiki_qid, batch.ulan)

       from batch
       
      where batch.constituentid = artist.constituentid

        and (artist.name, artist.bio, artist.nationality,
             artist.gender, artist.begin, artist."end",
             artist.wiki_qid, artist.ulan)
         <> (batch.name, batch.bio, batch.nationality,
             batch.gender, batch.begin, batch."end",
             batch.wiki_qid, batch.ulan)

  returning artist.constituentid
),
    ins as
(
    insert into moma.artist
         select constituentid, name, bio, nationality,
                gender, begin, "end", wiki_qid, ulan
           from batch
          where not exists
                (
                     select 1
                       from moma.artist
                      where artist.constituentid = batch.constituentid
                )
    on conflict (constituentid) do nothing
      returning artist.constituentid
)
select (select count(*) from upd) as updates,
       (select count(*) from ins) as inserts;

commit;
