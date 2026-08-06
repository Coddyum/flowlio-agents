-- Dropping the column loses which surface opened each edge. Nothing recomputes it afterwards: the
-- information only exists at write time, exactly like set_blocked.
ALTER TABLE task_dependencies DROP COLUMN origin;
