begin;

with new_visitors as
 (
   delete from tweet.visitor
         where id = any (  
                          select id
                            from tweet.visitor
                        order by datetime, messageid
                      for update
                     skip locked
                           limit 1000
                    )
     returning messageid,
               cast(datetime as date) as date,
               hll_hash_text(ipaddr::text) as visitors
 ),
    new_visitor_groups as
 (
    select messageid, date, hll_add_agg(visitors) as visitors
      from new_visitors
  group by messageid, date
 )
 insert into tweet.uniques
      select messageid, date, visitors
        from new_visitor_groups
 on conflict (messageid, date)
   do update set visitors = hll_union(uniques.visitors, excluded.visitors)
           where uniques.messageid = excluded.messageid
             and uniques.date = excluded.date
   returning messageid, date, # visitors as uniques;
       
rollback;
