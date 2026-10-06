package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justyn-clark/wakeplane/internal/app"
	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestAutomationRoutesRequireAuthAndRecordAudit(t *testing.T) {
	service, err := app.New(context.Background(), config.Config{DatabasePath: filepath.Join(t.TempDir(), "auth.db"), AuthToken: "test-operator", RequestAudit: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	mux := NewMux(service)
	for _, path := range []string{"/v1/templates", "/v1/schedules/preview", "/v1/mcp", "/v1/schedules/missing/events", "/v1/runs/missing/reconcile"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s bypassed auth: %d", path, rec.Code)
		}
	}
	count, err := service.RequestAuditCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("new routes not audited: %d", count)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"route-test","version":"1"}}}`))
	req.Header.Set("Authorization", "Bearer test-operator")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "wakeplane") {
		t.Fatalf("authorized MCP initialize: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPreviewAndEventHTTPContracts(t *testing.T) {
	service, err := newTestService(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	mux := NewMux(service)
	scheduleID := createScheduleForListTest(t, service, "event-api", true)
	for _, body := range []string{`{"id":"one","source":"fixture","data":{"value":1}}`, `{"id":"one","source":"fixture","data":{"value":1}}`, `{"id":"one","source":"fixture","data":{"value":2}}`} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/schedules/"+scheduleID+"/events", strings.NewReader(body)))
		if strings.Contains(body, `"value":2`) {
			if rec.Code != http.StatusConflict {
				t.Fatalf("changed replay: %d", rec.Code)
			}
			continue
		}
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			t.Fatalf("event response: %d %s", rec.Code, rec.Body.String())
		}
	}
	items, _, err := service.ListRuns(context.Background(), &scheduleID, nil, nil, 50, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("event retry created duplicate run: %d %v", len(items), err)
	}
	preview := domain.CreateScheduleRequest{Name: "draft", Timezone: "UTC", Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindCron, Expr: "0 9 * * *"}, Target: domain.TargetSpec{Kind: domain.TargetKindHTTP, Method: "GET", URL: "https://example.test/health"}}
	body, _ := json.Marshal(preview)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/schedules/preview", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body.String())
	}
	var result struct {
		NextRuns []string `json:"next_runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || len(result.NextRuns) != 5 {
		t.Fatalf("preview: %+v %v", result, err)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/schedules/preview", strings.NewReader(string(body)+` {}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatal("trailing JSON accepted")
	}
}
