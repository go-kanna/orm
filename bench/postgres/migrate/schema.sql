-- The tables are in a schema of their own, apart from those of the example in the same database, and the URLs the
-- benchmarks run on choose it with ?search_path=bench.
DROP SCHEMA IF EXISTS bench CASCADE;
CREATE SCHEMA bench;
SET search_path TO bench;

CREATE TABLE customers (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email      text NOT NULL UNIQUE,
    name       text NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE orders (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id bigint NOT NULL REFERENCES customers,
    status      text NOT NULL,
    placed_at   timestamptz NOT NULL,
    note        text
);

CREATE INDEX orders_status_placed_at_id ON orders (status, placed_at, id);

CREATE TABLE order_lines (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id bigint NOT NULL REFERENCES orders ON DELETE CASCADE,
    sku      text NOT NULL,
    quantity integer NOT NULL
);

CREATE INDEX order_lines_order_id ON order_lines (order_id);
