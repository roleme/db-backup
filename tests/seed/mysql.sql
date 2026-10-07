CREATE TABLE shop.posts (id INT PRIMARY KEY, body JSON);
CREATE TABLE shop.tags (id INT);
INSERT INTO shop.posts VALUES (1, '{"a": 1}');
GRANT ALL ON `dbb\_verify\_%`.* TO 'shop'@'%';
