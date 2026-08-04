   update tweet.users
      set nickname = case when uname ~ ' '
                          then substring(uname from '[^ ]* (.*)')
                          else uname
                      end
    where nickname is null
returning users.*;
