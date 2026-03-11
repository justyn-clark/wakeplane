CREATE TABLE IF NOT EXISTS schedules (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  enabled INTEGER NOT NULL DEFAULT 1,
  schedule_kind TEXT NOT NULL CHECK (schedule_kind IN ('cron', 'interval', 'once')),
  schedule_spec_json TEXT NOT NULL,
  timezone TEXT NOT NULL,
  target_kind TEXT NOT NULL CHECK (target_kind IN ('http', 'shell', 'workflow')),
  target_spec_json TEXT NOT NULL,
  overlap_policy TEXT NOT NULL CHECK (overlap_policy IN ('allow', 'forbid', 'queue_latest', 'replace')),
  misfire_policy TEXT NOT NULL CHECK (misfire_policy IN ('skip', 'run_once_if_late', 'catch_up')),
  timeout_seconds INTEGER NOT NULL DEFAULT 300,
  max_concurrency INTEGER NOT NULL DEFAULT 1,
  retry_max_attempts INTEGER NOT NULL DEFAULT 0,
  retry_strategy TEXT NOT NULL DEFAULT 'exponential' CHECK (retry_strategy IN ('none', 'exponential')),
  retry_initial_delay_seconds INTEGER NOT NULL DEFAULT 30,
  retry_max_delay_seconds INTEGER NOT NULL DEFAULT 900,
  start_at TEXT NULL,
  end_at TEXT NULL,
  paused_at TEXT NULL,
  next_run_at TEXT NULL,
  last_run_at TEXT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS schedule_runs (
  id TEXT PRIMARY KEY,
  schedule_id TEXT NOT NULL,
  occurrence_key TEXT NOT NULL,
  nominal_time TEXT NOT NULL,
  due_time TEXT NOT NULL,
  status TEXT NOT NULL CHECK (
    status IN (
      'pending',
      'claimed',
      'running',
      'succeeded',
      'failed',
      'retry_scheduled',
      'dead_lettered',
      'cancelled',
      'skipped'
    )
  ),
  attempt INTEGER NOT NULL DEFAULT 1,
  claimed_by_worker_id TEXT NULL,
  claim_expires_at TEXT NULL,
  started_at TEXT NULL,
  finished_at TEXT NULL,
  http_status_code INTEGER NULL,
  exit_code INTEGER NULL,
  result_json TEXT NULL,
  error_text TEXT NULL,
  retry_available_at TEXT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (schedule_id) REFERENCES schedules(id) ON DELETE CASCADE,
  UNIQUE (occurrence_key, attempt)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_schedule_runs_occurrence_attempt
  ON schedule_runs (occurrence_key, attempt);

CREATE INDEX IF NOT EXISTS idx_schedule_runs_schedule_id
  ON schedule_runs (schedule_id);

CREATE INDEX IF NOT EXISTS idx_schedule_runs_status
  ON schedule_runs (status);

CREATE INDEX IF NOT EXISTS idx_schedule_runs_due_time_status
  ON schedule_runs (due_time, status);

CREATE INDEX IF NOT EXISTS idx_schedule_runs_retry_available_at
  ON schedule_runs (retry_available_at);

CREATE TABLE IF NOT EXISTS worker_leases (
  id TEXT PRIMARY KEY,
  worker_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  lease_key TEXT NOT NULL UNIQUE,
  acquired_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  heartbeat_at TEXT NOT NULL,
  FOREIGN KEY (run_id) REFERENCES schedule_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_worker_leases_worker_id
  ON worker_leases (worker_id);

CREATE INDEX IF NOT EXISTS idx_worker_leases_expires_at
  ON worker_leases (expires_at);

CREATE TABLE IF NOT EXISTS dead_letters (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  schedule_id TEXT NOT NULL,
  occurrence_key TEXT NOT NULL,
  reason TEXT NOT NULL,
  payload_json TEXT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY (run_id) REFERENCES schedule_runs(id) ON DELETE CASCADE,
  FOREIGN KEY (schedule_id) REFERENCES schedules(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_dead_letters_schedule_id
  ON dead_letters (schedule_id);

CREATE INDEX IF NOT EXISTS idx_dead_letters_created_at
  ON dead_letters (created_at);

CREATE TABLE IF NOT EXISTS execution_receipts (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  receipt_kind TEXT NOT NULL CHECK (receipt_kind IN ('stdout', 'stderr', 'http_response', 'workflow_result', 'summary')),
  content_type TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY (run_id) REFERENCES schedule_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_execution_receipts_run_id
  ON execution_receipts (run_id);
