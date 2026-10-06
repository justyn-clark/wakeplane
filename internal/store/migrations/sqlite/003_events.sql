CREATE TABLE IF NOT EXISTS trigger_events (
    occurrence_key TEXT PRIMARY KEY,
    schedule_id TEXT NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
    event_json TEXT NOT NULL,
    run_id TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_trigger_events_schedule ON trigger_events(schedule_id);
