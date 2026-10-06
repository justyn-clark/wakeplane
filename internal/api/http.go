package api

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/justyn-clark/wakeplane/internal/app"
	"github.com/justyn-clark/wakeplane/internal/domain"
	wakeplaneMCP "github.com/justyn-clark/wakeplane/internal/mcp"
	"github.com/justyn-clark/wakeplane/internal/templates"
)

//go:embed console/*
var consoleFS embed.FS

func NewMux(service *app.Service) http.Handler {
	mux := http.NewServeMux()
	mcpHandler := wakeplaneMCP.NewHandler(service, service.Version())
	mux.HandleFunc("POST /v1/mcp", mcpHandler.ServeHTTP)
	mux.HandleFunc("GET /v1/mcp", mcpHandler.ServeHTTP)
	mux.HandleFunc("DELETE /v1/mcp", mcpHandler.ServeHTTP)
	mux.HandleFunc("GET /v1/templates", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"items": templates.List()})
	})
	mux.HandleFunc("POST /v1/runs/{id}/reconcile", func(w http.ResponseWriter, r *http.Request) {
		run, err := service.ReconcileExternalJob(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, run)
	})
	mux.HandleFunc("POST /v1/schedules/preview", func(w http.ResponseWriter, r *http.Request) {
		var req domain.CreateScheduleRequest
		if err := decodeBoundedJSON(w, r, &req); err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		runs, errs, err := service.PreviewSchedule(r.Context(), req)
		if err != nil {
			writeError(w, err)
			return
		}
		if len(errs) > 0 {
			writeAPIError(w, domain.NewValidationError(errs))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"next_runs": runs})
	})
	mux.HandleFunc("POST /v1/schedules/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		var event domain.TriggerEvent
		if err := decodeBoundedJSON(w, r, &event); err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		run, created, err := service.TriggerEvent(r.Context(), r.PathValue("id"), event)
		if err != nil {
			writeError(w, err)
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{"run": run, "created": created})
	})

	consoleAssets, err := fs.Sub(consoleFS, "console")
	if err != nil {
		panic(err)
	}
	consoleHandler := http.FileServer(http.FS(consoleAssets))
	mux.Handle("GET /console/", http.StripPrefix("/console/", consoleHandler))
	mux.HandleFunc("GET /console", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/console/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/console/", http.StatusFound)
	})

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
		targetKind, err := parseTargetKind(r)
		if err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		cursor, err := parseCursor(r)
		if err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		items, nextCursor, err := service.ListRuns(r.Context(), &scheduleID, status, targetKind, parseLimit(r), cursor)
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
		targetKind, err := parseTargetKind(r)
		if err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		cursor, err := parseCursor(r)
		if err != nil {
			writeError(w, domain.NewBadRequestError(err.Error()))
			return
		}
		items, nextCursor, err := service.ListRuns(r.Context(), scheduleID, status, targetKind, parseLimit(r), cursor)
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

	return controlPlaneMiddleware(service, mux)
}

func decodeBoundedJSON(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("request must contain exactly one JSON object")
	}
	return nil
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func controlPlaneMiddleware(service *app.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		authSubject := ""
		if token := service.AuthToken(); token != "" {
			if !authorizedBearer(r.Header.Get("Authorization"), token) {
				w.Header().Set("Content-Type", "application/json")
				rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusUnauthorized}
				writeAPIError(rec, domain.NewUnauthorizedError("missing or invalid bearer token"))
				recordAudit(context.Background(), service, r, rec.statusCode, authSubject)
				return
			}
			authSubject = "operator"
		}
		rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rec, r)
		recordAudit(context.Background(), service, r, rec.statusCode, authSubject)
	})
}

func authorizedBearer(header, token string) bool {
	if header == "" {
		return false
	}
	value := strings.TrimSpace(header)
	if !strings.HasPrefix(value, "Bearer ") {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func recordAudit(ctx context.Context, service *app.Service, r *http.Request, statusCode int, authSubject string) {
	if !service.RequestAuditEnabled() {
		return
	}
	_ = service.RecordRequestAudit(ctx, domain.RequestAudit{
		Method:      r.Method,
		Path:        r.URL.Path,
		StatusCode:  statusCode,
		RemoteAddr:  r.RemoteAddr,
		UserAgent:   r.UserAgent(),
		AuthSubject: authSubject,
		CreatedAt:   time.Now().UTC(),
	})
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

func parseTargetKind(r *http.Request) (*domain.TargetKind, error) {
	raw := r.URL.Query().Get("target_kind")
	if raw == "" {
		return nil, nil
	}
	kind := domain.TargetKind(raw)
	switch kind {
	case domain.TargetKindHTTP, domain.TargetKindShell, domain.TargetKindWorkflow:
		return &kind, nil
	default:
		return nil, fmt.Errorf("invalid target_kind value %q", raw)
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
