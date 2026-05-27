package app

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestPostgresServiceCreateTriggerAndStatus(t *testing.T) {
	databaseURL := os.Getenv("WAKEPLANE_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("set WAKEPLANE_POSTGRES_TEST_URL to run Postgres service tests")
	}
	resetPostgresServiceTestDB(t, databaseURL)

	service, err := New(context.Background(), config.Config{
		StoreDialect:       "postgres",
		DatabaseURL:        databaseURL,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: time.Second,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_pg_service",
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer service.Close()

	schedule, errs, err := service.CreateSchedule(context.Background(), domain.CreateScheduleRequest{
		Name:     "postgres-service-check",
		Enabled:  true,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindShell, Command: "/bin/echo"},
		Policy:   domain.DefaultPolicy(),
		Retry:    domain.DefaultRetryPolicy(),
	})
	if err != nil || len(errs) > 0 {
		t.Fatalf("CreateSchedule failed: %v %+v", err, errs)
	}
	run, err := service.TriggerSchedule(context.Background(), schedule.ID, "postgres service parity")
	if err != nil {
		t.Fatalf("TriggerSchedule returned error: %v", err)
	}
	got, err := service.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if got.Status != domain.RunPending {
		t.Fatalf("expected triggered run to be pending, got %s", got.Status)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Database.Driver != "postgres" {
		t.Fatalf("expected postgres database driver, got %q", status.Database.Driver)
	}
	if strings.Contains(status.Database.Path, "wakeplane:wakeplane") {
		t.Fatalf("status database path leaked credentials: %q", status.Database.Path)
	}
	if status.Scheduler.DueRuns == 0 {
		t.Fatalf("expected status to show due runs")
	}
}

func resetPostgresServiceTestDB(t *testing.T, databaseURL string) {
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
