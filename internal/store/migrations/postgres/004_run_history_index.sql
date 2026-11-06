-- Match timestampSQL(created_at) so RFC3339Nano chronology and cursor ordering
-- use an index without rewriting historical timestamps.
CREATE INDEX IF NOT EXISTS idx_schedule_runs_created_normalized_id ON schedule_runs (
    (substr(created_at, 1, 19) || '.' || substr(CASE WHEN substr(created_at, 20, 1) = '.' THEN substr(created_at, 21, length(created_at) - 21) ELSE '' END || '000000000', 1, 9) || 'Z') DESC,
    id DESC
);
