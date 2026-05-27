package cli

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/api"
	"github.com/justyn-clark/wakeplane/internal/app"
	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
	"net/http/httptest"
)

func TestPostgresCLIExportManifest(t *testing.T) {
	databaseURL := os.Getenv("WAKEPLANE_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("set WAKEPLANE_POSTGRES_TEST_URL to run Postgres CLI tests")
	}
	resetPostgresCLITestDB(t, databaseURL)

	service, err := app.New(context.Background(), config.Config{
		StoreDialect:       "postgres",
		DatabaseURL:        databaseURL,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: time.Second,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_pg_cli",
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("app.New returned error: %v", err)
	}
	defer service.Close()
	server := httptest.NewServer(api.NewMux(service))
	defer server.Close()

	if _, errs, err := service.CreateSchedule(context.Background(), domain.CreateScheduleRequest{
		Name:     "postgres-cli-export",
		Enabled:  true,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindShell, Command: "/bin/echo", Args: []string{"postgres-cli"}},
		Policy:   domain.DefaultPolicy(),
		Retry:    domain.DefaultRetryPolicy(),
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateSchedule failed: %v %+v", err, errs)
	}
	manifest, err := exportScheduleManifest(server.URL)
	if err != nil {
		t.Fatalf("exportScheduleManifest returned error: %v", err)
	}
	if len(manifest.Schedules) != 1 {
		t.Fatalf("expected one exported schedule, got %d", len(manifest.Schedules))
	}
	got := manifest.Schedules[0]
	if got.Name != "postgres-cli-export" || got.Target.Command != "/bin/echo" {
		t.Fatalf("unexpected exported schedule: %+v", got)
	}
}

func resetPostgresCLITestDB(t *testing.T, databaseURL string) {
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
