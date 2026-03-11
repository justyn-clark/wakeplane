package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/justyn-clark/timekeeper/internal/config"
	"github.com/justyn-clark/timekeeper/internal/dispatcher"
	"github.com/justyn-clark/timekeeper/internal/domain"
	"github.com/justyn-clark/timekeeper/internal/executors"
	httpExec "github.com/justyn-clark/timekeeper/internal/executors/http"
	shellExec "github.com/justyn-clark/timekeeper/internal/executors/shell"
	workflowExec "github.com/justyn-clark/timekeeper/internal/executors/workflow"
	"github.com/justyn-clark/timekeeper/internal/logging"
	"github.com/justyn-clark/timekeeper/internal/planner"
	"github.com/justyn-clark/timekeeper/internal/scheduler"
	"github.com/justyn-clark/timekeeper/internal/store"
	"github.com/justyn-clark/timekeeper/internal/timecalc"
)

type Service struct {
	cfg              config.Config
	logger           *slog.Logger
	store            *store.Store
	planner          *planner.Planner
	dispatcher       *dispatcher.Dispatcher
	schedulerLoop    *scheduler.Loop
	workflowRegistry *executors.WorkflowRegistry
	startedAt        time.Time
}

func New(ctx context.Context, cfg config.Config) (*Service, error) {
	logger := logging.New()
	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		return nil, err
	}
	workflowRegistry := executors.NewWorkflowRegistry()
	workflowRegistry.Register("sync.customers", func(ctx context.Context, input map[string]any) (map[string]any, error) {
		return map[string]any{
			"workflow_id": "sync.customers",
			"input":       input,
			"status":      "completed",
		}, nil
	})
	registry := executors.NewRegistry(
		httpExec.New(),
		shellExec.New(),
		workflowExec.New(workflowRegistry),
	)
	pl := planner.New(st, logger)
	disp := dispatcher.New(st, registry, logger, cfg.WorkerID, cfg.LeaseTTL)
	return &Service{
		cfg:              cfg,
		logger:           logger,
		store:            st,
		planner:          pl,
		dispatcher:       disp,
		schedulerLoop:    scheduler.New(pl, logger, cfg.SchedulerInterval),
		workflowRegistry: workflowRegistry,
		startedAt:        time.Now().UTC(),
	}, nil
}

func (s *Service) Close() error {
	return s.store.Close()
}

func (s *Service) Run(ctx context.Context) error {
	go s.runDispatcher(ctx)
	return s.schedulerLoop.Run(ctx)
}

func (s *Service) runDispatcher(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.DispatcherInterval)
	defer ticker.Stop()
	for {
		if err := s.dispatcher.Tick(ctx); err != nil && ctx.Err() == nil {
			s.logger.Error("dispatcher tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) CreateSchedule(ctx context.Context, req domain.CreateScheduleRequest) (domain.Schedule, []domain.ValidationError, error) {
	req = withDefaults(req)
	if errs := domain.ValidateCreateSchedule(req); len(errs) > 0 {
		return domain.Schedule{}, errs, nil
	}
	now := time.Now().UTC()
	schedule := domain.Schedule{
		ID:        domain.NewID("sch"),
		Name:      req.Name,
		Enabled:   req.Enabled,
		Timezone:  req.Timezone,
		Schedule:  req.Schedule,
		Target:    req.Target,
		Policy:    req.Policy,
		Retry:     req.Retry,
		StartAt:   req.StartAt,
		EndAt:     req.EndAt,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if schedule.Schedule.Kind == domain.ScheduleKindInterval && schedule.Schedule.AnchorAt == nil {
		schedule.Schedule.AnchorAt = &now
	}
	if schedule.Enabled {
		next, err := timecalc.NextAfter(schedule, now.Add(-time.Nanosecond))
		if err != nil {
			return domain.Schedule{}, nil, err
		}
		schedule.NextRunAt = next
	}
	if err := s.store.CreateSchedule(ctx, schedule); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.Schedule{}, []domain.ValidationError{{Field: "name", Message: "must be unique"}}, nil
		}
		return domain.Schedule{}, nil, err
	}
	return schedule, nil, nil
}

func (s *Service) ListSchedules(ctx context.Context, enabled *bool, limit int, cursor string) ([]domain.ScheduleSummary, *string, error) {
	return s.store.ListSchedules(ctx, enabled, limit, cursor)
}

func (s *Service) GetSchedule(ctx context.Context, id string) (domain.Schedule, error) {
	return s.store.GetSchedule(ctx, id)
}

func (s *Service) ReplaceSchedule(ctx context.Context, id string, req domain.UpdateScheduleRequest) (domain.Schedule, []domain.ValidationError, error) {
	current, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return domain.Schedule{}, nil, err
	}
	req = withDefaults(req)
	if errs := domain.ValidateCreateSchedule(req); len(errs) > 0 {
		return domain.Schedule{}, errs, nil
	}
	current.Name = req.Name
	current.Enabled = req.Enabled
	current.Timezone = req.Timezone
	current.Schedule = req.Schedule
	current.Target = req.Target
	current.Policy = req.Policy
	current.Retry = req.Retry
	current.StartAt = req.StartAt
	current.EndAt = req.EndAt
	current.UpdatedAt = time.Now().UTC()
	if current.Enabled {
		next, err := timecalc.NextAfter(current, time.Now().UTC().Add(-time.Nanosecond))
		if err != nil {
			return domain.Schedule{}, nil, err
		}
		current.NextRunAt = next
	}
	if err := s.store.UpdateSchedule(ctx, current); err != nil {
		return domain.Schedule{}, nil, err
	}
	return current, nil, nil
}

func (s *Service) PatchSchedule(ctx context.Context, id string, patch domain.PatchScheduleRequest) (domain.Schedule, []domain.ValidationError, error) {
	current, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return domain.Schedule{}, nil, err
	}
	next := domain.ApplyPatch(current, patch)
	if errs := domain.ValidatePatch(current, patch); len(errs) > 0 {
		return domain.Schedule{}, errs, nil
	}
	next.UpdatedAt = time.Now().UTC()
	if next.Enabled {
		computed, err := timecalc.NextAfter(next, time.Now().UTC().Add(-time.Nanosecond))
		if err != nil {
			return domain.Schedule{}, nil, err
		}
		next.NextRunAt = computed
	}
	if err := s.store.UpdateSchedule(ctx, next); err != nil {
		return domain.Schedule{}, nil, err
	}
	return next, nil, nil
}

func (s *Service) DeleteSchedule(ctx context.Context, id string) error {
	return s.store.DeleteSchedule(ctx, id)
}

func (s *Service) PauseSchedule(ctx context.Context, id string) (domain.Schedule, error) {
	schedule, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return domain.Schedule{}, err
	}
	now := time.Now().UTC()
	schedule.Enabled = false
	schedule.PausedAt = &now
	schedule.UpdatedAt = now
	if err := s.store.UpdateSchedule(ctx, schedule); err != nil {
		return domain.Schedule{}, err
	}
	return schedule, nil
}

func (s *Service) ResumeSchedule(ctx context.Context, id string) (domain.Schedule, error) {
	schedule, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return domain.Schedule{}, err
	}
	now := time.Now().UTC()
	schedule.Enabled = true
	schedule.PausedAt = nil
	next, err := timecalc.NextAfter(schedule, now.Add(-time.Nanosecond))
	if err != nil {
		return domain.Schedule{}, err
	}
	schedule.NextRunAt = next
	schedule.UpdatedAt = now
	if err := s.store.UpdateSchedule(ctx, schedule); err != nil {
		return domain.Schedule{}, err
	}
	return schedule, nil
}

func (s *Service) TriggerSchedule(ctx context.Context, id, reason string) (domain.Run, error) {
	if err := domain.ValidateTriggerReason(reason); err != nil {
		return domain.Run{}, err
	}
	schedule, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return domain.Run{}, err
	}
	now := time.Now().UTC()
	runID := domain.NewID("run")
	run := domain.Run{
		ID:            runID,
		ScheduleID:    schedule.ID,
		OccurrenceKey: fmt.Sprintf("manual:%s", runID),
		NominalTime:   now,
		DueTime:       now,
		Status:        domain.RunPending,
		Attempt:       1,
		ResultJSON:    domain.MustJSON(map[string]any{"reason": reason}),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	return run, s.store.InsertRun(ctx, run)
}

func (s *Service) ListRuns(ctx context.Context, scheduleID *string, status *domain.RunStatus, limit int, cursor string) ([]domain.RunSummary, *string, error) {
	return s.store.ListRuns(ctx, scheduleID, status, limit, cursor)
}

func (s *Service) GetRun(ctx context.Context, id string) (domain.Run, error) {
	return s.store.GetRun(ctx, id)
}

func (s *Service) ListReceipts(ctx context.Context, runID string) ([]domain.Receipt, error) {
	return s.store.ListReceipts(ctx, runID)
}

func (s *Service) Health(context.Context) map[string]bool {
	return map[string]bool{"ok": true}
}

func (s *Service) Ready(ctx context.Context) map[string]any {
	storage := "ok"
	if err := s.store.Ping(ctx); err != nil {
		storage = "error"
	}
	return map[string]any{"ok": storage == "ok", "storage": storage}
}

func (s *Service) Status(ctx context.Context) (domain.StatusResponse, error) {
	resp := domain.StatusResponse{
		Service:   "timekeeper",
		Version:   s.cfg.Version,
		StartedAt: s.startedAt.Format(time.RFC3339),
	}
	resp.Database.Driver = "sqlite"
	resp.Database.Path = s.cfg.DatabasePath
	resp.Scheduler.LoopIntervalSeconds = int(s.cfg.SchedulerInterval / time.Second)
	if last := s.planner.LastTick(); !last.IsZero() {
		resp.Scheduler.LastTickAt = last.Format(time.RFC3339)
	}
	resp.Workers.Active = s.dispatcher.ActiveWorkers()
	return resp, nil
}

func (s *Service) Metrics(ctx context.Context) (string, error) {
	schedulesTotal, err := s.store.CountTable(ctx, "schedules")
	if err != nil {
		return "", err
	}
	schedulesEnabled, err := s.store.ScheduleEnabledCount(ctx)
	if err != nil {
		return "", err
	}
	runsTotal, err := s.store.CountTable(ctx, "schedule_runs")
	if err != nil {
		return "", err
	}
	runsRunning, err := s.store.CountStatus(ctx, "schedule_runs", "status", string(domain.RunRunning))
	if err != nil {
		return "", err
	}
	runsFailed, err := s.store.CountStatus(ctx, "schedule_runs", "status", string(domain.RunFailed))
	if err != nil {
		return "", err
	}
	runsSucceeded, err := s.store.CountStatus(ctx, "schedule_runs", "status", string(domain.RunSucceeded))
	if err != nil {
		return "", err
	}
	deadLetters, err := s.store.CountTable(ctx, "dead_letters")
	if err != nil {
		return "", err
	}
	leases, err := s.store.WorkerLeaseCount(ctx)
	if err != nil {
		return "", err
	}
	lastTick := int64(0)
	if tick := s.planner.LastTick(); !tick.IsZero() {
		lastTick = tick.Unix()
	}
	metrics := fmt.Sprintf(
		"schedules_total %d\nschedules_enabled_total %d\nruns_total %d\nruns_running %d\nruns_failed_total %d\nruns_succeeded_total %d\ndead_letters_total %d\nworker_leases_active %d\nscheduler_last_tick_unix %d\n",
		schedulesTotal,
		schedulesEnabled,
		runsTotal,
		runsRunning,
		runsFailed,
		runsSucceeded,
		deadLetters,
		leases,
		lastTick,
	)
	return metrics, nil
}

func withDefaults(req domain.CreateScheduleRequest) domain.CreateScheduleRequest {
	if req.Policy.TimeoutSeconds == 0 {
		req.Policy = domain.DefaultPolicy()
	}
	if req.Policy.Overlap == "" {
		req.Policy.Overlap = domain.DefaultPolicy().Overlap
	}
	if req.Policy.Misfire == "" {
		req.Policy.Misfire = domain.DefaultPolicy().Misfire
	}
	if req.Policy.TimeoutSeconds == 0 {
		req.Policy.TimeoutSeconds = domain.DefaultPolicy().TimeoutSeconds
	}
	if req.Policy.MaxConcurrency == 0 {
		req.Policy.MaxConcurrency = domain.DefaultPolicy().MaxConcurrency
	}
	if req.Retry.Strategy == "" {
		req.Retry = domain.DefaultRetryPolicy()
	}
	if req.Retry.InitialDelaySeconds == 0 {
		req.Retry.InitialDelaySeconds = domain.DefaultRetryPolicy().InitialDelaySeconds
	}
	if req.Retry.MaxDelaySeconds == 0 {
		req.Retry.MaxDelaySeconds = domain.DefaultRetryPolicy().MaxDelaySeconds
	}
	return req
}
