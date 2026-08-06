-- Rolls back 000011. Dropping the column loses only a derived value: it is recomputed by the
-- backfill of the up migration.

ALTER TABLE projects DROP COLUMN note_bytes;
