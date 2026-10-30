DROP TABLE IF EXISTS order_tags, tags, shipments, order_lines, orders, products, customers;

CREATE TABLE customers (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email      text NOT NULL UNIQUE,
    name       text NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE products (
    sku   text PRIMARY KEY,
    name  text NOT NULL,
    price bigint NOT NULL
);

CREATE TABLE orders (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id bigint NOT NULL REFERENCES customers,
    status      text NOT NULL,
    placed_at   timestamptz NOT NULL,
    note        text
);

CREATE TABLE order_lines (
    order_id bigint NOT NULL REFERENCES orders ON DELETE CASCADE,
    line_no  integer NOT NULL,
    sku      text NOT NULL REFERENCES products,
    quantity integer NOT NULL,
    PRIMARY KEY (order_id, line_no)
);

CREATE TABLE shipments (
    order_id bigint PRIMARY KEY REFERENCES orders ON DELETE CASCADE,
    carrier  text NOT NULL,
    tracking text NOT NULL
);

CREATE TABLE tags (name text PRIMARY KEY);

CREATE TABLE order_tags (
    order_id bigint NOT NULL REFERENCES orders ON DELETE CASCADE,
    tag      text NOT NULL REFERENCES tags,
    PRIMARY KEY (order_id, tag)
);
