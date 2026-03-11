package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/justyn-clark/timekeeper/internal/app"
	"github.com/justyn-clark/timekeeper/internal/config"
	"github.com/justyn-clark/timekeeper/internal/domain"
)

func TestCreateScheduleRoute(t *testing.T) {
	service, err := app.New(context.Background(), config.Config{
		DatabasePath:       filepath.Join(t.TempDir(), "timekeeper.db"),
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: time.Second,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_test",
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("app.New returned error: %v", err)
	}
	defer service.Close()

	body, _ := json.Marshal(domain.CreateScheduleRequest{
		Name:     "nightly-sync",
		Enabled:  true,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindWorkflow, WorkflowID: "sync.customers"},
		Policy:   domain.DefaultPolicy(),
		Retry:    domain.DefaultRetryPolicy(),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/schedules", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	NewMux(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, rec.Code, rec.Body.String())
	}
}
