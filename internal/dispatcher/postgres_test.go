package dispatcher

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/executors"
	"github.com/justyn-clark/wakeplane/internal/logging"
	"github.com/justyn-clark/wakeplane/internal/store"
)

func TestPostgresRecoverExpiredClaimedRunReturnsToPending(t *testing.T) {
	st := openPostgresDispatcherStore(t)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	schedule := dispatcherSchedule(now)
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	run := insertDispatcherRun(t, st, schedule.ID, domain.RunPending, 1, now)
	workerID := "wrk_pg_claimed"
	claimedAt := now.Add(-2 * time.Minute)
	claimed, err := st.ClaimRun(context.Background(), schedule, run.ID, workerID, claimedAt, time.Second)
	if err != nil {
		t.Fatalf("ClaimRun returned error: %v", err)
	}
	if !claimed {
		t.Fatalf("expected expired setup claim to succeed")
	}

	d := New(st, executors.NewRegistry(), logging.New(), "wrk_pg_recovery", 30*time.Second)
	d.now = func() time.Time { return now }
	if err := d.recoverExpiredLeases(context.Background(), now); err != nil {
		t.Fatalf("recoverExpiredLeases returned error: %v", err)
	}
	got, err := st.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if got.Status != domain.RunPending {
		t.Fatalf("expected recovered claimed run to be pending, got %s", got.Status)
	}
	if got.ClaimedByWorkerID != nil || got.ClaimExpiresAt != nil {
		t.Fatalf("expected claim fields cleared, worker=%v expires=%v", got.ClaimedByWorkerID, got.ClaimExpiresAt)
	}
	leases, err := st.WorkerLeaseCount(context.Background())
	if err != nil {
		t.Fatalf("WorkerLeaseCount returned error: %v", err)
	}
	if leases != 0 {
		t.Fatalf("expected expired lease removed, got %d", leases)
	}
}

func TestPostgresRecoverExpiredRunningRunSchedulesRetry(t *testing.T) {
	st := openPostgresDispatcherStore(t)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	schedule := dispatcherSchedule(now)
	schedule.Retry.MaxAttempts = 2
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	run := insertDispatcherRun(t, st, schedule.ID, domain.RunPending, 1, now)
	workerID := "wrk_pg_running"
	claimedAt := now.Add(-2 * time.Minute)
	claimed, err := st.ClaimRun(context.Background(), schedule, run.ID, workerID, claimedAt, time.Second)
	if err != nil {
		t.Fatalf("ClaimRun returned error: %v", err)
	}
	if !claimed {
		t.Fatalf("expected expired setup claim to succeed")
	}
	if err := st.MarkRunRunning(context.Background(), run.ID, claimedAt); err != nil {
		t.Fatalf("MarkRunRunning returned error: %v", err)
	}

	d := New(st, executors.NewRegistry(), logging.New(), workerID, 30*time.Second)
	d.now = func() time.Time { return now }
	if err := d.recoverExpiredLeases(context.Background(), now); err != nil {
		t.Fatalf("recoverExpiredLeases returned error: %v", err)
	}
	got, err := st.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if got.Status != domain.RunFailed {
		t.Fatalf("expected expired running run to become failed, got %s", got.Status)
	}
	scheduleID := schedule.ID
	retryStatus := domain.RunRetryScheduled
	retries, _, err := st.ListRuns(context.Background(), &scheduleID, &retryStatus, nil, 10, "")
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(retries) != 1 {
		t.Fatalf("expected exactly one retry-scheduled run, got %d", len(retries))
	}
	if retries[0].Attempt != 2 {
		t.Fatalf("expected retry attempt 2, got %d", retries[0].Attempt)
	}
}

func TestPostgresFinalFailureDeadLettersAndRetainsReceipt(t *testing.T) {
	st := openPostgresDispatcherStore(t)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	schedule := dispatcherSchedule(now)
	schedule.Retry.MaxAttempts = 1
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	run := insertDispatcherRun(t, st, schedule.ID, domain.RunPending, 1, now)
	workerID := "wrk_pg_dead_letter"
	claimed, err := st.ClaimRun(context.Background(), schedule, run.ID, workerID, now, time.Minute)
	if err != nil {
		t.Fatalf("ClaimRun returned error: %v", err)
	}
	if !claimed {
		t.Fatalf("expected setup claim to succeed")
	}
	if err := st.MarkRunRunning(context.Background(), run.ID, now); err != nil {
		t.Fatalf("MarkRunRunning returned error: %v", err)
	}
	claimedRun, err := st.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}

	d := New(st, executors.NewRegistry(), logging.New(), workerID, 30*time.Second)
	d.now = func() time.Time { return now }
	d.completeFailureWithResult(context.Background(), schedule, claimedRun, executors.Result{
		ErrorText: "permanent failure",
		Receipts: []executors.Receipt{{
			Kind:        "summary",
			ContentType: "text/plain",
			Body:        "permanent failure",
		}},
	})
	got, err := st.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if got.Status != domain.RunDeadLettered {
		t.Fatalf("expected dead-lettered run, got %s", got.Status)
	}
	deadLetters, err := st.CountTable(context.Background(), "dead_letters")
	if err != nil {
		t.Fatalf("CountTable dead_letters returned error: %v", err)
	}
	if deadLetters != 1 {
		t.Fatalf("expected one dead letter, got %d", deadLetters)
	}
	receipts, err := st.ListReceipts(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListReceipts returned error: %v", err)
	}
	if len(receipts) != 1 {
		t.Fatalf("expected one failure receipt, got %d", len(receipts))
	}
}

func openPostgresDispatcherStore(t *testing.T) *store.Store {
	t.Helper()
	databaseURL := os.Getenv("WAKEPLANE_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("set WAKEPLANE_POSTGRES_TEST_URL to run Postgres dispatcher tests")
	}
	resetPostgresDispatcherTestDB(t, databaseURL)
	st, err := store.OpenPostgres(databaseURL)
	if err != nil {
		t.Fatalf("OpenPostgres returned error: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	return st
}

func resetPostgresDispatcherTestDB(t *testing.T, databaseURL string) {
	t.Helper()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open postgres reset connection: %v", err)
	}
	defer db.Close()
	for _, table := range []string{
		"request_audit_logs",
		"execution_receipts",
		"dead_letters",
		"worker_leases",
		"schedule_runs",
		"schedules",
	} {
		if _, err := db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table+" CASCADE"); err != nil {
			t.Fatalf("drop table %s: %v", table, err)
		}
	}
}

func insertDispatcherRun(t *testing.T, st *store.Store, scheduleID string, status domain.RunStatus, attempt int, now time.Time) domain.Run {
	t.Helper()
	run := domain.Run{
		ID:            domain.NewID("run"),
		ScheduleID:    scheduleID,
		OccurrenceKey: domain.OccurrenceKey(scheduleID, now),
		NominalTime:   now,
		DueTime:       now,
		Status:        status,
		Attempt:       attempt,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := st.InsertRun(context.Background(), run); err != nil {
		t.Fatalf("InsertRun returned error: %v", err)
	}
	return run
}
