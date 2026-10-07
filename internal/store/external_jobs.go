package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

var ErrLeaseLost = errors.New("execution lease is no longer owned")

// ExecutionLeaseToken identifies one claim, including when a worker reclaims the
// same run. Worker IDs alone cannot fence an older execution from a newer claim.
func (s *Store) ExecutionLeaseToken(ctx context.Context, runID, workerID string) (string, error) {
	var token string
	err := s.queryRow(ctx, `SELECT id FROM worker_leases WHERE run_id = ? AND worker_id = ?`, runID, workerID).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrLeaseLost
	}
	return token, err
}

func (s *Store) GetExternalJob(ctx context.Context, occurrenceKey string) (*domain.ExternalJob, error) {
	var checkpoint, target string
	err := s.queryRow(ctx, `SELECT checkpoint_json, target_snapshot_json FROM external_jobs WHERE occurrence_key = ?`, occurrenceKey).Scan(&checkpoint, &target)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeExternalJob(checkpoint, target)
}

func decodeExternalJob(checkpoint, target string) (*domain.ExternalJob, error) {
	var job domain.ExternalJob
	if err := json.Unmarshal([]byte(checkpoint), &job); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(target), &job.RequestTarget); err != nil {
		return nil, err
	}
	job.CanReconcile = job.ReconciliationAvailable()
	return &job, nil
}

// lockOwnedRun serializes checkpoint/finalization with claim recovery. All state
// writes require the exact unexpired lease token, not just a worker ID.
func (s *Store) lockOwnedRun(ctx context.Context, tx *sql.Tx, runID, workerID, token string, now time.Time) error {
	return s.lockRunLease(ctx, tx, runID, workerID, token, now, true)
}

func (s *Store) lockRunLease(ctx context.Context, tx *sql.Tx, runID, workerID, token string, now time.Time, requireUnexpired bool) error {
	query := `SELECT status, claimed_by_worker_id FROM schedule_runs WHERE id = ?`
	if s.dialect == "postgres" {
		query += ` FOR UPDATE`
	}
	var status domain.RunStatus
	var owner sql.NullString
	if err := s.txQueryRow(ctx, tx, query, runID).Scan(&status, &owner); err != nil {
		return err
	}
	if (status != domain.RunRunning && status != domain.RunClaimed) || !owner.Valid || owner.String != workerID {
		return ErrLeaseLost
	}
	var expires string
	if err := s.txQueryRow(ctx, tx, `SELECT expires_at FROM worker_leases WHERE id = ? AND run_id = ? AND worker_id = ?`, token, runID, workerID).Scan(&expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		return err
	}
	if requireUnexpired && !mustParseTime(expires).After(now) {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) SaveExternalJob(ctx context.Context, run domain.Run, workerID, token string, job domain.ExternalJob, now time.Time) error {
	job.CanReconcile = job.ReconciliationAvailable()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.lockOwnedRun(ctx, tx, run.ID, workerID, token, now); err != nil {
		return err
	}
	var priorCheckpoint, priorTarget string
	err = s.txQueryRow(ctx, tx, `SELECT checkpoint_json, target_snapshot_json FROM external_jobs WHERE occurrence_key = ?`, run.OccurrenceKey).Scan(&priorCheckpoint, &priorTarget)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		prior, err := decodeExternalJob(priorCheckpoint, priorTarget)
		if err != nil {
			return err
		}
		if !prior.DeadlineAt.Equal(job.DeadlineAt) || !prior.SubmittedAt.Equal(job.SubmittedAt) || priorTarget != mustJSONString(job.RequestTarget) {
			return errors.New("external job execution snapshot is immutable")
		}
		if prior.JobID != "" && (prior.JobID != job.JobID || prior.StatusURL != job.StatusURL) {
			return errors.New("external job identity is immutable")
		}
		prior.UpdatedAt = job.UpdatedAt
		if prior.Terminal() && mustJSONString(*prior) != mustJSONString(job) {
			return errors.New("external job terminal checkpoint is immutable")
		}
	}
	job.UpdatedAt = now
	_, err = s.txExec(ctx, tx, `
		INSERT INTO external_jobs (occurrence_key, schedule_id, checkpoint_json, job_status, target_snapshot_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (occurrence_key) DO UPDATE SET checkpoint_json = EXCLUDED.checkpoint_json, job_status = EXCLUDED.job_status, updated_at = EXCLUDED.updated_at
	`, run.OccurrenceKey, run.ScheduleID, mustJSONString(job), job.Status, mustJSONString(job.RequestTarget), timeString(job.SubmittedAt), timeString(now))
	if err != nil {
		return err
	}
	// Preserve observations in the existing append-only receipt ledger. Avoid
	// filling it with identical polling responses; timestamps are not evidence.
	var prior domain.ExternalJob
	if priorCheckpoint != "" {
		_ = json.Unmarshal([]byte(priorCheckpoint), &prior)
	}
	prior.UpdatedAt = job.UpdatedAt
	if priorCheckpoint == "" || mustJSONString(prior) != mustJSONString(job) {
		body := truncateReceiptBody(mustJSONString(job), s.receiptMaxBytes)
		_, err = s.txExec(ctx, tx, `INSERT INTO execution_receipts (id, run_id, receipt_kind, content_type, body, created_at) VALUES (?, ?, 'summary', 'application/json', ?, ?)`, domain.NewID("rcpt"), run.ID, body, timeString(now))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) MarkRunRunningWithLease(ctx context.Context, runID, workerID, token string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.lockOwnedRun(ctx, tx, runID, workerID, token, now); err != nil {
		return err
	}
	if _, err := s.txExec(ctx, tx, `UPDATE schedule_runs SET status = 'running', started_at = COALESCE(started_at, ?), updated_at = ? WHERE id = ?`, timeString(now), timeString(now), runID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FinishRunWithLease(ctx context.Context, run domain.Run, workerID, token string, now time.Time) error {
	return s.finishExternalRun(ctx, run, workerID, token, nil, nil, now, false)
}

// FinishExternalFailure commits the failed attempt and its follow-up together.
// A crash cannot strand accepted remote work between failure and retry insertion.
func (s *Store) FinishExternalFailure(ctx context.Context, run domain.Run, workerID, token string, retry *domain.Run, dead *domain.DeadLetter, now time.Time) error {
	return s.finishExternalRun(ctx, run, workerID, token, retry, dead, now, false)
}

// FinishExpiredRunFailure recovers an ordinary running attempt atomically with
// its retry/dead letter and lease removal. Recheck expiry under the run lock so
// a heartbeat after the caller's discovery snapshot fences stale recovery.
func (s *Store) FinishExpiredRunFailure(ctx context.Context, run domain.Run, retry *domain.Run, dead *domain.DeadLetter, now time.Time) error {
	return s.finishExternalRun(ctx, run, "", "", retry, dead, now, true)
}

func (s *Store) lockExpiredRunningRun(ctx context.Context, tx *sql.Tx, runID string, now time.Time) (string, string, error) {
	query := `SELECT status, claimed_by_worker_id FROM schedule_runs WHERE id = ?`
	if s.dialect == "postgres" {
		query += ` FOR UPDATE`
	}
	var status domain.RunStatus
	var owner sql.NullString
	if err := s.txQueryRow(ctx, tx, query, runID).Scan(&status, &owner); err != nil {
		return "", "", err
	}
	if status != domain.RunRunning || !owner.Valid {
		return "", "", ErrLeaseLost
	}
	var token, workerID, expires string
	if err := s.txQueryRow(ctx, tx, `SELECT id, worker_id, expires_at FROM worker_leases WHERE run_id = ?`, runID).Scan(&token, &workerID, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", ErrLeaseLost
		}
		return "", "", err
	}
	if workerID != owner.String || mustParseTime(expires).After(now) {
		return "", "", ErrLeaseLost
	}
	return workerID, token, nil
}

func (s *Store) finishExternalRun(ctx context.Context, run domain.Run, workerID, token string, retry *domain.Run, dead *domain.DeadLetter, now time.Time, expired bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// A cancelled executor may finish after its lease expires during shutdown.
	// It may record that outcome only while the exact claim is still current;
	// recovery or a newer claim changes/removes the token and fences this write.
	if expired {
		workerID, token, err = s.lockExpiredRunningRun(ctx, tx, run.ID, now)
		if err != nil {
			return err
		}
	} else if err := s.lockRunLease(ctx, tx, run.ID, workerID, token, now, false); err != nil {
		return err
	}
	_, err = s.txExec(ctx, tx, `UPDATE schedule_runs SET status = ?, claimed_by_worker_id = ?, claim_expires_at = NULL, started_at = ?, finished_at = ?, http_status_code = ?, exit_code = ?, result_json = ?, error_text = ?, retry_available_at = ?, updated_at = ? WHERE id = ?`, run.Status, workerID, timePtrString(run.StartedAt), timePtrString(run.FinishedAt), intPtr(run.HTTPStatusCode), intPtr(run.ExitCode), rawJSON(run.ResultJSON), stringPtr(run.ErrorText), timePtrString(run.RetryAvailableAt), timeString(now), run.ID)
	if err != nil {
		return err
	}
	if _, err := s.txExec(ctx, tx, `DELETE FROM worker_leases WHERE id = ?`, token); err != nil {
		return err
	}
	if retry != nil {
		if _, err := s.txExec(ctx, tx, `INSERT INTO schedule_runs (id, schedule_id, occurrence_key, nominal_time, due_time, status, attempt, retry_available_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (occurrence_key, attempt) DO NOTHING`, retry.ID, retry.ScheduleID, retry.OccurrenceKey, timeString(retry.NominalTime), timeString(retry.DueTime), retry.Status, retry.Attempt, timePtrString(retry.RetryAvailableAt), timeString(retry.CreatedAt), timeString(retry.UpdatedAt)); err != nil {
			return err
		}
	}
	if dead != nil {
		if _, err := s.txExec(ctx, tx, `INSERT INTO dead_letters (id, run_id, schedule_id, occurrence_key, reason, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, dead.ID, dead.RunID, dead.ScheduleID, dead.OccurrenceKey, dead.Reason, rawJSON(dead.PayloadJSON), timeString(dead.CreatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RequeueExternalRun preserves the original attempt and absolute job deadline.
// An owned release is used on shutdown; an expired release is used on recovery.
func (s *Store) RequeueExternalRun(ctx context.Context, runID, workerID, token string, now time.Time, expired bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if !expired {
		if err := s.lockOwnedRun(ctx, tx, runID, workerID, token, now); err != nil {
			return err
		}
	} else {
		query := `SELECT status FROM schedule_runs WHERE id = ?`
		if s.dialect == "postgres" {
			query += ` FOR UPDATE`
		}
		var status domain.RunStatus
		var expires string
		if err := s.txQueryRow(ctx, tx, query, runID).Scan(&status); err != nil {
			return err
		}
		// Read the lease AFTER acquiring the run lock. A join snapshot acquired
		// before waiting for a heartbeat's lock can contain the old expiry.
		if err := s.txQueryRow(ctx, tx, `SELECT id, expires_at FROM worker_leases WHERE run_id = ?`, runID).Scan(&token, &expires); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLeaseLost
			}
			return err
		}
		if status != domain.RunRunning || mustParseTime(expires).After(now) {
			return ErrLeaseLost
		}
	}
	if _, err := s.txExec(ctx, tx, `UPDATE schedule_runs SET status = 'pending', claimed_by_worker_id = NULL, claim_expires_at = NULL, updated_at = ? WHERE id = ?`, timeString(now), runID); err != nil {
		return err
	}
	if _, err := s.txExec(ctx, tx, `DELETE FROM worker_leases WHERE id = ?`, token); err != nil {
		return err
	}
	message := fmt.Sprintf("external job tracking released for resume; expired=%t", expired)
	if _, err := s.txExec(ctx, tx, `INSERT INTO execution_receipts (id, run_id, receipt_kind, content_type, body, created_at) VALUES (?, ?, 'summary', 'text/plain', ?, ?)`, domain.NewID("rcpt"), runID, message, timeString(now)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RenewLeaseWithToken(ctx context.Context, runID, workerID, token string, now time.Time, ttl time.Duration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.lockOwnedRun(ctx, tx, runID, workerID, token, now); err != nil {
		return err
	}
	expires := now.Add(ttl)
	if _, err := s.txExec(ctx, tx, `UPDATE worker_leases SET expires_at = ?, heartbeat_at = ? WHERE id = ?`, timeString(expires), timeString(now), token); err != nil {
		return err
	}
	if _, err := s.txExec(ctx, tx, `UPDATE schedule_runs SET claim_expires_at = ?, updated_at = ? WHERE id = ?`, timeString(expires), timeString(now), runID); err != nil {
		return err
	}
	return tx.Commit()
}

// A completion only advances runtime fields; it cannot overwrite an operator's
// schedule edits made while a long remote job was being observed.
func (s *Store) RecordScheduleCompletion(ctx context.Context, scheduleID string, now time.Time) error {
	_, err := s.exec(ctx, `UPDATE schedules SET last_run_at = ?, updated_at = ? WHERE id = ?`, timeString(now), timeString(now), scheduleID)
	return err
}

const activeOccurrenceQuery = `SELECT COUNT(*) FROM (
	SELECT occurrence_key FROM schedule_runs WHERE schedule_id = ? AND occurrence_key <> ? AND status IN ('claimed', 'running')
	UNION
	SELECT occurrence_key FROM external_jobs WHERE schedule_id = ? AND occurrence_key <> ? AND job_status IN ('submitting', 'queued', 'running')
) active_occurrences`

func (s *Store) ActiveOtherOccurrenceCount(ctx context.Context, scheduleID, occurrenceKey string) (int, error) {
	var count int
	err := s.queryRow(ctx, activeOccurrenceQuery, scheduleID, occurrenceKey, scheduleID, occurrenceKey).Scan(&count)
	return count, err
}

// Compact only confirmed terminal work. Unresolved remote work retains its
// status URL and authentication snapshot until an operator reconciles it.
func (s *Store) compactPrunedExternalJobs(ctx context.Context, tx *sql.Tx) error {
	rows, err := s.txQuery(ctx, tx, `SELECT occurrence_key, checkpoint_json FROM external_jobs ej WHERE compacted = 0 AND job_status IN ('succeeded', 'failed', 'cancelled') AND NOT EXISTS (SELECT 1 FROM schedule_runs sr WHERE sr.occurrence_key = ej.occurrence_key)`)
	if err != nil {
		return err
	}
	type tombstone struct {
		key string
		job domain.ExternalJob
	}
	var items []tombstone
	for rows.Next() {
		var item tombstone
		var checkpoint string
		if err := rows.Scan(&item.key, &checkpoint); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal([]byte(checkpoint), &item.job); err != nil {
			rows.Close()
			return err
		}
		item.job.StatusURL = ""
		item.job.Progress, item.job.Result, item.job.Artifacts = nil, nil, nil
		item.job.Error = ""
		item.job.RequestTarget = domain.TargetSpec{}
		item.job.Compacted = true
		item.job.CanReconcile = false
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		if _, err := s.txExec(ctx, tx, `UPDATE external_jobs SET checkpoint_json = ?, target_snapshot_json = '{}', compacted = 1 WHERE occurrence_key = ? AND compacted = 0`, mustJSONString(item.job), item.key); err != nil {
			return err
		}
	}
	return nil
}

// ReconcileExternalJob records an operator's bounded status observation without
// changing historical run outcomes. It must never race an active/retry attempt.
func (s *Store) ReconcileExternalJob(ctx context.Context, run domain.Run, expected, observed domain.ExternalJob, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if s.dialect == "postgres" {
		var id string
		if err := s.txQueryRow(ctx, tx, `SELECT id FROM schedules WHERE id = ? FOR UPDATE`, run.ScheduleID).Scan(&id); err != nil {
			return err
		}
	}
	var active int
	if err := s.txQueryRow(ctx, tx, `SELECT COUNT(*) FROM schedule_runs WHERE occurrence_key = ? AND status IN ('pending', 'claimed', 'running', 'retry_scheduled')`, run.OccurrenceKey).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return ErrConflict
	}
	var checkpoint, target string
	if err := s.txQueryRow(ctx, tx, `SELECT checkpoint_json, target_snapshot_json FROM external_jobs WHERE occurrence_key = ?`, run.OccurrenceKey).Scan(&checkpoint, &target); err != nil {
		return err
	}
	current, err := decodeExternalJob(checkpoint, target)
	if err != nil {
		return err
	}
	if current.Compacted || current.Terminal() || mustJSONString(*current) != mustJSONString(expected) || target != mustJSONString(expected.RequestTarget) {
		return ErrConflict
	}
	if current.JobID == "" {
		if current.Status != domain.ExternalJobSubmitting || current.RequestTarget.HTTPJob == nil || current.RequestTarget.HTTPJob.LookupURL == "" || observed.JobID == "" || observed.StatusURL == "" {
			return ErrConflict
		}
	} else if current.JobID != observed.JobID || current.StatusURL != observed.StatusURL {
		return ErrConflict
	}
	if !current.DeadlineAt.Equal(observed.DeadlineAt) || !current.SubmittedAt.Equal(observed.SubmittedAt) || target != mustJSONString(observed.RequestTarget) {
		return ErrConflict
	}
	observed.UpdatedAt = now
	observed.CanReconcile = observed.ReconciliationAvailable()
	if _, err := s.txExec(ctx, tx, `UPDATE external_jobs SET checkpoint_json = ?, job_status = ?, updated_at = ? WHERE occurrence_key = ?`, mustJSONString(observed), observed.Status, timeString(now), run.OccurrenceKey); err != nil {
		return err
	}
	// The API selected an existing run. Preserve reconciliation evidence there,
	// alongside its original failure instead of rewriting that failure as success.
	if _, err := s.txExec(ctx, tx, `INSERT INTO execution_receipts (id, run_id, receipt_kind, content_type, body, created_at) VALUES (?, ?, 'summary', 'application/json', ?, ?)`, domain.NewID("rcpt"), run.ID, truncateReceiptBody(mustJSONString(map[string]any{"action": "external_job_reconciled", "job": observed}), s.receiptMaxBytes), timeString(now)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UnresolvedExternalJobCount(ctx context.Context, scheduleID string) (int, error) {
	var count int
	err := s.queryRow(ctx, `SELECT COUNT(*) FROM external_jobs WHERE schedule_id = ? AND job_status IN ('submitting', 'queued', 'running')`, scheduleID).Scan(&count)
	return count, err
}
