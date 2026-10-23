SET NAMES utf8mb4;

CREATE TABLE items (
  id INT PRIMARY KEY AUTO_INCREMENT,
  name VARCHAR(100) NOT NULL
);
INSERT INTO items (name) VALUES ('Original');

CREATE TABLE qa_records (
  id INT PRIMARY KEY AUTO_INCREMENT,
  codigo VARCHAR(30) NOT NULL UNIQUE,
  nombre VARCHAR(100) NOT NULL,
  notas TEXT NULL
);
CREATE TABLE qa_structure (id INT PRIMARY KEY AUTO_INCREMENT, nombre VARCHAR(100));
CREATE TABLE qa_drafts (id INT PRIMARY KEY AUTO_INCREMENT, nombre VARCHAR(100) NOT NULL);
INSERT INTO qa_drafts (nombre) VALUES ('Original');
CREATE TABLE qa_exports (
  id INT PRIMARY KEY AUTO_INCREMENT,
  codigo VARCHAR(30) NOT NULL UNIQUE,
  nombre VARCHAR(100) NOT NULL,
  notas TEXT NULL
);
INSERT INTO qa_exports (codigo, nombre, notas) VALUES
  ('QA-001', 'Muñoz, "prueba"', CONCAT('Línea uno', CHAR(10), 'Línea dos')),
  ('QA-002', 'Segundo registro', NULL);
