package planner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/store"
	"github.com/justyn-clark/wakeplane/internal/timecalc"
)

type Planner struct {
	store        *store.Store
	logger       *slog.Logger
	misfireGrace time.Duration
	now          func() time.Time
	lastTickMu   sync.RWMutex
	lastTick     time.Time
}

// New uses one scheduler polling interval as the grace for skip misfires.
// Existing callers use the daemon's default five-second interval.
func New(st *store.Store, logger *slog.Logger, pollingInterval ...time.Duration) *Planner {
	grace := 5 * time.Second
	if len(pollingInterval) > 0 && pollingInterval[0] > 0 {
		grace = pollingInterval[0]
	}
	return &Planner{
		store:        st,
		logger:       logger,
		misfireGrace: grace,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (p *Planner) LastTick() time.Time {
	p.lastTickMu.RLock()
	defer p.lastTickMu.RUnlock()
	return p.lastTick
}

func (p *Planner) Tick(ctx context.Context) error {
	now := p.now()
	p.lastTickMu.Lock()
	p.lastTick = now
	p.lastTickMu.Unlock()
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
			// Polling normally discovers an occurrence just after its nominal
			// time. Skip only work older than one polling interval, including
			// after a restart; no in-memory tick history changes this decision.
			status := domain.RunPending
			var errText *string
			if now.Sub(nominal) > p.misfireGrace {
				status = domain.RunSkipped
				errText = stringPtr("skipped due to misfire policy")
			}
			if err := p.insertOccurrence(ctx, schedule, nominal, status, errText); err != nil && err != store.ErrAlreadyExists {
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
