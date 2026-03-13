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

	"github.com/justyn-clark/wakeplane/internal/app"
	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestCreateScheduleRoute(t *testing.T) {
	service, err := app.New(context.Background(), config.Config{
		DatabasePath:       filepath.Join(t.TempDir(), "wakeplane.db"),
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

func TestGetMissingScheduleReturnsErrorEnvelope(t *testing.T) {
	service, err := app.New(context.Background(), config.Config{
		DatabasePath:       filepath.Join(t.TempDir(), "wakeplane.db"),
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

	req := httptest.NewRequest(http.MethodGet, "/v1/schedules/missing", nil)
	rec := httptest.NewRecorder()
	NewMux(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	var body domain.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if body.Code != "not_found" {
		t.Fatalf("expected not_found code, got %q", body.Code)
	}
}
