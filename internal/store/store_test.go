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
