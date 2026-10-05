package store

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestPostgresClaimSerializesConcurrentWorkers(t *testing.T) {
	ctx := context.Background()
	st := openPostgresTestStore(t)

	schedule := insertTestSchedule(t, st)
	now := time.Now().UTC()
	runA := insertTestRun(t, st, schedule.ID, domain.RunPending, now)
	runB := insertTestRun(t, st, schedule.ID, domain.RunPending, now)

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for _, item := range []struct {
		runID    string
		workerID string
	}{
		{runID: runA.ID, workerID: "wrk_pg_a"},
		{runID: runB.ID, workerID: "wrk_pg_b"},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := st.ClaimRun(ctx, schedule, item.runID, item.workerID, now, time.Minute)
			if err != nil {
				t.Errorf("ClaimRun returned error: %v", err)
				return
			}
			results <- claimed
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	claimedCount := 0
	for claimed := range results {
		if claimed {
			claimedCount++
		}
	}
	if claimedCount != 1 {
		t.Fatalf("expected exactly one concurrent claim, got %d", claimedCount)
	}
}

func TestPostgresProductionStoreInvariants(t *testing.T) {
	runProductionStoreInvariants(t, openPostgresTestStore(t))
}

func openPostgresTestStore(t *testing.T) *Store {
	t.Helper()
	databaseURL := os.Getenv("WAKEPLANE_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("set WAKEPLANE_POSTGRES_TEST_URL to run Postgres store tests")
	}
	st, err := OpenPostgres(databaseURL)
	if err != nil {
		t.Fatalf("OpenPostgres returned error: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	resetPostgresTestDB(t, st)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	return st
}

func resetPostgresTestDB(t *testing.T, st *Store) {
	t.Helper()
	for _, table := range []string{
		"external_jobs",
		"trigger_events",
		"request_audit_logs",
		"execution_receipts",
		"dead_letters",
		"worker_leases",
		"schedule_runs",
		"schedules",
	} {
		if _, err := st.exec(context.Background(), "DROP TABLE IF EXISTS "+table+" CASCADE"); err != nil {
			t.Fatalf("drop table %s: %v", table, err)
		}
	}
}
