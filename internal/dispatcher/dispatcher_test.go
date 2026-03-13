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

func ptrTime(v time.Time) *time.Time { return &v }
