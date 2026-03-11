package planner

import (
	"context"
	"log/slog"
	"time"

	"github.com/justyn-clark/timekeeper/internal/domain"
	"github.com/justyn-clark/timekeeper/internal/store"
	"github.com/justyn-clark/timekeeper/internal/timecalc"
)

type Planner struct {
	store    *store.Store
	logger   *slog.Logger
	now      func() time.Time
	lastTick time.Time
}

func New(st *store.Store, logger *slog.Logger) *Planner {
	return &Planner{
		store:  st,
		logger: logger,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (p *Planner) LastTick() time.Time {
	return p.lastTick
}

func (p *Planner) Tick(ctx context.Context) error {
	now := p.now()
	p.lastTick = now
	schedules, err := p.store.ListAllSchedules(ctx)
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		if err := p.materializeSchedule(ctx, schedule, now); err != nil {
			p.logger.Error("planner tick failed for schedule", "schedule_id", schedule.ID, "error", err)
		}
	}
	return nil
}

func (p *Planner) materializeSchedule(ctx context.Context, schedule domain.Schedule, now time.Time) error {
	if !schedule.Enabled || schedule.PausedAt != nil {
		return nil
	}
	if schedule.NextRunAt == nil {
		next, err := timecalc.NextAfter(schedule, now.Add(-time.Nanosecond))
		if err != nil {
			return err
		}
		schedule.NextRunAt = next
		return p.store.UpdateSchedule(ctx, schedule)
	}
	if schedule.NextRunAt.After(now) {
		return nil
	}

	var due []time.Time
	next := schedule.NextRunAt
	for next != nil && !next.After(now) {
		due = append(due, next.UTC())
		computed, err := timecalc.NextAfter(schedule, next.UTC())
		if err != nil {
			return err
		}
		next = computed
	}

	switch schedule.Policy.Misfire {
	case domain.MisfireSkip:
		for _, nominal := range due {
			if err := p.insertOccurrence(ctx, schedule, nominal, domain.RunSkipped, stringPtr("skipped due to misfire policy")); err != nil && err != store.ErrAlreadyExists {
				return err
			}
		}
	case domain.MisfireRunOnceIfLate:
		for i, nominal := range due {
			status := domain.RunPending
			var errText *string
			if i < len(due)-1 {
				status = domain.RunSkipped
				errText = stringPtr("skipped due to run_once_if_late policy")
			}
			if err := p.insertOccurrence(ctx, schedule, nominal, status, errText); err != nil && err != store.ErrAlreadyExists {
				return err
			}
		}
	case domain.MisfireCatchUp:
		for _, nominal := range due {
			if err := p.insertOccurrence(ctx, schedule, nominal, domain.RunPending, nil); err != nil && err != store.ErrAlreadyExists {
				return err
			}
		}
	}

	schedule.NextRunAt = next
	schedule.UpdatedAt = now
	return p.store.UpdateSchedule(ctx, schedule)
}

func (p *Planner) insertOccurrence(ctx context.Context, schedule domain.Schedule, nominal time.Time, status domain.RunStatus, errText *string) error {
	now := p.now()
	run := domain.Run{
		ID:            domain.NewID("run"),
		ScheduleID:    schedule.ID,
		OccurrenceKey: domain.OccurrenceKey(schedule.ID, nominal),
		NominalTime:   nominal.UTC(),
		DueTime:       nominal.UTC(),
		Status:        status,
		Attempt:       1,
		ErrorText:     errText,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if status == domain.RunSkipped {
		run.FinishedAt = &now
	}
	return p.store.InsertRun(ctx, run)
}

func stringPtr(v string) *string { return &v }
