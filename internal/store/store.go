package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/justyn-clark/timekeeper/internal/domain"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) Migrate(ctx context.Context) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		b, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, string(b)); err != nil {
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *Store) CreateSchedule(ctx context.Context, schedule domain.Schedule) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO schedules (
			id, name, enabled, schedule_kind, schedule_spec_json, timezone,
			target_kind, target_spec_json, overlap_policy, misfire_policy,
			timeout_seconds, max_concurrency, retry_max_attempts, retry_strategy,
			retry_initial_delay_seconds, retry_max_delay_seconds, start_at, end_at,
			paused_at, next_run_at, last_run_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		schedule.ID,
		schedule.Name,
		boolToInt(schedule.Enabled),
		schedule.Schedule.Kind,
		mustJSONString(schedule.Schedule),
		schedule.Timezone,
		schedule.Target.Kind,
		mustJSONString(schedule.Target),
		schedule.Policy.Overlap,
		schedule.Policy.Misfire,
		schedule.Policy.TimeoutSeconds,
		schedule.Policy.MaxConcurrency,
		schedule.Retry.MaxAttempts,
		schedule.Retry.Strategy,
		schedule.Retry.InitialDelaySeconds,
		schedule.Retry.MaxDelaySeconds,
		timePtrString(schedule.StartAt),
		timePtrString(schedule.EndAt),
		timePtrString(schedule.PausedAt),
		timePtrString(schedule.NextRunAt),
		timePtrString(schedule.LastRunAt),
		timeString(schedule.CreatedAt),
		timeString(schedule.UpdatedAt),
	)
	return err
}

func (s *Store) UpdateSchedule(ctx context.Context, schedule domain.Schedule) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE schedules
		SET name = ?, enabled = ?, schedule_kind = ?, schedule_spec_json = ?, timezone = ?,
		    target_kind = ?, target_spec_json = ?, overlap_policy = ?, misfire_policy = ?,
		    timeout_seconds = ?, max_concurrency = ?, retry_max_attempts = ?, retry_strategy = ?,
		    retry_initial_delay_seconds = ?, retry_max_delay_seconds = ?, start_at = ?, end_at = ?,
		    paused_at = ?, next_run_at = ?, last_run_at = ?, updated_at = ?
		WHERE id = ?
	`,
		schedule.Name,
		boolToInt(schedule.Enabled),
		schedule.Schedule.Kind,
		mustJSONString(schedule.Schedule),
		schedule.Timezone,
		schedule.Target.Kind,
		mustJSONString(schedule.Target),
		schedule.Policy.Overlap,
		schedule.Policy.Misfire,
		schedule.Policy.TimeoutSeconds,
		schedule.Policy.MaxConcurrency,
		schedule.Retry.MaxAttempts,
		schedule.Retry.Strategy,
		schedule.Retry.InitialDelaySeconds,
		schedule.Retry.MaxDelaySeconds,
		timePtrString(schedule.StartAt),
		timePtrString(schedule.EndAt),
		timePtrString(schedule.PausedAt),
		timePtrString(schedule.NextRunAt),
		timePtrString(schedule.LastRunAt),
		timeString(schedule.UpdatedAt),
		schedule.ID,
	)
	return err
}

func (s *Store) GetSchedule(ctx context.Context, id string) (domain.Schedule, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, enabled, schedule_kind, schedule_spec_json, timezone, target_kind, target_spec_json,
		       overlap_policy, misfire_policy, timeout_seconds, max_concurrency, retry_max_attempts,
		       retry_strategy, retry_initial_delay_seconds, retry_max_delay_seconds, start_at, end_at,
		       paused_at, next_run_at, last_run_at, created_at, updated_at
		FROM schedules WHERE id = ?
	`, id)
	return scanSchedule(row)
}

func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE id = ?`, id)
	return err
}

func (s *Store) ListSchedules(ctx context.Context, enabled *bool, limit int, cursor string) ([]domain.ScheduleSummary, *string, error) {
	if limit <= 0 {
		limit = 50
	}
	args := []any{}
	clauses := []string{}
	if enabled != nil {
		clauses = append(clauses, "enabled = ?")
		args = append(args, boolToInt(*enabled))
	}
	if cursor != "" {
		createdAt, id, err := domain.DecodeCursor(cursor)
		if err != nil {
			return nil, nil, err
		}
		clauses = append(clauses, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, timeString(createdAt), timeString(createdAt), id)
	}
	query := `
		SELECT id, name, enabled, schedule_spec_json, timezone, target_kind, paused_at, next_run_at, last_run_at, created_at, updated_at
		FROM schedules
	`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var items []domain.ScheduleSummary
	for rows.Next() {
		var (
			item         domain.ScheduleSummary
			enabledInt   int
			specJSON     string
			pausedAt     sql.NullString
			nextRunAt    sql.NullString
			lastRunAt    sql.NullString
			createdAtRaw string
			updatedAtRaw string
		)
		if err := rows.Scan(&item.ID, &item.Name, &enabledInt, &specJSON, &item.Timezone, &item.TargetKind, &pausedAt, &nextRunAt, &lastRunAt, &createdAtRaw, &updatedAtRaw); err != nil {
			return nil, nil, err
		}
		item.Enabled = enabledInt == 1
		if err := json.Unmarshal([]byte(specJSON), &item.Schedule); err != nil {
			return nil, nil, err
		}
		item.PausedAt = parseNullTime(pausedAt)
		item.NextRunAt = parseNullTime(nextRunAt)
		item.LastRunAt = parseNullTime(lastRunAt)
		item.CreatedAt = mustParseTime(createdAtRaw)
		item.UpdatedAt = mustParseTime(updatedAtRaw)
		items = append(items, item)
	}
	if len(items) <= limit {
		return items, nil, nil
	}
	last := items[limit]
	items = items[:limit]
	nextCursor := domain.EncodeCursor(last.CreatedAt, last.ID)
	return items, &nextCursor, nil
}

func (s *Store) ListAllSchedules(ctx context.Context) ([]domain.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, enabled, schedule_kind, schedule_spec_json, timezone, target_kind, target_spec_json,
		       overlap_policy, misfire_policy, timeout_seconds, max_concurrency, retry_max_attempts,
		       retry_strategy, retry_initial_delay_seconds, retry_max_delay_seconds, start_at, end_at,
		       paused_at, next_run_at, last_run_at, created_at, updated_at
		FROM schedules
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var schedules []domain.Schedule
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, nil
}

func (s *Store) InsertRun(ctx context.Context, run domain.Run) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO schedule_runs (
			id, schedule_id, occurrence_key, nominal_time, due_time, status, attempt, claimed_by_worker_id,
			claim_expires_at, started_at, finished_at, http_status_code, exit_code, result_json, error_text,
			retry_available_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.ID,
		run.ScheduleID,
		run.OccurrenceKey,
		timeString(run.NominalTime),
		timeString(run.DueTime),
		run.Status,
		run.Attempt,
		stringPtr(run.ClaimedByWorkerID),
		timePtrString(run.ClaimExpiresAt),
		timePtrString(run.StartedAt),
		timePtrString(run.FinishedAt),
		intPtr(run.HTTPStatusCode),
		intPtr(run.ExitCode),
		rawJSON(run.ResultJSON),
		stringPtr(run.ErrorText),
		timePtrString(run.RetryAvailableAt),
		timeString(run.CreatedAt),
		timeString(run.UpdatedAt),
	)
	if isUniqueErr(err) {
		return ErrAlreadyExists
	}
	return err
}

func (s *Store) GetRun(ctx context.Context, id string) (domain.Run, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, schedule_id, occurrence_key, nominal_time, due_time, status, attempt, claimed_by_worker_id,
		       claim_expires_at, started_at, finished_at, http_status_code, exit_code, result_json, error_text,
		       retry_available_at, created_at, updated_at
		FROM schedule_runs WHERE id = ?
	`, id)
	run, err := scanRun(row)
	if err != nil {
		return domain.Run{}, err
	}
	receipts, err := s.ListReceipts(ctx, id)
	if err != nil {
		return domain.Run{}, err
	}
	run.Receipts = receipts
	return run, nil
}

func (s *Store) ListRuns(ctx context.Context, scheduleID *string, status *domain.RunStatus, limit int, cursor string) ([]domain.RunSummary, *string, error) {
	if limit <= 0 {
		limit = 50
	}
	args := []any{}
	clauses := []string{}
	if scheduleID != nil {
		clauses = append(clauses, "schedule_id = ?")
		args = append(args, *scheduleID)
	}
	if status != nil {
		clauses = append(clauses, "status = ?")
		args = append(args, *status)
	}
	if cursor != "" {
		createdAt, id, err := domain.DecodeCursor(cursor)
		if err != nil {
			return nil, nil, err
		}
		clauses = append(clauses, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, timeString(createdAt), timeString(createdAt), id)
	}
	query := `
		SELECT id, schedule_id, occurrence_key, status, attempt, started_at, finished_at, created_at, updated_at
		FROM schedule_runs
	`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var items []domain.RunSummary
	for rows.Next() {
		var (
			item         domain.RunSummary
			startedAt    sql.NullString
			finishedAt   sql.NullString
			createdAtRaw string
			updatedAtRaw string
		)
		if err := rows.Scan(&item.ID, &item.ScheduleID, &item.OccurrenceKey, &item.Status, &item.Attempt, &startedAt, &finishedAt, &createdAtRaw, &updatedAtRaw); err != nil {
			return nil, nil, err
		}
		item.StartedAt = parseNullTime(startedAt)
		item.FinishedAt = parseNullTime(finishedAt)
		item.CreatedAt = mustParseTime(createdAtRaw)
		item.UpdatedAt = mustParseTime(updatedAtRaw)
		items = append(items, item)
	}
	if len(items) <= limit {
		return items, nil, nil
	}
	last := items[limit]
	items = items[:limit]
	nextCursor := domain.EncodeCursor(last.CreatedAt, last.ID)
	return items, &nextCursor, nil
}

func (s *Store) ListReceipts(ctx context.Context, runID string) ([]domain.Receipt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, receipt_kind, content_type, body, created_at
		FROM execution_receipts
		WHERE run_id = ?
		ORDER BY created_at ASC, id ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.Receipt
	for rows.Next() {
		var (
			item         domain.Receipt
			createdAtRaw string
		)
		item.RunID = runID
		if err := rows.Scan(&item.ID, &item.ReceiptKind, &item.ContentType, &item.Body, &createdAtRaw); err != nil {
			return nil, err
		}
		item.CreatedAt = mustParseTime(createdAtRaw)
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) InsertReceipt(ctx context.Context, receipt domain.Receipt) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO execution_receipts (id, run_id, receipt_kind, content_type, body, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, receipt.ID, receipt.RunID, receipt.ReceiptKind, receipt.ContentType, receipt.Body, timeString(receipt.CreatedAt))
	return err
}

func (s *Store) InsertDeadLetter(ctx context.Context, dead domain.DeadLetter) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO dead_letters (id, run_id, schedule_id, occurrence_key, reason, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, dead.ID, dead.RunID, dead.ScheduleID, dead.OccurrenceKey, dead.Reason, rawJSON(dead.PayloadJSON), timeString(dead.CreatedAt))
	return err
}

func (s *Store) ListCandidateRuns(ctx context.Context, now time.Time, limit int) ([]domain.Run, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, schedule_id, occurrence_key, nominal_time, due_time, status, attempt, claimed_by_worker_id,
		       claim_expires_at, started_at, finished_at, http_status_code, exit_code, result_json, error_text,
		       retry_available_at, created_at, updated_at
		FROM schedule_runs
		WHERE (status = 'pending' AND due_time <= ?)
		   OR (status = 'retry_scheduled' AND retry_available_at IS NOT NULL AND retry_available_at <= ?)
		ORDER BY due_time ASC, created_at ASC
		LIMIT ?
	`, timeString(now), timeString(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []domain.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *Store) ClaimRun(ctx context.Context, runID, workerID string, now time.Time, ttl time.Duration) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	expires := now.Add(ttl)
	res, err := tx.ExecContext(ctx, `
		UPDATE schedule_runs
		SET status = 'claimed', claimed_by_worker_id = ?, claim_expires_at = ?, updated_at = ?
		WHERE id = ? AND status IN ('pending', 'retry_scheduled')
	`, workerID, timeString(expires), timeString(now), runID)
	if err != nil {
		return false, err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if rowsAffected == 0 {
		return false, nil
	}
	_, err = tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO worker_leases (id, worker_id, run_id, lease_key, acquired_at, expires_at, heartbeat_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, domain.NewID("lease"), workerID, runID, runID, timeString(now), timeString(expires), timeString(now))
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) RenewLease(ctx context.Context, runID, workerID string, now time.Time, ttl time.Duration) error {
	expires := now.Add(ttl)
	_, err := s.db.ExecContext(ctx, `
		UPDATE worker_leases
		SET expires_at = ?, heartbeat_at = ?
		WHERE run_id = ? AND worker_id = ?
	`, timeString(expires), timeString(now), runID, workerID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE schedule_runs
		SET claim_expires_at = ?, updated_at = ?
		WHERE id = ? AND claimed_by_worker_id = ?
	`, timeString(expires), timeString(now), runID, workerID)
	return err
}

func (s *Store) RecoverExpiredClaims(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT run_id FROM worker_leases WHERE expires_at <= ?`, timeString(now))
	if err != nil {
		return err
	}
	var runIDs []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			rows.Close()
			return err
		}
		runIDs = append(runIDs, runID)
	}
	rows.Close()
	for _, runID := range runIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE schedule_runs
			SET status = 'pending', claimed_by_worker_id = NULL, claim_expires_at = NULL, updated_at = ?
			WHERE id = ? AND status IN ('claimed', 'running')
		`, timeString(now), runID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM worker_leases WHERE run_id = ?`, runID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) MarkRunRunning(ctx context.Context, runID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE schedule_runs
		SET status = 'running', started_at = COALESCE(started_at, ?), updated_at = ?
		WHERE id = ?
	`, timeString(now), timeString(now), runID)
	return err
}

func (s *Store) FinishRun(ctx context.Context, run domain.Run) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE schedule_runs
		SET status = ?, claimed_by_worker_id = ?, claim_expires_at = ?, started_at = ?, finished_at = ?,
		    http_status_code = ?, exit_code = ?, result_json = ?, error_text = ?, retry_available_at = ?, updated_at = ?
		WHERE id = ?
	`,
		run.Status,
		stringPtr(run.ClaimedByWorkerID),
		timePtrString(run.ClaimExpiresAt),
		timePtrString(run.StartedAt),
		timePtrString(run.FinishedAt),
		intPtr(run.HTTPStatusCode),
		intPtr(run.ExitCode),
		rawJSON(run.ResultJSON),
		stringPtr(run.ErrorText),
		timePtrString(run.RetryAvailableAt),
		timeString(run.UpdatedAt),
		run.ID,
	)
	if err != nil {
		return err
	}
	if run.FinishedAt != nil {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM worker_leases WHERE run_id = ?`, run.ID)
	}
	return nil
}

func (s *Store) UpdateScheduleRuntime(ctx context.Context, scheduleID string, nextRunAt, lastRunAt, pausedAt *time.Time, enabled bool, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE schedules
		SET enabled = ?, paused_at = ?, next_run_at = ?, last_run_at = ?, updated_at = ?
		WHERE id = ?
	`, boolToInt(enabled), timePtrString(pausedAt), timePtrString(nextRunAt), timePtrString(lastRunAt), timeString(now), scheduleID)
	return err
}

func (s *Store) ActiveRunCount(ctx context.Context, scheduleID string) (int, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM schedule_runs
		WHERE schedule_id = ? AND status IN ('claimed', 'running')
	`, scheduleID)
	var n int
	return n, row.Scan(&n)
}

func (s *Store) ListActiveRuns(ctx context.Context, scheduleID string) ([]domain.Run, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, schedule_id, occurrence_key, nominal_time, due_time, status, attempt, claimed_by_worker_id,
		       claim_expires_at, started_at, finished_at, http_status_code, exit_code, result_json, error_text,
		       retry_available_at, created_at, updated_at
		FROM schedule_runs
		WHERE schedule_id = ? AND status IN ('claimed', 'running')
		ORDER BY created_at ASC
	`, scheduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []domain.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *Store) ListPendingRunsBySchedule(ctx context.Context, scheduleID string) ([]domain.Run, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, schedule_id, occurrence_key, nominal_time, due_time, status, attempt, claimed_by_worker_id,
		       claim_expires_at, started_at, finished_at, http_status_code, exit_code, result_json, error_text,
		       retry_available_at, created_at, updated_at
		FROM schedule_runs
		WHERE schedule_id = ? AND status IN ('pending', 'retry_scheduled')
		ORDER BY due_time ASC, created_at ASC
	`, scheduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []domain.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *Store) CountStatus(ctx context.Context, table, column, value string) (int, error) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", table, column)
	row := s.db.QueryRowContext(ctx, query, value)
	var n int
	return n, row.Scan(&n)
}

func (s *Store) CountTable(ctx context.Context, table string) (int, error) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	row := s.db.QueryRowContext(ctx, query)
	var n int
	return n, row.Scan(&n)
}

func (s *Store) WorkerLeaseCount(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM worker_leases`)
	var n int
	return n, row.Scan(&n)
}

func (s *Store) ScheduleEnabledCount(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schedules WHERE enabled = 1`)
	var n int
	return n, row.Scan(&n)
}

var ErrAlreadyExists = errors.New("already exists")

func scanSchedule(scanner interface{ Scan(dest ...any) error }) (domain.Schedule, error) {
	var (
		s          domain.Schedule
		enabledInt int
		specJSON   string
		targetJSON string
		startAt    sql.NullString
		endAt      sql.NullString
		pausedAt   sql.NullString
		nextRunAt  sql.NullString
		lastRunAt  sql.NullString
		createdAt  string
		updatedAt  string
	)
	if err := scanner.Scan(
		&s.ID, &s.Name, &enabledInt, &s.Schedule.Kind, &specJSON, &s.Timezone,
		&s.Target.Kind, &targetJSON, &s.Policy.Overlap, &s.Policy.Misfire,
		&s.Policy.TimeoutSeconds, &s.Policy.MaxConcurrency, &s.Retry.MaxAttempts,
		&s.Retry.Strategy, &s.Retry.InitialDelaySeconds, &s.Retry.MaxDelaySeconds,
		&startAt, &endAt, &pausedAt, &nextRunAt, &lastRunAt, &createdAt, &updatedAt,
	); err != nil {
		return domain.Schedule{}, err
	}
	if err := json.Unmarshal([]byte(specJSON), &s.Schedule); err != nil {
		return domain.Schedule{}, err
	}
	if err := json.Unmarshal([]byte(targetJSON), &s.Target); err != nil {
		return domain.Schedule{}, err
	}
	s.Enabled = enabledInt == 1
	s.StartAt = parseNullTime(startAt)
	s.EndAt = parseNullTime(endAt)
	s.PausedAt = parseNullTime(pausedAt)
	s.NextRunAt = parseNullTime(nextRunAt)
	s.LastRunAt = parseNullTime(lastRunAt)
	s.CreatedAt = mustParseTime(createdAt)
	s.UpdatedAt = mustParseTime(updatedAt)
	return s, nil
}

func scanRun(scanner interface{ Scan(dest ...any) error }) (domain.Run, error) {
	var (
		run              domain.Run
		claimedByWorker  sql.NullString
		claimExpiresAt   sql.NullString
		startedAt        sql.NullString
		finishedAt       sql.NullString
		httpStatusCode   sql.NullInt64
		exitCode         sql.NullInt64
		resultJSON       sql.NullString
		errorText        sql.NullString
		retryAvailableAt sql.NullString
		nominalTimeRaw   string
		dueTimeRaw       string
		createdAtRaw     string
		updatedAtRaw     string
	)
	if err := scanner.Scan(
		&run.ID, &run.ScheduleID, &run.OccurrenceKey, &nominalTimeRaw, &dueTimeRaw,
		&run.Status, &run.Attempt, &claimedByWorker, &claimExpiresAt, &startedAt, &finishedAt,
		&httpStatusCode, &exitCode, &resultJSON, &errorText, &retryAvailableAt, &createdAtRaw, &updatedAtRaw,
	); err != nil {
		return domain.Run{}, err
	}
	run.NominalTime = mustParseTime(nominalTimeRaw)
	run.DueTime = mustParseTime(dueTimeRaw)
	run.ClaimedByWorkerID = parseNullString(claimedByWorker)
	run.ClaimExpiresAt = parseNullTime(claimExpiresAt)
	run.StartedAt = parseNullTime(startedAt)
	run.FinishedAt = parseNullTime(finishedAt)
	if httpStatusCode.Valid {
		v := int(httpStatusCode.Int64)
		run.HTTPStatusCode = &v
	}
	if exitCode.Valid {
		v := int(exitCode.Int64)
		run.ExitCode = &v
	}
	if resultJSON.Valid {
		run.ResultJSON = json.RawMessage(resultJSON.String)
	}
	run.ErrorText = parseNullString(errorText)
	run.RetryAvailableAt = parseNullTime(retryAvailableAt)
	run.CreatedAt = mustParseTime(createdAtRaw)
	run.UpdatedAt = mustParseTime(updatedAtRaw)
	return run, nil
}

func timeString(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func timePtrString(t *time.Time) any {
	if t == nil {
		return nil
	}
	return timeString(*t)
}

func mustJSONString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func parseNullTime(v sql.NullString) *time.Time {
	if !v.Valid || strings.TrimSpace(v.String) == "" {
		return nil
	}
	t := mustParseTime(v.String)
	return &t
}

func parseNullString(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func mustParseTime(raw string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err == nil {
		return t.UTC()
	}
	t, _ = time.Parse(time.RFC3339, raw)
	return t.UTC()
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func intPtr(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func stringPtr(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

func rawJSON(v json.RawMessage) any {
	if len(v) == 0 {
		return nil
	}
	return string(v)
}

func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique")
}
