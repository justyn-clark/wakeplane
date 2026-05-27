package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestInsertReceiptTruncatesBody(t *testing.T) {
	st := openTestStore(t)
	st.SetReceiptMaxBytes(32)
	schedule := insertTestSchedule(t, st)
	run := insertTestRun(t, st, schedule.ID, domain.RunSucceeded, time.Now().UTC())

	err := st.InsertReceipt(context.Background(), domain.Receipt{
		ID:          domain.NewID("rcpt"),
		RunID:       run.ID,
		ReceiptKind: "stdout",
		ContentType: "text/plain",
		Body:        strings.Repeat("x", 100),
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("InsertReceipt returned error: %v", err)
	}
	items, err := st.ListReceipts(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListReceipts returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 receipt, got %d", len(items))
	}
	if len([]byte(items[0].Body)) > 32 {
		t.Fatalf("expected receipt body to be bounded, got %d bytes", len([]byte(items[0].Body)))
	}
	if !strings.Contains(items[0].Body, "truncated") {
		t.Fatalf("expected truncation marker, got %q", items[0].Body)
	}
}

func TestPruneTerminalRunsBeforeCascadesReceipts(t *testing.T) {
	st := openTestStore(t)
	schedule := insertTestSchedule(t, st)
	oldFinishedAt := time.Now().UTC().Add(-48 * time.Hour)
	oldRun := insertTestRun(t, st, schedule.ID, domain.RunSucceeded, oldFinishedAt)
	activeRun := insertTestRun(t, st, schedule.ID, domain.RunRunning, oldFinishedAt)

	for _, runID := range []string{oldRun.ID, activeRun.ID} {
		if err := st.InsertReceipt(context.Background(), domain.Receipt{
			ID:          domain.NewID("rcpt"),
			RunID:       runID,
			ReceiptKind: "stdout",
			ContentType: "text/plain",
			Body:        "receipt",
			CreatedAt:   time.Now().UTC(),
		}); err != nil {
			t.Fatalf("InsertReceipt returned error: %v", err)
		}
	}

	deleted, err := st.PruneTerminalRunsBefore(context.Background(), time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("PruneTerminalRunsBefore returned error: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 pruned run, got %d", deleted)
	}
	if _, err := st.GetRun(context.Background(), oldRun.ID); err != ErrNotFound {
		t.Fatalf("expected old terminal run to be pruned, got %v", err)
	}
	if _, err := st.GetRun(context.Background(), activeRun.ID); err != nil {
		t.Fatalf("expected active run to remain, got %v", err)
	}
}

func TestSQLiteProductionStoreInvariants(t *testing.T) {
	runProductionStoreInvariants(t, openTestStore(t))
}

func TestPostgresRebindsPlaceholders(t *testing.T) {
	st := &Store{dialect: "postgres"}

	got := st.rebind("SELECT * FROM schedule_runs WHERE schedule_id = ? AND status = ? LIMIT ?")
	want := "SELECT * FROM schedule_runs WHERE schedule_id = $1 AND status = $2 LIMIT $3"
	if got != want {
		t.Fatalf("unexpected postgres query binding:\nwant %q\n got %q", want, got)
	}
}

func TestSQLiteRebindLeavesPlaceholders(t *testing.T) {
	st := &Store{dialect: "sqlite"}

	got := st.rebind("SELECT * FROM schedule_runs WHERE schedule_id = ?")
	if got != "SELECT * FROM schedule_runs WHERE schedule_id = ?" {
		t.Fatalf("unexpected sqlite query binding: %q", got)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/wakeplane.db")
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	return st
}

func insertTestSchedule(t *testing.T, st *Store) domain.Schedule {
	t.Helper()
	now := time.Now().UTC()
	next := now.Add(time.Minute)
	schedule := domain.Schedule{
		ID:        domain.NewID("sch"),
		Name:      domain.NewID("schedule"),
		Enabled:   true,
		Timezone:  "UTC",
		Schedule:  domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:    domain.TargetSpec{Kind: domain.TargetKindShell, Command: "/bin/echo"},
		Policy:    domain.DefaultPolicy(),
		Retry:     domain.DefaultRetryPolicy(),
		NextRunAt: &next,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	return schedule
}

func insertTestRun(t *testing.T, st *Store, scheduleID string, status domain.RunStatus, finishedAt time.Time) domain.Run {
	t.Helper()
	now := time.Now().UTC()
	run := domain.Run{
		ID:            domain.NewID("run"),
		ScheduleID:    scheduleID,
		OccurrenceKey: domain.NewID("occ"),
		NominalTime:   now,
		DueTime:       now,
		Status:        status,
		Attempt:       1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if status != domain.RunRunning && status != domain.RunPending && status != domain.RunClaimed && status != domain.RunRetryScheduled {
		run.FinishedAt = &finishedAt
	}
	if err := st.InsertRun(context.Background(), run); err != nil {
		t.Fatalf("InsertRun returned error: %v", err)
	}
	return run
}

func runProductionStoreInvariants(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	schedule := insertTestSchedule(t, st)
	schedule.Policy.MaxConcurrency = 1
	schedule.Retry.MaxAttempts = 2
	if err := st.UpdateSchedule(ctx, schedule); err != nil {
		t.Fatalf("UpdateSchedule returned error: %v", err)
	}

	expiredClaimed := domain.Run{
		ID:                domain.NewID("run"),
		ScheduleID:        schedule.ID,
		OccurrenceKey:     domain.NewID("occ"),
		NominalTime:       now,
		DueTime:           now,
		Status:            domain.RunClaimed,
		Attempt:           1,
		ClaimedByWorkerID: ptrString("wrk_expired"),
		ClaimExpiresAt:    ptrTime(now.Add(-time.Minute)),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := st.InsertRun(ctx, expiredClaimed); err != nil {
		t.Fatalf("InsertRun expired claim returned error: %v", err)
	}
	insertTestLease(t, st, "wrk_expired", expiredClaimed.ID, now.Add(-2*time.Minute), now.Add(-time.Minute))
	if err := st.RecoverExpiredClaims(ctx, now); err != nil {
		t.Fatalf("RecoverExpiredClaims returned error: %v", err)
	}
	recovered, err := st.GetRun(ctx, expiredClaimed.ID)
	if err != nil {
		t.Fatalf("GetRun recovered claim returned error: %v", err)
	}
	if recovered.Status != domain.RunPending || recovered.ClaimedByWorkerID != nil || recovered.ClaimExpiresAt != nil {
		t.Fatalf("expected expired claim to be reclaimable pending run, got status=%s worker=%v expires=%v", recovered.Status, recovered.ClaimedByWorkerID, recovered.ClaimExpiresAt)
	}
	leases, err := st.WorkerLeaseCount(ctx)
	if err != nil {
		t.Fatalf("WorkerLeaseCount returned error: %v", err)
	}
	if leases != 0 {
		t.Fatalf("expected expired lease to be removed, got %d", leases)
	}

	terminal := domain.Run{
		ID:            domain.NewID("run"),
		ScheduleID:    schedule.ID,
		OccurrenceKey: domain.NewID("occ"),
		NominalTime:   now,
		DueTime:       now,
		Status:        domain.RunSucceeded,
		Attempt:       1,
		FinishedAt:    ptrTime(now),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := st.InsertRun(ctx, terminal); err != nil {
		t.Fatalf("InsertRun terminal returned error: %v", err)
	}
	claimed, err := st.ClaimRun(ctx, schedule, terminal.ID, "wrk_terminal", now, time.Minute)
	if err != nil {
		t.Fatalf("ClaimRun terminal returned error: %v", err)
	}
	if claimed {
		t.Fatalf("terminal run was claimable")
	}

	occurrenceKey := domain.NewID("occ")
	failed := domain.Run{
		ID:            domain.NewID("run"),
		ScheduleID:    schedule.ID,
		OccurrenceKey: occurrenceKey,
		NominalTime:   now,
		DueTime:       now,
		Status:        domain.RunFailed,
		Attempt:       1,
		FinishedAt:    ptrTime(now),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := st.InsertRun(ctx, failed); err != nil {
		t.Fatalf("InsertRun failed returned error: %v", err)
	}
	retryAt := now.Add(time.Minute)
	retry := domain.Run{
		ID:               domain.NewID("run"),
		ScheduleID:       schedule.ID,
		OccurrenceKey:    occurrenceKey,
		NominalTime:      now,
		DueTime:          retryAt,
		Status:           domain.RunRetryScheduled,
		Attempt:          2,
		RetryAvailableAt: &retryAt,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := st.InsertRun(ctx, retry); err != nil {
		t.Fatalf("InsertRun retry returned error: %v", err)
	}
	duplicateRetry := retry
	duplicateRetry.ID = domain.NewID("run")
	if err := st.InsertRun(ctx, duplicateRetry); err != ErrAlreadyExists {
		t.Fatalf("expected duplicate retry attempt to be rejected, got %v", err)
	}
	candidates, err := st.ListCandidateRuns(ctx, retryAt, 10)
	if err != nil {
		t.Fatalf("ListCandidateRuns returned error: %v", err)
	}
	foundRetry := false
	for _, candidate := range candidates {
		if candidate.ID == failed.ID {
			t.Fatalf("terminal failed run was returned as a candidate")
		}
		if candidate.ID == retry.ID {
			foundRetry = true
		}
	}
	if !foundRetry {
		t.Fatalf("retry-scheduled run was not returned as due candidate")
	}

	dead := domain.Run{
		ID:            domain.NewID("run"),
		ScheduleID:    schedule.ID,
		OccurrenceKey: domain.NewID("occ"),
		NominalTime:   now,
		DueTime:       now,
		Status:        domain.RunDeadLettered,
		Attempt:       2,
		FinishedAt:    ptrTime(now),
		ErrorText:     ptrString("final failure"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := st.InsertRun(ctx, dead); err != nil {
		t.Fatalf("InsertRun dead-lettered returned error: %v", err)
	}
	if err := st.InsertDeadLetter(ctx, domain.DeadLetter{
		ID:            domain.NewID("dlq"),
		RunID:         dead.ID,
		ScheduleID:    schedule.ID,
		OccurrenceKey: dead.OccurrenceKey,
		Reason:        "final failure",
		CreatedAt:     now,
	}); err != nil {
		t.Fatalf("InsertDeadLetter returned error: %v", err)
	}
	deadLetters, err := st.CountTable(ctx, "dead_letters")
	if err != nil {
		t.Fatalf("CountTable dead_letters returned error: %v", err)
	}
	if deadLetters != 1 {
		t.Fatalf("expected 1 dead letter, got %d", deadLetters)
	}
	if err := st.InsertReceipt(ctx, domain.Receipt{
		ID:          domain.NewID("rcpt"),
		RunID:       dead.ID,
		ReceiptKind: "summary",
		ContentType: "text/plain",
		Body:        "final failure",
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("InsertReceipt returned error: %v", err)
	}
	receipts, err := st.ListReceipts(ctx, dead.ID)
	if err != nil {
		t.Fatalf("ListReceipts returned error: %v", err)
	}
	if len(receipts) != 1 {
		t.Fatalf("expected 1 receipt, got %d", len(receipts))
	}
	if err := st.InsertRequestAudit(ctx, domain.RequestAudit{
		ID:         domain.NewID("aud"),
		Method:     "GET",
		Path:       "/v1/status",
		StatusCode: 200,
		CreatedAt:  now,
	}); err != nil {
		t.Fatalf("InsertRequestAudit returned error: %v", err)
	}
	auditCount, err := st.RequestAuditCount(ctx)
	if err != nil {
		t.Fatalf("RequestAuditCount returned error: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("expected 1 request audit row, got %d", auditCount)
	}
}

func insertTestLease(t *testing.T, st *Store, workerID, runID string, acquiredAt, expiresAt time.Time) {
	t.Helper()
	if _, err := st.exec(context.Background(), `
		INSERT INTO worker_leases (id, worker_id, run_id, lease_key, acquired_at, expires_at, heartbeat_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, domain.NewID("lease"), workerID, runID, runID, timeString(acquiredAt), timeString(expiresAt), timeString(acquiredAt)); err != nil {
		t.Fatalf("insert worker lease returned error: %v", err)
	}
}

func ptrString(v string) *string {
	return &v
}

func ptrTime(v time.Time) *time.Time {
	return &v
}
