-- 0006: downloads that finished sharing, and whether their data was deleted.

ALTER TABLE downloads ADD COLUMN data_removed INTEGER NOT NULL DEFAULT 0;
