package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/justyn-clark/timekeeper/internal/config"
	"github.com/justyn-clark/timekeeper/internal/domain"
)

func TestServiceCreateTriggerAndInspectRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "timekeeper.db")
	service, err := New(context.Background(), config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: 100 * time.Millisecond,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_test",
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer service.Close()

	schedule, errs, err := service.CreateSchedule(context.Background(), domain.CreateScheduleRequest{
		Name:     "nightly-sync",
		Enabled:  true,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindWorkflow, WorkflowID: "sync.customers", Input: map[string]any{"source": "crm"}},
		Policy:   domain.DefaultPolicy(),
		Retry:    domain.DefaultRetryPolicy(),
	})
	if err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("CreateSchedule returned validation errors: %+v", errs)
	}

	run, err := service.TriggerSchedule(context.Background(), schedule.ID, "manual operator trigger")
	if err != nil {
		t.Fatalf("TriggerSchedule returned error: %v", err)
	}
	got, err := service.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if got.OccurrenceKey[:7] != "manual:" {
		t.Fatalf("expected manual occurrence key, got %s", got.OccurrenceKey)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected sqlite database at %s: %v", dbPath, err)
	}
}
