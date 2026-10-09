package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestEmptyListRoutesReturnArrays(t *testing.T) {
	service, err := newTestService(t)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx := context.Background()
	mux := NewMux(service)
	check := func(path string) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d: %s", path, rec.Code, rec.Body.String())
		}
		var result struct {
			Items      json.RawMessage `json:"items"`
			NextCursor json.RawMessage `json:"next_cursor"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if string(result.Items) != "[]" || string(result.NextCursor) != "null" {
			t.Errorf("%s empty response=%s", path, rec.Body.String())
		}
	}
	check("/v1/schedules")
	check("/v1/runs")
	schedule, errs, err := service.CreateSchedule(ctx, domain.CreateScheduleRequest{
		Name: "empty-history", Timezone: "UTC", Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target: domain.TargetSpec{Kind: domain.TargetKindHTTP, URL: "https://example.com/job", Method: "POST"},
	})
	if err != nil || len(errs) > 0 {
		t.Fatalf("create: %v %+v", err, errs)
	}
	check("/v1/schedules/" + schedule.ID + "/runs")
	run, err := service.TriggerSchedule(ctx, schedule.ID, "empty receipts contract")
	if err != nil {
		t.Fatal(err)
	}
	check("/v1/runs/" + run.ID + "/receipts")
	check("/v1/runs?status=succeeded")
	check("/v1/schedules?enabled=true")
}
