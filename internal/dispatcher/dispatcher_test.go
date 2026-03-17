package dispatcher

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/executors"
	"github.com/justyn-clark/wakeplane/internal/logging"
	"github.com/justyn-clark/wakeplane/internal/store"
)

func TestClaimRunOnlySucceedsOnceUnderContention(t *testing.T) {
	st := newDispatcherStore(t)
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	schedule := dispatcherSchedule(now)
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	run := domain.Run{
		ID:            domain.NewID("run"),
		ScheduleID:    schedule.ID,
		OccurrenceKey: domain.OccurrenceKey(schedule.ID, now),
		NominalTime:   now,
		DueTime:       now,
		Status:        domain.RunPending,
		Attempt:       1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := st.InsertRun(context.Background(), run); err != nil {
		t.Fatalf("InsertRun returned error: %v", err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claimed, err := st.ClaimRun(context.Background(), schedule, run.ID, domain.NewID("wrk"), now, 30*time.Second)
			if err != nil {
				t.Errorf("ClaimRun returned error: %v", err)
				return
			}
			if claimed {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("expected exactly one successful claim, got %d", successes)
	}
}

func TestRecoverExpiredRunningRunSchedulesRetry(t *testing.T) {
	st := newDispatcherStore(t)
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	schedule := dispatcherSchedule(now)
	schedule.Retry.MaxAttempts = 2
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	workerID := "wrk_test"
	expires := now.Add(-time.Second)
	run := domain.Run{
		ID:                domain.NewID("run"),
		ScheduleID:        schedule.ID,
		OccurrenceKey:     domain.OccurrenceKey(schedule.ID, now),
		NominalTime:       now,
		DueTime:           now,
		Status:            domain.RunRunning,
		Attempt:           1,
		ClaimedByWorkerID: &workerID,
		ClaimExpiresAt:    &expires,
		StartedAt:         ptrTime(now),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := st.InsertRun(context.Background(), run); err != nil {
		t.Fatalf("InsertRun returned error: %v", err)
	}
	if _, err := st.DB().ExecContext(context.Background(), `
		INSERT INTO worker_leases (id, worker_id, run_id, lease_key, acquired_at, expires_at, heartbeat_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, domain.NewID("lease"), workerID, run.ID, run.ID, now.Add(-2*time.Second).Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert worker lease returned error: %v", err)
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
		t.Fatalf("expected failed original run, got %s", got.Status)
	}
	scheduleID := schedule.ID
	items, _, err := st.ListRuns(context.Background(), &scheduleID, nil, 10, "")
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected original run plus retry attempt, got %d", len(items))
	}
}

func TestRecoverExpiredClaimedRunReturnsToPending(t *testing.T) {
	st := newDispatcherStore(t)
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	schedule := dispatcherSchedule(now)
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	workerID := "wrk_test"
	expires := now.Add(-time.Second)
	run := domain.Run{
		ID:                domain.NewID("run"),
		ScheduleID:        schedule.ID,
		OccurrenceKey:     domain.OccurrenceKey(schedule.ID, now),
		NominalTime:       now,
		DueTime:           now,
		Status:            domain.RunClaimed,
		Attempt:           1,
		ClaimedByWorkerID: &workerID,
		ClaimExpiresAt:    &expires,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := st.InsertRun(context.Background(), run); err != nil {
		t.Fatalf("InsertRun returned error: %v", err)
	}
	if _, err := st.DB().ExecContext(context.Background(), `
		INSERT INTO worker_leases (id, worker_id, run_id, lease_key, acquired_at, expires_at, heartbeat_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, domain.NewID("lease"), workerID, run.ID, run.ID, now.Add(-2*time.Second).Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert worker lease returned error: %v", err)
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
	if got.Status != domain.RunPending {
		t.Fatalf("expected pending recovered run, got %s", got.Status)
	}
}

func newDispatcherStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wakeplane.db"))
	if err != nil {
		t.Fatalf("store.Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	return st
}

func dispatcherSchedule(now time.Time) domain.Schedule {
	return domain.Schedule{
		ID:       domain.NewID("sch"),
		Name:     "dispatch-schedule",
		Enabled:  true,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60, AnchorAt: &now},
		Target:   domain.TargetSpec{Kind: domain.TargetKindShell, Command: "/bin/echo"},
		Policy: domain.Policy{
			Overlap:        domain.OverlapForbid,
			Misfire:        domain.MisfireCatchUp,
			TimeoutSeconds: 60,
			MaxConcurrency: 1,
		},
		Retry:     domain.DefaultRetryPolicy(),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestRecoverClaimedNotRunningAcrossReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")

	// Phase 1: create schedule and run, simulate crash after claim but before mark-running.
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open returned error: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	schedule := dispatcherSchedule(now)
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	workerID := "wrk_crashed"
	expires := now.Add(-time.Second) // already expired
	run := domain.Run{
		ID:                domain.NewID("run"),
		ScheduleID:        schedule.ID,
		OccurrenceKey:     domain.OccurrenceKey(schedule.ID, now),
		NominalTime:       now,
		DueTime:           now,
		Status:            domain.RunClaimed,
		Attempt:           1,
		ClaimedByWorkerID: &workerID,
		ClaimExpiresAt:    &expires,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := st.InsertRun(context.Background(), run); err != nil {
		t.Fatalf("InsertRun returned error: %v", err)
	}
	if _, err := st.DB().ExecContext(context.Background(), `
		INSERT INTO worker_leases (id, worker_id, run_id, lease_key, acquired_at, expires_at, heartbeat_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, domain.NewID("lease"), workerID, run.ID, run.ID,
		now.Add(-2*time.Second).Format(time.RFC3339Nano),
		expires.Format(time.RFC3339Nano),
		expires.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert lease returned error: %v", err)
	}
	_ = st.Close()

	// Phase 2: reopen store and run recovery as a new dispatcher would on startup.
	st2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen store returned error: %v", err)
	}
	defer st2.Close()
	if err := st2.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	d := New(st2, executors.NewRegistry(), logging.New(), "wrk_recovery", 30*time.Second)
	d.now = func() time.Time { return now }
	if err := d.recoverExpiredLeases(context.Background(), now); err != nil {
		t.Fatalf("recoverExpiredLeases returned error: %v", err)
	}
	got, err := st2.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if got.Status != domain.RunPending {
		t.Fatalf("expected claimed run recovered to pending, got %s", got.Status)
	}
	if got.ClaimedByWorkerID != nil {
		t.Fatalf("expected claimed_by_worker_id cleared, got %v", got.ClaimedByWorkerID)
	}
}

func TestRecoverRunningBeforeFinishAcrossReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open returned error: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	schedule := dispatcherSchedule(now)
	schedule.Retry.MaxAttempts = 3
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	workerID := "wrk_crashed"
	expires := now.Add(-time.Second)
	startedAt := now.Add(-5 * time.Second)
	run := domain.Run{
		ID:                domain.NewID("run"),
		ScheduleID:        schedule.ID,
		OccurrenceKey:     domain.OccurrenceKey(schedule.ID, now),
		NominalTime:       now,
		DueTime:           now,
		Status:            domain.RunRunning,
		Attempt:           1,
		ClaimedByWorkerID: &workerID,
		ClaimExpiresAt:    &expires,
		StartedAt:         &startedAt,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := st.InsertRun(context.Background(), run); err != nil {
		t.Fatalf("InsertRun returned error: %v", err)
	}
	if _, err := st.DB().ExecContext(context.Background(), `
		INSERT INTO worker_leases (id, worker_id, run_id, lease_key, acquired_at, expires_at, heartbeat_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, domain.NewID("lease"), workerID, run.ID, run.ID,
		startedAt.Format(time.RFC3339Nano),
		expires.Format(time.RFC3339Nano),
		expires.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert lease returned error: %v", err)
	}
	_ = st.Close()

	// Reopen and recover.
	st2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen store returned error: %v", err)
	}
	defer st2.Close()
	if err := st2.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	d := New(st2, executors.NewRegistry(), logging.New(), "wrk_recovery", 30*time.Second)
	d.now = func() time.Time { return now }
	if err := d.recoverExpiredLeases(context.Background(), now); err != nil {
		t.Fatalf("recoverExpiredLeases returned error: %v", err)
	}

	got, err := st2.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if got.Status != domain.RunFailed {
		t.Fatalf("expected running run recovered to failed, got %s", got.Status)
	}
	if got.FinishedAt == nil {
		t.Fatalf("expected finished_at to be set after recovery")
	}

	// Verify retry was scheduled.
	scheduleID := schedule.ID
	items, _, err := st2.ListRuns(context.Background(), &scheduleID, nil, 10, "")
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected original run plus retry attempt, got %d runs", len(items))
	}
}

func TestRetryScheduledRunIsPickedUpAfterReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open returned error: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	schedule := dispatcherSchedule(now)
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	retryAt := now.Add(-10 * time.Second) // already past
	run := domain.Run{
		ID:               domain.NewID("run"),
		ScheduleID:       schedule.ID,
		OccurrenceKey:    domain.OccurrenceKey(schedule.ID, now),
		NominalTime:      now,
		DueTime:          now,
		Status:           domain.RunRetryScheduled,
		Attempt:          2,
		RetryAvailableAt: &retryAt,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := st.InsertRun(context.Background(), run); err != nil {
		t.Fatalf("InsertRun returned error: %v", err)
	}
	_ = st.Close()

	// Reopen and verify the retry-scheduled run is returned as a candidate.
	st2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen store returned error: %v", err)
	}
	defer st2.Close()
	if err := st2.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	candidates, err := st2.ListCandidateRuns(context.Background(), now, 64)
	if err != nil {
		t.Fatalf("ListCandidateRuns returned error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 retry candidate, got %d", len(candidates))
	}
	if candidates[0].ID != run.ID {
		t.Fatalf("expected retry run %s, got %s", run.ID, candidates[0].ID)
	}
	if candidates[0].Attempt != 2 {
		t.Fatalf("expected attempt 2, got %d", candidates[0].Attempt)
	}
}

func TestFailedRunWithoutRetryIsStableAfterReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open returned error: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	schedule := dispatcherSchedule(now)
	schedule.Retry.MaxAttempts = 3
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	// Simulate a crash that left a run marked failed but no retry was inserted.
	finishedAt := now
	errText := "worker lease expired during execution"
	run := domain.Run{
		ID:            domain.NewID("run"),
		ScheduleID:    schedule.ID,
		OccurrenceKey: domain.OccurrenceKey(schedule.ID, now),
		NominalTime:   now,
		DueTime:       now,
		Status:        domain.RunFailed,
		Attempt:       1,
		ErrorText:     &errText,
		FinishedAt:    &finishedAt,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := st.InsertRun(context.Background(), run); err != nil {
		t.Fatalf("InsertRun returned error: %v", err)
	}
	_ = st.Close()

	// Reopen: verify the failed run is stable and not picked up as a candidate.
	// This documents a known gap: if a crash occurs after FinishRun but before retry
	// insertion, the retry is lost. Recovery does not re-derive missing retries.
	st2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen store returned error: %v", err)
	}
	defer st2.Close()
	if err := st2.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	got, err := st2.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if got.Status != domain.RunFailed {
		t.Fatalf("expected failed run to remain failed, got %s", got.Status)
	}
	candidates, err := st2.ListCandidateRuns(context.Background(), now, 64)
	if err != nil {
		t.Fatalf("ListCandidateRuns returned error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected no candidates for failed run without retry, got %d", len(candidates))
	}
	scheduleID := schedule.ID
	items, _, err := st2.ListRuns(context.Background(), &scheduleID, nil, 10, "")
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected only the original failed run (no auto-generated retry), got %d", len(items))
	}
}

func ptrTime(v time.Time) *time.Time { return &v }
