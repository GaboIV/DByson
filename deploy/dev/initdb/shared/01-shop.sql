-- Esquema de ejemplo para desarrollo y pruebas de integración.
USE shop;

CREATE TABLE customers (
  id         INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  name       VARCHAR(120) NOT NULL,
  email      VARCHAR(190) NOT NULL UNIQUE,
  profile    JSON NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB;

CREATE TABLE products (
  id    INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  sku   VARCHAR(40) NOT NULL UNIQUE,
  title VARCHAR(200) NOT NULL,
  price DECIMAL(10,2) NOT NULL,
  photo BLOB NULL
) ENGINE=InnoDB;

CREATE TABLE orders (
  id          INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  customer_id INT UNSIGNED NOT NULL,
  status      ENUM('pending','paid','shipped','cancelled') NOT NULL DEFAULT 'pending',
  placed_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_orders_customer (customer_id),
  CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id)
) ENGINE=InnoDB;

CREATE TABLE order_items (
  order_id   INT UNSIGNED NOT NULL,
  product_id INT UNSIGNED NOT NULL,
  quantity   SMALLINT UNSIGNED NOT NULL DEFAULT 1,
  PRIMARY KEY (order_id, product_id),
  CONSTRAINT fk_items_order   FOREIGN KEY (order_id)   REFERENCES orders (id),
  CONSTRAINT fk_items_product FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE=InnoDB;

INSERT INTO customers (name, email, profile) VALUES
  ('Ana Torres',  'ana@example.com',  '{"vip": true, "lang": "es"}'),
  ('Luis Rojas',  'luis@example.com', '{"vip": false, "lang": "es"}'),
  ('Mia Chen',    'mia@example.com',  NULL);

INSERT INTO products (sku, title, price) VALUES
  ('KB-001', 'Teclado mecánico', 89.90),
  ('MS-002', 'Mouse inalámbrico', 29.50),
  ('MN-003', 'Monitor 27"', 319.00);

INSERT INTO orders (customer_id, status) VALUES (1, 'paid'), (1, 'pending'), (2, 'shipped');

INSERT INTO order_items (order_id, product_id, quantity) VALUES
  (1, 1, 1), (1, 2, 2), (2, 3, 1), (3, 2, 1);
