-- name: axm-get-column-presets
-- The column presets people made, by name.
SELECT id, name, columns FROM axm_column_presets ORDER BY lower(name), id;

-- name: axm-get-column-preset
SELECT id, name, columns FROM axm_column_presets WHERE id = $1;

-- name: axm-column-preset-exists
SELECT EXISTS (SELECT 1 FROM axm_column_presets WHERE id = $1);

-- name: axm-create-column-preset
-- Inserts only while there are fewer than $3 presets (no row back = too many).
INSERT INTO axm_column_presets (name, columns)
SELECT $1::TEXT, $2::TEXT[] WHERE (SELECT COUNT(*) FROM axm_column_presets) < $3::INT RETURNING id;

-- name: axm-update-column-preset
UPDATE axm_column_presets SET name = $2, columns = $3, updated_at = NOW() WHERE id = $1;

-- name: axm-delete-column-preset
DELETE FROM axm_column_presets WHERE id = $1;

-- name: axm-column-preset-usage
-- How many lists use each preset ID (built-in ones and 'u<id>').
SELECT axm_preset AS preset, COUNT(*) AS lists FROM lists GROUP BY axm_preset;

-- name: axm-column-preset-list-names
-- Up to $2 lists per preset ID in $1, by name (for the delete confirmation).
SELECT axm_preset AS preset, id, name FROM (
    SELECT axm_preset, id, name, ROW_NUMBER() OVER (PARTITION BY axm_preset ORDER BY name, id) AS n
    FROM lists WHERE axm_preset = ANY($1::TEXT[])
) x WHERE n <= $2 ORDER BY axm_preset, n;

-- name: axm-reset-preset-lists
-- Puts the lists that use preset $1 on preset $2; returns how many.
UPDATE lists SET axm_preset = $2, updated_at = NOW() WHERE axm_preset = $1;
