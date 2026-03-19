package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/justyn-clark/wakeplane/internal/app"
	"github.com/justyn-clark/wakeplane/internal/domain"
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
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
	})
	mux.HandleFunc("GET /v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics, err := service.Metrics(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(metrics))
	})
	mux.HandleFunc("POST /v1/schedules", func(w http.ResponseWriter, r *http.Request) {
		var req domain.CreateScheduleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		schedule, errs, err := service.CreateSchedule(r.Context(), req)
		if err != nil {
			writeError(w, err)
			return
		}
		if len(errs) > 0 {
			writeAPIError(w, domain.NewValidationError(errs))
			return
		}
		writeJSON(w, http.StatusCreated, schedule)
	})
	mux.HandleFunc("GET /v1/schedules", func(w http.ResponseWriter, r *http.Request) {
		enabled, err := parseEnabledFilter(r)
		if err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		limit := parseLimit(r)
		cursor, err := parseCursor(r)
		if err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		items, nextCursor, err := service.ListSchedules(r.Context(), enabled, limit, cursor)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, domain.ListResponse[domain.ScheduleSummary]{Items: items, NextCursor: nextCursor})
	})
	mux.HandleFunc("GET /v1/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		schedule, err := service.GetSchedule(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, schedule)
	})
	mux.HandleFunc("PUT /v1/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req domain.UpdateScheduleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		schedule, errs, err := service.ReplaceSchedule(r.Context(), r.PathValue("id"), req)
		if err != nil {
			writeError(w, err)
			return
		}
		if len(errs) > 0 {
			writeAPIError(w, domain.NewValidationError(errs))
			return
		}
		writeJSON(w, http.StatusOK, schedule)
	})
	mux.HandleFunc("PATCH /v1/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req domain.PatchScheduleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		schedule, errs, err := service.PatchSchedule(r.Context(), r.PathValue("id"), req)
		if err != nil {
			writeError(w, err)
			return
		}
		if len(errs) > 0 {
			writeAPIError(w, domain.NewValidationError(errs))
			return
		}
		writeJSON(w, http.StatusOK, schedule)
	})
	mux.HandleFunc("DELETE /v1/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := service.DeleteSchedule(r.Context(), r.PathValue("id")); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": r.PathValue("id")})
	})
	mux.HandleFunc("POST /v1/schedules/{id}/pause", func(w http.ResponseWriter, r *http.Request) {
		schedule, err := service.PauseSchedule(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": schedule.ID, "paused_at": schedule.PausedAt, "enabled": schedule.Enabled})
	})
	mux.HandleFunc("POST /v1/schedules/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		schedule, err := service.ResumeSchedule(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": schedule.ID, "paused_at": schedule.PausedAt, "enabled": schedule.Enabled, "next_run_at": schedule.NextRunAt})
	})
	mux.HandleFunc("POST /v1/schedules/{id}/trigger", func(w http.ResponseWriter, r *http.Request) {
		var req domain.TriggerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		run, err := service.TriggerSchedule(r.Context(), r.PathValue("id"), req.Reason)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run_id": run.ID, "schedule_id": run.ScheduleID, "occurrence_key": run.OccurrenceKey, "status": run.Status, "created_at": run.CreatedAt})
	})
	mux.HandleFunc("GET /v1/schedules/{id}/runs", func(w http.ResponseWriter, r *http.Request) {
		scheduleID := r.PathValue("id")
		status, err := parseStatus(r)
		if err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		cursor, err := parseCursor(r)
		if err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		items, nextCursor, err := service.ListRuns(r.Context(), &scheduleID, status, parseLimit(r), cursor)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, domain.ListResponse[domain.RunSummary]{Items: items, NextCursor: nextCursor})
	})
	mux.HandleFunc("GET /v1/runs", func(w http.ResponseWriter, r *http.Request) {
		var scheduleID *string
		if raw := r.URL.Query().Get("schedule_id"); raw != "" {
			scheduleID = &raw
		}
		status, err := parseStatus(r)
		if err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		cursor, err := parseCursor(r)
		if err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		items, nextCursor, err := service.ListRuns(r.Context(), scheduleID, status, parseLimit(r), cursor)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, domain.ListResponse[domain.RunSummary]{Items: items, NextCursor: nextCursor})
	})
	mux.HandleFunc("GET /v1/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		run, err := service.GetRun(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, run)
	})
	mux.HandleFunc("GET /v1/runs/{id}/receipts", func(w http.ResponseWriter, r *http.Request) {
		items, err := service.ListReceipts(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
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

func parseStatus(r *http.Request) (*domain.RunStatus, error) {
	raw := r.URL.Query().Get("status")
	if raw == "" {
		return nil, nil
	}
	status := domain.RunStatus(raw)
	switch status {
	case domain.RunPending,
		domain.RunClaimed,
		domain.RunRunning,
		domain.RunSucceeded,
		domain.RunFailed,
		domain.RunRetryScheduled,
		domain.RunDeadLettered,
		domain.RunCancelled,
		domain.RunSkipped:
		return &status, nil
	default:
		return nil, fmt.Errorf("invalid status value %q", raw)
	}
}

func parseEnabledFilter(r *http.Request) (*bool, error) {
	raw := r.URL.Query().Get("enabled")
	if raw == "" {
		return nil, nil
	}
	switch raw {
	case "true":
		v := true
		return &v, nil
	case "false":
		v := false
		return &v, nil
	default:
		return nil, fmt.Errorf("invalid enabled value %q: must be true or false", raw)
	}
}

func parseCursor(r *http.Request) (string, error) {
	cursor := r.URL.Query().Get("cursor")
	if cursor == "" {
		return "", nil
	}
	if _, _, err := domain.DecodeCursor(cursor); err != nil {
		return "", fmt.Errorf("invalid cursor")
	}
	return cursor, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) {
		writeAPIError(w, apiErr)
		return
	}
	writeJSON(w, http.StatusInternalServerError, domain.ErrorResponse{Code: "internal_error", Error: err.Error()})
}

func writeAPIError(w http.ResponseWriter, err *domain.APIError) {
	writeJSON(w, err.Status, domain.ErrorResponse{Code: err.Code, Error: err.Message, Details: err.Details})
}
