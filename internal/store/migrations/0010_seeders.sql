-- 0010: how many seeders a download's release had when it was found, so the
-- queue can start the best-seeded downloads first. -1 = not known.
ALTER TABLE downloads ADD COLUMN seeders INTEGER NOT NULL DEFAULT -1;
