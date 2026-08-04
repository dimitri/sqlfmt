begin;

create or replace function twcache.tg_notify_counters ()
 returns trigger
 language plpgsql
as $$
declare
  channel text := TG_ARGV[0];
begin
  PERFORM (
     with payload(messageid, rts, favs) as
     (
       select NEW.messageid,
              coalesce(
                 case NEW.action
                   when 'rt'    then  1
                   when 'de-rt' then -1
                  end,
                 0
              ) as rts,
              coalesce(
                case NEW.action
                  when 'fav'    then  1
                  when 'de-fav' then -1
                 end,
                0
              ) as favs
     )
     select pg_notify(channel, row_to_json(payload)::text)
       from payload
  );
  RETURN NULL;
end;
$$;

CREATE TRIGGER notify_counters
         AFTER INSERT
            ON tweet.activity
      FOR EACH ROW
       EXECUTE PROCEDURE twcache.tg_notify_counters('tweet.activity');

commit;
