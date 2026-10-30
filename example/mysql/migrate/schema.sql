DROP TABLE IF EXISTS order_tags, tags, shipments, order_lines, orders, products, customers;

CREATE TABLE customers (
    id         bigint AUTO_INCREMENT PRIMARY KEY,
    email      varchar(255) NOT NULL UNIQUE,
    name       varchar(255) NOT NULL,
    created_at datetime(6) NOT NULL
);

CREATE TABLE products (
    sku   varchar(32) PRIMARY KEY,
    name  varchar(255) NOT NULL,
    price bigint NOT NULL
);

CREATE TABLE orders (
    id          bigint AUTO_INCREMENT PRIMARY KEY,
    customer_id bigint NOT NULL,
    status      varchar(16) NOT NULL,
    placed_at   datetime(6) NOT NULL,
    note        text,
    FOREIGN KEY (customer_id) REFERENCES customers (id)
);

CREATE TABLE order_lines (
    order_id bigint NOT NULL,
    line_no  int NOT NULL,
    sku      varchar(32) NOT NULL,
    quantity int NOT NULL,
    PRIMARY KEY (order_id, line_no),
    FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE,
    FOREIGN KEY (sku) REFERENCES products (sku)
);

CREATE TABLE shipments (
    order_id bigint PRIMARY KEY,
    carrier  varchar(64) NOT NULL,
    tracking varchar(64) NOT NULL,
    FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE
);

CREATE TABLE tags (name varchar(64) PRIMARY KEY);

CREATE TABLE order_tags (
    order_id bigint NOT NULL,
    tag      varchar(64) NOT NULL,
    PRIMARY KEY (order_id, tag),
    FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE,
    FOREIGN KEY (tag) REFERENCES tags (name)
);
