CREATE TABLE products (
    product_no integer,
    name text,
    price numeric CHECK (price > 0)
);
