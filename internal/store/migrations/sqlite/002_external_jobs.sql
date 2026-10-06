CREATE TABLE IF NOT EXISTS external_jobs (
  occurrence_key TEXT PRIMARY KEY,
  schedule_id TEXT NOT NULL,
  checkpoint_json TEXT NOT NULL,
  job_status TEXT NOT NULL CHECK (job_status IN ('submitting', 'queued', 'running', 'succeeded', 'failed', 'cancelled')),
  compacted INTEGER NOT NULL DEFAULT 0,
  target_snapshot_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (schedule_id) REFERENCES schedules(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_external_jobs_schedule_id ON external_jobs (schedule_id);
