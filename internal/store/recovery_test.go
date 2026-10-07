package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestSQLiteExpiredFailureAtomicity(t *testing.T) { expiredFailureAtomicity(t, openTestStore) }
func TestPostgresExpiredFailureAtomicity(t *testing.T) {
	expiredFailureAtomicity(t, openPostgresTestStore)
}

func expiredFailureAtomicity(t *testing.T, open func(*testing.T) *Store) {
	t.Helper()
	for _, mode := range []string{"retry", "dead-letter", "renewed", "completed"} {
		t.Run(mode, func(t *testing.T) {
			st := open(t)
			ctx := context.Background()
			now := time.Now().UTC()
			sch := insertTestSchedule(t, st)
			run := insertTestRun(t, st, sch.ID, domain.RunPending, now)
			claimedAt := now.Add(-2 * time.Minute)
			if ok, err := st.ClaimRun(ctx, sch, run.ID, "worker", claimedAt, time.Minute); err != nil || !ok {
				t.Fatalf("claim=%t error=%v", ok, err)
			}
			token, err := st.ExecutionLeaseToken(ctx, run.ID, "worker")
			if err != nil {
				t.Fatal(err)
			}
			if err := st.MarkRunRunningWithLease(ctx, run.ID, "worker", token, claimedAt); err != nil {
				t.Fatal(err)
			}
			expired, err := st.ListExpiredLeases(ctx, now)
			if err != nil || len(expired) != 1 {
				t.Fatalf("expired=%d error=%v", len(expired), err)
			}
			run = expired[0].Run
			run.FinishedAt = &now
			run.ClaimExpiresAt = nil
			run.UpdatedAt = now
			run.Status = domain.RunFailed
			retry := domain.Run{ID: domain.NewID("run"), ScheduleID: "missing-schedule", OccurrenceKey: run.OccurrenceKey, NominalTime: run.NominalTime, DueTime: now, Status: domain.RunRetryScheduled, Attempt: 2, CreatedAt: now, UpdatedAt: now}
			dead := domain.DeadLetter{ID: domain.NewID("dlq"), RunID: run.ID, ScheduleID: "missing-schedule", OccurrenceKey: run.OccurrenceKey, Reason: "expired", CreatedAt: now}
			var retryPtr *domain.Run
			var deadPtr *domain.DeadLetter
			switch mode {
			case "retry":
				retryPtr = &retry
			case "dead-letter":
				run.Status = domain.RunDeadLettered
				deadPtr = &dead
			case "renewed":
				if err := st.RenewLeaseWithToken(ctx, run.ID, "worker", token, claimedAt.Add(30*time.Second), 3*time.Minute); err != nil {
					t.Fatal(err)
				}
			case "completed":
				completed := run
				completed.Status = domain.RunSucceeded
				if err := st.FinishRunWithLease(ctx, completed, "worker", token, now); err != nil {
					t.Fatal(err)
				}
			}
			err = st.FinishExpiredRunFailure(ctx, run, retryPtr, deadPtr, now)
			if err == nil {
				t.Fatal("invalid/stale recovery committed")
			}
			if (mode == "renewed" || mode == "completed") && !errors.Is(err, ErrLeaseLost) {
				t.Fatalf("stale recovery error=%v", err)
			}
			got, err := st.GetRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := domain.RunRunning
			if mode == "completed" {
				want = domain.RunSucceeded
			}
			if got.Status != want {
				t.Fatalf("recovery changed status to %s, want %s", got.Status, want)
			}
			leases, err := st.WorkerLeaseCount(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "completed" && leases != 1 {
				t.Fatalf("rollback removed lease: %d", leases)
			}
			if mode == "retry" || mode == "dead-letter" {
				// Repeat recovery after repairing the follow-up: one atomic commit,
				// and a repeated stale attempt cannot duplicate it.
				retry.ScheduleID = sch.ID
				dead.ScheduleID = sch.ID
				if err := st.FinishExpiredRunFailure(ctx, run, retryPtr, deadPtr, now); err != nil {
					t.Fatal(err)
				}
				if err := st.FinishExpiredRunFailure(ctx, run, retryPtr, deadPtr, now); !errors.Is(err, ErrLeaseLost) {
					t.Fatalf("repeat recovery=%v", err)
				}
				got, err = st.GetRun(ctx, run.ID)
				if err != nil || got.Status != run.Status {
					t.Fatalf("committed run=%+v error=%v", got, err)
				}
				if leases, err = st.WorkerLeaseCount(ctx); err != nil || leases != 0 {
					t.Fatalf("lease removal count=%d error=%v", leases, err)
				}
				var count int
				if mode == "retry" {
					err = st.queryRow(ctx, `SELECT COUNT(*) FROM schedule_runs WHERE occurrence_key = ? AND attempt = 2`, run.OccurrenceKey).Scan(&count)
				} else {
					err = st.queryRow(ctx, `SELECT COUNT(*) FROM dead_letters WHERE run_id = ?`, run.ID).Scan(&count)
				}
				if err != nil || count != 1 {
					t.Fatalf("follow-up count=%d error=%v", count, err)
				}
			}
		})
	}
}
