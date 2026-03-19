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
	service, err := newTestService(t)
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

func TestListSchedulesEnabledFilter(t *testing.T) {
	service, err := newTestService(t)
	if err != nil {
		t.Fatalf("app.New returned error: %v", err)
	}
	defer service.Close()

	createScheduleForListTest(t, service, "enabled-schedule", true)
	createScheduleForListTest(t, service, "disabled-schedule", false)

	testCases := []struct {
		name           string
		query          string
		wantStatus     int
		wantItems      int
		wantEnabled    *bool
		wantErrorCode  string
		wantErrorMatch string
	}{
		{name: "omitted enabled", query: "/v1/schedules", wantStatus: http.StatusOK, wantItems: 2},
		{name: "enabled true", query: "/v1/schedules?enabled=true", wantStatus: http.StatusOK, wantItems: 1, wantEnabled: boolPtr(true)},
		{name: "enabled false", query: "/v1/schedules?enabled=false", wantStatus: http.StatusOK, wantItems: 1, wantEnabled: boolPtr(false)},
		{name: "enabled foo rejected", query: "/v1/schedules?enabled=foo", wantStatus: http.StatusBadRequest, wantErrorCode: "bad_request", wantErrorMatch: `invalid enabled value "foo": must be true or false`},
		{name: "enabled one rejected", query: "/v1/schedules?enabled=1", wantStatus: http.StatusBadRequest, wantErrorCode: "bad_request", wantErrorMatch: `invalid enabled value "1": must be true or false`},
		{name: "enabled uppercase rejected", query: "/v1/schedules?enabled=TRUE", wantStatus: http.StatusBadRequest, wantErrorCode: "bad_request", wantErrorMatch: `invalid enabled value "TRUE": must be true or false`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.query, nil)
			rec := httptest.NewRecorder()

			NewMux(service).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}

			if tc.wantStatus != http.StatusOK {
				var body domain.ErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("json.Unmarshal returned error: %v", err)
				}
				if body.Code != tc.wantErrorCode {
					t.Fatalf("expected error code %q, got %q", tc.wantErrorCode, body.Code)
				}
				if body.Error != tc.wantErrorMatch {
					t.Fatalf("expected error %q, got %q", tc.wantErrorMatch, body.Error)
				}
				return
			}

			var body domain.ListResponse[domain.ScheduleSummary]
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if len(body.Items) != tc.wantItems {
				t.Fatalf("expected %d items, got %d", tc.wantItems, len(body.Items))
			}
			if tc.wantEnabled == nil {
				return
			}
			for _, item := range body.Items {
				if item.Enabled != *tc.wantEnabled {
					t.Fatalf("expected all items enabled=%t, got schedule %q enabled=%t", *tc.wantEnabled, item.ID, item.Enabled)
				}
			}
		})
	}
}

func TestListSchedulesRejectsMalformedCursor(t *testing.T) {
	service, err := newTestService(t)
	if err != nil {
		t.Fatalf("app.New returned error: %v", err)
	}
	defer service.Close()

	req := httptest.NewRequest(http.MethodGet, "/v1/schedules?cursor=not-a-cursor", nil)
	rec := httptest.NewRecorder()

	NewMux(service).ServeHTTP(rec, req)

	assertErrorResponse(t, rec, http.StatusBadRequest, "bad_request", "invalid cursor")
}

func TestListRunsStatusAndCursorValidation(t *testing.T) {
	service, err := newTestService(t)
	if err != nil {
		t.Fatalf("app.New returned error: %v", err)
	}
	defer service.Close()

	scheduleID := createScheduleForListTest(t, service, "run-filter-schedule", true)
	run, err := service.TriggerSchedule(context.Background(), scheduleID, "test run")
	if err != nil {
		t.Fatalf("TriggerSchedule returned error: %v", err)
	}

	testCases := []struct {
		name           string
		path           string
		wantStatus     int
		wantItems      int
		wantErrorCode  string
		wantErrorMatch string
	}{
		{name: "omitted status", path: "/v1/runs", wantStatus: http.StatusOK, wantItems: 1},
		{name: "valid status", path: "/v1/runs?status=pending", wantStatus: http.StatusOK, wantItems: 1},
		{name: "invalid status", path: "/v1/runs?status=foo", wantStatus: http.StatusBadRequest, wantErrorCode: "bad_request", wantErrorMatch: `invalid status value "foo"`},
		{name: "uppercase status rejected", path: "/v1/runs?status=FAILED", wantStatus: http.StatusBadRequest, wantErrorCode: "bad_request", wantErrorMatch: `invalid status value "FAILED"`},
		{name: "invalid cursor", path: "/v1/runs?cursor=not-a-cursor", wantStatus: http.StatusBadRequest, wantErrorCode: "bad_request", wantErrorMatch: "invalid cursor"},
		{name: "schedule runs valid status", path: "/v1/schedules/" + scheduleID + "/runs?status=pending", wantStatus: http.StatusOK, wantItems: 1},
		{name: "schedule runs invalid status", path: "/v1/schedules/" + scheduleID + "/runs?status=foo", wantStatus: http.StatusBadRequest, wantErrorCode: "bad_request", wantErrorMatch: `invalid status value "foo"`},
		{name: "schedule runs invalid cursor", path: "/v1/schedules/" + scheduleID + "/runs?cursor=not-a-cursor", wantStatus: http.StatusBadRequest, wantErrorCode: "bad_request", wantErrorMatch: "invalid cursor"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()

			NewMux(service).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				assertErrorResponse(t, rec, tc.wantStatus, tc.wantErrorCode, tc.wantErrorMatch)
				return
			}

			var body domain.ListResponse[domain.RunSummary]
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if len(body.Items) != tc.wantItems {
				t.Fatalf("expected %d items, got %d", tc.wantItems, len(body.Items))
			}
			if len(body.Items) == 1 && body.Items[0].ID != run.ID {
				t.Fatalf("expected run %q, got %q", run.ID, body.Items[0].ID)
			}
		})
	}
}

func newTestService(t *testing.T) (*app.Service, error) {
	t.Helper()
	return app.New(context.Background(), config.Config{
		DatabasePath:       filepath.Join(t.TempDir(), "wakeplane.db"),
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: time.Second,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_test",
		Version:            "test",
	})
}

func createScheduleForListTest(t *testing.T, service *app.Service, name string, enabled bool) string {
	t.Helper()
	schedule, errs, err := service.CreateSchedule(context.Background(), domain.CreateScheduleRequest{
		Name:     name,
		Enabled:  enabled,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindWorkflow, WorkflowID: "sync.customers"},
		Policy:   domain.DefaultPolicy(),
		Retry:    domain.DefaultRetryPolicy(),
	})
	if err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("CreateSchedule returned validation errors: %+v", errs)
	}
	return schedule.ID
}

func boolPtr(v bool) *bool {
	return &v
}

func assertErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode, wantError string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("expected status %d, got %d: %s", wantStatus, rec.Code, rec.Body.String())
	}
	var body domain.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if body.Code != wantCode {
		t.Fatalf("expected error code %q, got %q", wantCode, body.Code)
	}
	if body.Error != wantError {
		t.Fatalf("expected error %q, got %q", wantError, body.Error)
	}
}
