select users.userid as follower,
       users.uname,
       f.userid as following,
       f.uname
  from      tweet.users
       join tweet.users f
         on f.uname = substring(users.bio from 'in love with #?(.*).')
 where users.bio ~ 'in love with';
