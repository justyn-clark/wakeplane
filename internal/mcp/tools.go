package mcp

import (
	"context"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

type emptyInput struct{}

type scheduleInput struct {
	Name     string              `json:"name" jsonschema:"Human-readable name for this automation"`
	Enabled  bool                `json:"enabled,omitempty" jsonschema:"Enable recurring execution immediately; defaults to false for a safe draft"`
	Timezone string              `json:"timezone" jsonschema:"Explicit IANA timezone such as America/Los_Angeles or UTC"`
	Schedule domain.ScheduleSpec `json:"schedule" jsonschema:"Typed cron, interval, or once schedule; cron has five fields"`
	Target   domain.TargetSpec   `json:"target" jsonschema:"Typed existing HTTP, shell, or registered workflow target; HTTP jobs use http_job settings"`
	Policy   *domain.Policy      `json:"policy,omitempty" jsonschema:"Optional execution policy; omitted uses the application defaults"`
	Retry    *domain.RetryPolicy `json:"retry,omitempty" jsonschema:"Optional retry policy; omitted uses the application defaults"`
	StartAt  *time.Time          `json:"start_at,omitempty"`
	EndAt    *time.Time          `json:"end_at,omitempty"`
}

func (in scheduleInput) request() domain.CreateScheduleRequest {
	req := domain.CreateScheduleRequest{
		Name: in.Name, Enabled: in.Enabled, Timezone: in.Timezone,
		Schedule: in.Schedule, Target: in.Target, StartAt: in.StartAt, EndAt: in.EndAt,
	}
	if in.Policy != nil {
		req.Policy = *in.Policy
	}
	if in.Retry != nil {
		req.Retry = *in.Retry
	}
	return req
}

type replaceInput struct {
	ScheduleID string        `json:"schedule_id"`
	Schedule   scheduleInput `json:"schedule" jsonschema:"Complete replacement, not a partial patch; fields omitted here return to defaults"`
}

type scheduleIDInput struct {
	ScheduleID string `json:"schedule_id"`
}

type runIDInput struct {
	RunID string `json:"run_id"`
}

type triggerInput struct {
	ScheduleID string `json:"schedule_id"`
	Reason     string `json:"reason" jsonschema:"Explicit human-authorized reason for creating a manual run"`
}

type eventInput struct {
	ScheduleID string              `json:"schedule_id"`
	Event      domain.TriggerEvent `json:"event" jsonschema:"Stable source and unique event ID identify one occurrence; reuse both and identical data on delivery retries"`
}

type listSchedulesInput struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Page size from 1 to 100; omitted defaults to 50"`
	Cursor  string `json:"cursor,omitempty" jsonschema:"Opaque next_cursor from the previous page"`
}

type listRunsInput struct {
	ScheduleID *string            `json:"schedule_id,omitempty"`
	Status     *domain.RunStatus  `json:"status,omitempty"`
	TargetKind *domain.TargetKind `json:"target_kind,omitempty"`
	Limit      int                `json:"limit,omitempty" jsonschema:"Page size from 1 to 100; omitted defaults to 50"`
	Cursor     string             `json:"cursor,omitempty" jsonschema:"Opaque next_cursor from the previous page"`
}

func registerTools(server *sdk.Server, service Service) {
	addTool(server, "wakeplane_status", "Inspect Wakeplane", "Inspect service, scheduler, worker, retention, and security status.", true, false, false, func(ctx context.Context, _ emptyInput) (any, error) {
		return service.Status(ctx)
	})
	addTool(server, "wakeplane_list_schedules", "List automations", "List schedule summaries with optional enabled filter and bounded pagination.", true, false, false, func(ctx context.Context, in listSchedulesInput) (any, error) {
		limit, err := validatePage(in.Limit, in.Cursor)
		if err != nil {
			return nil, err
		}
		items, next, err := service.ListSchedules(ctx, in.Enabled, limit, in.Cursor)
		if items == nil {
			items = []domain.ScheduleSummary{}
		}
		return domain.ListResponse[domain.ScheduleSummary]{Items: items, NextCursor: next}, err
	})
	addTool(server, "wakeplane_get_schedule", "Inspect automation", "Read a schedule's complete typed target, timing, and execution policy.", true, false, false, func(ctx context.Context, in scheduleIDInput) (any, error) {
		if err := requireID(in.ScheduleID); err != nil {
			return nil, err
		}
		return service.GetSchedule(ctx, in.ScheduleID)
	})
	addTool(server, "wakeplane_create_schedule", "Create automation", "Create a validated schedule with an explicit timezone. Disabled by default. Requires user authorization. This operation is not idempotent; inspect schedules before retrying an uncertain response.", false, false, true, func(ctx context.Context, in scheduleInput) (any, error) {
		schedule, validation, err := service.CreateSchedule(ctx, in.request())
		return scheduleResult(schedule, validation, err)
	})
	addTool(server, "wakeplane_update_schedule", "Replace automation", "Replace a schedule's full definition using its ID. Read its current definition first; omitted fields reset to defaults. Requires user authorization and may change future work.", false, true, true, func(ctx context.Context, in replaceInput) (any, error) {
		if err := requireID(in.ScheduleID); err != nil {
			return nil, err
		}
		schedule, validation, err := service.ReplaceSchedule(ctx, in.ScheduleID, in.Schedule.request())
		return scheduleResult(schedule, validation, err)
	})
	addTool(server, "wakeplane_pause_schedule", "Pause automation", "Disable future occurrences without deleting the schedule or its run ledger. Already queued or running work is not cancelled.", false, true, false, func(ctx context.Context, in scheduleIDInput) (any, error) {
		if err := requireID(in.ScheduleID); err != nil {
			return nil, err
		}
		return service.PauseSchedule(ctx, in.ScheduleID)
	})
	addTool(server, "wakeplane_resume_schedule", "Enable automation", "Enable future occurrences using the stored timezone and policy. Requires user authorization; this may cause external work to run.", false, true, true, func(ctx context.Context, in scheduleIDInput) (any, error) {
		if err := requireID(in.ScheduleID); err != nil {
			return nil, err
		}
		return service.ResumeSchedule(ctx, in.ScheduleID)
	})
	addTool(server, "wakeplane_trigger_schedule", "Queue manual run", "Record one durable manual run for an existing schedule, including a reason. The dispatcher applies policy before execution. Requires user authorization, also for paused schedules. Each call creates a new occurrence; do not blindly retry.", false, false, true, func(ctx context.Context, in triggerInput) (any, error) {
		if err := requireID(in.ScheduleID); err != nil {
			return nil, err
		}
		return service.TriggerSchedule(ctx, in.ScheduleID, in.Reason)
	})
	addTool(server, "wakeplane_list_runs", "List execution history", "List durable run summaries with optional schedule, status, and target filters and bounded pagination.", true, false, false, func(ctx context.Context, in listRunsInput) (any, error) {
		limit, err := validatePage(in.Limit, in.Cursor)
		if err != nil {
			return nil, err
		}
		if in.ScheduleID != nil {
			if err := requireID(*in.ScheduleID); err != nil {
				return nil, err
			}
		}
		if in.Status != nil && !validRunStatus(*in.Status) {
			return nil, domain.NewBadRequestError("invalid run status")
		}
		if in.TargetKind != nil && !validTargetKind(*in.TargetKind) {
			return nil, domain.NewBadRequestError("invalid target kind")
		}
		items, next, err := service.ListRuns(ctx, in.ScheduleID, in.Status, in.TargetKind, limit, in.Cursor)
		if items == nil {
			items = []domain.RunSummary{}
		}
		return domain.ListResponse[domain.RunSummary]{Items: items, NextCursor: next}, err
	})
	addTool(server, "wakeplane_get_run", "Inspect execution outcome", "Read a durable run, attempts, receipts, and external job state. Pending or running means unfinished; verify final status and artifacts before reporting success.", true, false, false, func(ctx context.Context, in runIDInput) (any, error) {
		if err := requireID(in.RunID); err != nil {
			return nil, err
		}
		return service.GetRun(ctx, in.RunID)
	})
	if preview, ok := service.(previewService); ok {
		addTool(server, "wakeplane_preview_schedule", "Preview automation timing", "Validate a draft and preview upcoming occurrence times without saving or executing work. Supply the same explicit timezone, typed target, and policy as creation.", true, false, false, func(ctx context.Context, in scheduleInput) (any, error) {
			times, validation, err := preview.PreviewSchedule(ctx, in.request())
			if err == nil && len(validation) > 0 {
				err = domain.NewValidationError(validation)
			}
			return struct {
				Times []time.Time `json:"times"`
			}{Times: times}, err
		})
	}
	if events, ok := service.(eventService); ok {
		addTool(server, "wakeplane_send_event", "Deliver automation event", "Deliver an authorized external event to an enabled schedule. Stable event ID and source deduplicate identical retries into the original durable run; changed payloads conflict. Disabled schedules reject events. The dispatcher applies execution policy.", false, false, true, func(ctx context.Context, in eventInput) (any, error) {
			if err := requireID(in.ScheduleID); err != nil {
				return nil, err
			}
			run, created, err := events.TriggerEvent(ctx, in.ScheduleID, in.Event)
			return struct {
				Run     domain.Run `json:"run"`
				Created bool       `json:"created"`
			}{Run: run, Created: created}, err
		}, true)
	}
}

func scheduleResult(schedule domain.Schedule, validation []domain.ValidationError, err error) (any, error) {
	if err == nil && len(validation) > 0 {
		err = domain.NewValidationError(validation)
	}
	return schedule, err
}

func requireID(id string) error {
	if strings.TrimSpace(id) == "" || len(id) > 256 {
		return domain.NewBadRequestError("ID must be non-empty and at most 256 bytes")
	}
	return nil
}

func validatePage(limit int, cursor string) (int, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return 0, domain.NewBadRequestError("limit must be between 1 and 100")
	}
	if len(cursor) > 2048 {
		return 0, domain.NewBadRequestError("invalid cursor")
	}
	if cursor != "" {
		if _, _, err := domain.DecodeCursor(cursor); err != nil {
			return 0, domain.NewBadRequestError("invalid cursor")
		}
	}
	return limit, nil
}

func validRunStatus(status domain.RunStatus) bool {
	switch status {
	case domain.RunPending, domain.RunClaimed, domain.RunRunning, domain.RunSucceeded, domain.RunFailed, domain.RunRetryScheduled, domain.RunDeadLettered, domain.RunCancelled, domain.RunSkipped:
		return true
	}
	return false
}

func validTargetKind(kind domain.TargetKind) bool {
	switch kind {
	case domain.TargetKindHTTP, domain.TargetKindShell, domain.TargetKindWorkflow:
		return true
	}
	return false
}
