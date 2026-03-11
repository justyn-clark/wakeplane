package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/justyn-clark/timekeeper/internal/app"
	"github.com/justyn-clark/timekeeper/internal/domain"
)

func NewMux(service *app.Service) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, service.Health(r.Context()))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, service.Ready(r.Context()))
	})
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		status, err := service.Status(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	})
	mux.HandleFunc("GET /v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics, err := service.Metrics(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(metrics))
	})
	mux.HandleFunc("POST /v1/schedules", func(w http.ResponseWriter, r *http.Request) {
		var req domain.CreateScheduleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		schedule, errs, err := service.CreateSchedule(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if len(errs) > 0 {
			writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{Error: "validation failed", Details: errs})
			return
		}
		writeJSON(w, http.StatusCreated, schedule)
	})
	mux.HandleFunc("GET /v1/schedules", func(w http.ResponseWriter, r *http.Request) {
		var enabled *bool
		if raw := r.URL.Query().Get("enabled"); raw != "" {
			v := raw == "true"
			enabled = &v
		}
		limit := parseLimit(r)
		cursor := r.URL.Query().Get("cursor")
		items, nextCursor, err := service.ListSchedules(r.Context(), enabled, limit, cursor)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, domain.ListResponse[domain.ScheduleSummary]{Items: items, NextCursor: nextCursor})
	})
	mux.HandleFunc("GET /v1/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		schedule, err := service.GetSchedule(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, schedule)
	})
	mux.HandleFunc("PUT /v1/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req domain.UpdateScheduleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		schedule, errs, err := service.ReplaceSchedule(r.Context(), r.PathValue("id"), req)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if len(errs) > 0 {
			writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{Error: "validation failed", Details: errs})
			return
		}
		writeJSON(w, http.StatusOK, schedule)
	})
	mux.HandleFunc("PATCH /v1/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req domain.PatchScheduleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		schedule, errs, err := service.PatchSchedule(r.Context(), r.PathValue("id"), req)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if len(errs) > 0 {
			writeJSON(w, http.StatusBadRequest, domain.ErrorResponse{Error: "validation failed", Details: errs})
			return
		}
		writeJSON(w, http.StatusOK, schedule)
	})
	mux.HandleFunc("DELETE /v1/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := service.DeleteSchedule(r.Context(), r.PathValue("id")); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": r.PathValue("id")})
	})
	mux.HandleFunc("POST /v1/schedules/{id}/pause", func(w http.ResponseWriter, r *http.Request) {
		schedule, err := service.PauseSchedule(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": schedule.ID, "paused_at": schedule.PausedAt, "enabled": schedule.Enabled})
	})
	mux.HandleFunc("POST /v1/schedules/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		schedule, err := service.ResumeSchedule(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": schedule.ID, "paused_at": schedule.PausedAt, "enabled": schedule.Enabled, "next_run_at": schedule.NextRunAt})
	})
	mux.HandleFunc("POST /v1/schedules/{id}/trigger", func(w http.ResponseWriter, r *http.Request) {
		var req domain.TriggerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		run, err := service.TriggerSchedule(r.Context(), r.PathValue("id"), req.Reason)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run_id": run.ID, "schedule_id": run.ScheduleID, "occurrence_key": run.OccurrenceKey, "status": run.Status, "created_at": run.CreatedAt})
	})
	mux.HandleFunc("GET /v1/schedules/{id}/runs", func(w http.ResponseWriter, r *http.Request) {
		scheduleID := r.PathValue("id")
		status := parseStatus(r)
		items, nextCursor, err := service.ListRuns(r.Context(), &scheduleID, status, parseLimit(r), r.URL.Query().Get("cursor"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, domain.ListResponse[domain.RunSummary]{Items: items, NextCursor: nextCursor})
	})
	mux.HandleFunc("GET /v1/runs", func(w http.ResponseWriter, r *http.Request) {
		var scheduleID *string
		if raw := r.URL.Query().Get("schedule_id"); raw != "" {
			scheduleID = &raw
		}
		status := parseStatus(r)
		items, nextCursor, err := service.ListRuns(r.Context(), scheduleID, status, parseLimit(r), r.URL.Query().Get("cursor"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, domain.ListResponse[domain.RunSummary]{Items: items, NextCursor: nextCursor})
	})
	mux.HandleFunc("GET /v1/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		run, err := service.GetRun(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, run)
	})
	mux.HandleFunc("GET /v1/runs/{id}/receipts", func(w http.ResponseWriter, r *http.Request) {
		items, err := service.ListReceipts(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, domain.ListResponse[domain.Receipt]{Items: items})
	})

	return mux
}

func parseLimit(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 50
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 50
	}
	return n
}

func parseStatus(r *http.Request) *domain.RunStatus {
	raw := r.URL.Query().Get("status")
	if raw == "" {
		return nil
	}
	status := domain.RunStatus(raw)
	return &status
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, domain.ErrorResponse{Error: err.Error()})
}
