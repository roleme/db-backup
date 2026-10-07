CREATE TABLE shop.posts (id INT PRIMARY KEY, body JSON);
CREATE TABLE shop.tags (id INT);
INSERT INTO shop.posts VALUES (1, '{"a": 1}');
CREATE FUNCTION shop.one() RETURNS INT DETERMINISTIC RETURN 1;
CREATE TRIGGER shop.tags_default BEFORE INSERT ON shop.tags FOR EACH ROW SET NEW.id = IFNULL(NEW.id, 0);
CREATE USER 'bkp'@'%' IDENTIFIED BY 'bkppw';
GRANT SELECT, SHOW VIEW, TRIGGER ON shop.* TO 'bkp'@'%';
GRANT SHOW_ROUTINE ON *.* TO 'bkp'@'%';
GRANT ALL ON `dbb\_verify\_%`.* TO 'bkp'@'%';
