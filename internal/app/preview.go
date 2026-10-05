package app

import (
	"context"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/timecalc"
)

// PreviewSchedule uses the same defaults, validation and time calculator as
// creation, without writing a schedule or dispatching any work.
func (s *Service) PreviewSchedule(ctx context.Context, req domain.CreateScheduleRequest) ([]time.Time, []domain.ValidationError, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	req = withDefaults(req)
	if errs := domain.ValidateCreateSchedule(req); len(errs) > 0 {
		return nil, errs, nil
	}
	now := time.Now().UTC()
	schedule := domain.Schedule{Timezone: req.Timezone, Schedule: req.Schedule, StartAt: req.StartAt, EndAt: req.EndAt, CreatedAt: now}
	if schedule.Schedule.Kind == domain.ScheduleKindInterval && schedule.Schedule.AnchorAt == nil {
		schedule.Schedule.AnchorAt = &now
	}
	base := now.Add(-time.Nanosecond)
	// Cron StartAt is a lower bound on valid cadence slots, not an arbitrary
	// replacement slot. Keep the preview on that cadence when a window is set.
	if req.StartAt != nil && req.StartAt.After(now) && req.Schedule.Kind == domain.ScheduleKindCron {
		base = req.StartAt.Add(-time.Nanosecond)
	}
	runs := make([]time.Time, 0, 5)
	for range 5 {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		next, err := timecalc.NextAfter(schedule, base)
		if err != nil {
			return nil, nil, err
		}
		if next == nil || next.IsZero() || !next.After(base) {
			break
		}
		runs = append(runs, *next)
		base = *next
	}
	return runs, nil, nil
}

func (s *Service) Version() string { return s.cfg.Version }
