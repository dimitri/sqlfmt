begin;

create schema if not exists sandbox;

create table sandbox.category
 (
   id    serial primary key,
   name  text not null
 );

insert into sandbox.category(name)
     values ('sport'),('news'),('box office'),('music');

create table sandbox.article
 (
   id         bigserial primary key,
   category   integer references sandbox.category(id),
   title      text not null,
   content    text
 );

create table sandbox.comment
 (
   id         bigserial primary key,
   article    integer references sandbox.article(id),
   content    text
 );

insert into sandbox.article(category, title, content)
     select random(1, 4) as category,
            initcap(sandbox.lorem(5)) as title,
            sandbox.lorem(100) as content
       from generate_series(1, 1000) as t(x);

insert into sandbox.comment(article, content)
     select random(1, 1000) as article,
            sandbox.lorem(150) as content
       from generate_series(1, 50000) as t(x);
            
select article.id, category.name, title
  from      sandbox.article
       join sandbox.category
         on category.id = article.category
 limit 3;

select count(*),
       avg(length(title))::int as avg_title_length,
       avg(length(content))::int as avg_content_length
  from sandbox.article;

   select article.id, article.title, count(*)
     from      sandbox.article
          join sandbox.comment
            on article.id = comment.article
group by article.id
order by count desc
   limit 5;

select category.name,
       count(distinct article.id) as articles,
       count(*) as comments
  from      sandbox.category
       left join sandbox.article on article.category = category.id
       left join sandbox.comment on comment.article = article.id
group by category.name
order by category.name;

rollback;
