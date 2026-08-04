begin;

create table tweet
 (
   id         bigint primary key,
   date       date,
   hour       time,
   uname      text,
   nickname   text,
   bio        text,
   message    text,
   favs       bigint,
   rts        bigint,
   latitude   double precision,
   longitude  double precision,
   country    text,
   place      text,
   picture    text,
   followers  bigint,
   following  bigint,
   listed     bigint,
   lang       text,
   url        text
 );

\copy tweet from 'tweets.csv' with csv header delimiter ';'

commit;
