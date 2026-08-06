-- Rolls back 000012. Destructive by nature: the entries are the data, not a derived value.

DROP TABLE IF EXISTS memories;
DROP TYPE IF EXISTS memory_kind;
ALTER TABLE projects DROP COLUMN IF EXISTS memory_bytes;
