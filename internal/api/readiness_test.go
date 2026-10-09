package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadinessHTTPStatusReflectsStorageFailure(t *testing.T) {
	service, err := newTestService(t)
	if err != nil {
		t.Fatal(err)
	}
	mux := NewMux(service)
	for _, tc := range []struct {
		name       string
		closeStore bool
		code       int
		ok         bool
		storage    string
	}{
		{"reachable", false, http.StatusOK, true, "ok"},
		{"unavailable", true, http.StatusServiceUnavailable, false, "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.closeStore {
				service.Close()
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != tc.code {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.code, rec.Body.String())
			}
			var body struct {
				OK      bool   `json:"ok"`
				Storage string `json:"storage"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.OK != tc.ok || body.Storage != tc.storage {
				t.Errorf("readiness = %+v", body)
			}
		})
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("liveness follows storage failure: %d", rec.Code)
	}
}
