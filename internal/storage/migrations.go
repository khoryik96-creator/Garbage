package storage

const migration2 = `ALTER TABLE runs ADD COLUMN protection TEXT NOT NULL DEFAULT 'fill_blanks';
ALTER TABLE writebacks ADD COLUMN field TEXT NOT NULL DEFAULT 'country';
ALTER TABLE writebacks ADD COLUMN guard_version INTEGER NOT NULL DEFAULT 0;
UPDATE writebacks SET guard_version=after_version;
INSERT INTO schema_migrations(version) VALUES(2);`
