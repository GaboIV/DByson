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

CREATE TABLE qa_imports (id INT PRIMARY KEY, nombre VARCHAR(100) NOT NULL, notas TEXT NULL) ENGINE=InnoDB;
CREATE TABLE qa_parents (a INT, b INT, nombre VARCHAR(100), PRIMARY KEY(a,b)) ENGINE=InnoDB;
CREATE TABLE qa_children (id INT PRIMARY KEY, a INT, b INT, CONSTRAINT qa_parent_fk FOREIGN KEY(a,b) REFERENCES qa_parents(a,b)) ENGINE=InnoDB;
INSERT INTO qa_parents VALUES(10,20,'Padre compuesto');
INSERT INTO qa_children VALUES(1,10,20),(2,NULL,NULL);
