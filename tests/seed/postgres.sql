\connect app
CREATE TABLE notes (id int PRIMARY KEY, body text);
CREATE TABLE tags (id int);
INSERT INTO notes VALUES (1, 'hello');
\connect postgres
CREATE DATABASE app2;
\connect app2
CREATE TABLE items (id int PRIMARY KEY, name text);
INSERT INTO items VALUES (1, 'widget');
\connect postgres
CREATE ROLE dbb_ro LOGIN PASSWORD 'ropw' CREATEDB IN ROLE pg_read_all_data;
