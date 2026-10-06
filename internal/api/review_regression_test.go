package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEventNumericPayloadChangedRetryConflicts(t *testing.T) {
	service, err := newTestService(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	mux := NewMux(service)
	scheduleID := createScheduleForListTest(t, service, "precise-event", true)
	for index, value := range []string{"9007199254740992", "9007199254740993"} {
		body := `{"id":"delivery","source":"precision-test","data":{"record_id":` + value + `}}`
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/schedules/"+scheduleID+"/events", strings.NewReader(body)))
		want := http.StatusCreated
		if index == 1 {
			want = http.StatusConflict
		}
		if response.Code != want {
			t.Fatalf("payload %s: HTTP %d, want %d; %s", value, response.Code, want, response.Body.String())
		}
	}
	items, _, err := service.ListRuns(context.Background(), &scheduleID, nil, nil, 50, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("event run count=%d error=%v", len(items), err)
	}
}

func TestStoredEventInspectionPreservesJSONNumber(t *testing.T) {
	service, err := newTestService(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	mux := NewMux(service)
	scheduleID := createScheduleForListTest(t, service, "inspect-precise-event", true)
	body := `{"id":"delivery","source":"precision-test","data":{"record_id":9007199254740993}}`
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/schedules/"+scheduleID+"/events", strings.NewReader(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("event creation: %d %s", response.Code, response.Body.String())
	}
	items, _, err := service.ListRuns(context.Background(), &scheduleID, nil, nil, 50, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("event run count=%d error=%v", len(items), err)
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/runs/"+items[0].ID, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("run inspection: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Event struct {
			Data map[string]any `json:"data"`
		} `json:"event"`
	}
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	value, ok := result.Event.Data["record_id"].(json.Number)
	if !ok || value.String() != "9007199254740993" {
		t.Fatalf("stored event lost precision: %#v", result.Event.Data["record_id"])
	}
}
