package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestScheduleEditsEnablePausedAutomationAndClearDisabledTiming(t *testing.T) {
	for _, mutation := range []string{"replace", "patch"} {
		t.Run(mutation, func(t *testing.T) {
			ctx := context.Background()
			s, err := New(ctx, config.Config{DatabasePath: filepath.Join(t.TempDir(), "edit.db")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			anchor := time.Now().UTC().Add(-time.Hour)
			req := domain.CreateScheduleRequest{Name: "edited automation", Enabled: true, Timezone: "UTC", Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60, AnchorAt: &anchor}, Target: domain.TargetSpec{Kind: domain.TargetKindWorkflow, WorkflowID: "sync.customers"}}
			schedule, validation, err := s.CreateSchedule(ctx, req)
			if err != nil || len(validation) > 0 {
				t.Fatalf("create: %v %+v", err, validation)
			}
			paused, err := s.PauseSchedule(ctx, schedule.ID)
			if err != nil || paused.PausedAt == nil {
				t.Fatalf("pause: %v %+v", err, paused)
			}
			edit := func(enabled bool) domain.Schedule {
				var edited domain.Schedule
				var validation []domain.ValidationError
				var err error
				if mutation == "replace" {
					req.Enabled = enabled
					edited, validation, err = s.ReplaceSchedule(ctx, schedule.ID, req)
				} else {
					edited, validation, err = s.PatchSchedule(ctx, schedule.ID, domain.PatchScheduleRequest{Enabled: &enabled})
				}
				if err != nil || len(validation) > 0 {
					t.Fatalf("%s: %v %+v", mutation, err, validation)
				}
				return edited
			}
			enabled := edit(true)
			if !enabled.Enabled || enabled.PausedAt != nil || enabled.NextRunAt == nil {
				t.Fatalf("editing to enable left automation unschedulable: %+v", enabled)
			}
			// Exercise the planner, not only the API representation: re-enabling
			// through an edit must create actual durable scheduled occurrences.
			due := time.Now().UTC().Add(-time.Second)
			enabled.NextRunAt = &due
			if err := s.store.UpdateSchedule(ctx, enabled); err != nil {
				t.Fatal(err)
			}
			if err := s.planner.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			runs, _, err := s.ListRuns(ctx, &schedule.ID, nil, nil, 50, "")
			if err != nil || len(runs) == 0 {
				t.Fatalf("enabled edit did not produce scheduled work: %v", err)
			}
			disabled := edit(false)
			if disabled.Enabled || disabled.PausedAt == nil || disabled.NextRunAt != nil {
				t.Fatalf("disabled edit retained active runtime state: %+v", disabled)
			}
		})
	}
}

func TestStatusWhileSchedulerRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, err := New(ctx, config.Config{DatabasePath: filepath.Join(t.TempDir(), "status.db"), SchedulerInterval: time.Millisecond, DispatcherInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		_ = s.Close()
	})
	for range 100 {
		if _, err := s.Status(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
