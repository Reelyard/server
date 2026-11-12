-- 0011: the audio language of a supplier's releases, used when a release
-- name doesn't say. '' = not set (untagged releases count as English).
ALTER TABLE suppliers ADD COLUMN language TEXT NOT NULL DEFAULT '';
