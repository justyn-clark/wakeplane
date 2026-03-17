package planner

import (
	"context"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/logging"
	"github.com/justyn-clark/wakeplane/internal/store"
)

func TestPlannerMisfirePolicies(t *testing.T) {
	testCases := []struct {
		name         string
		policy       domain.MisfirePolicy
		wantStatuses []domain.RunStatus
	}{
		{name: "skip", policy: domain.MisfireSkip, wantStatuses: []domain.RunStatus{domain.RunSkipped, domain.RunSkipped, domain.RunSkipped}},
		{name: "run_once_if_late", policy: domain.MisfireRunOnceIfLate, wantStatuses: []domain.RunStatus{domain.RunSkipped, domain.RunSkipped, domain.RunPending}},
		{name: "catch_up", policy: domain.MisfireCatchUp, wantStatuses: []domain.RunStatus{domain.RunPending, domain.RunPending, domain.RunPending}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			base := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
			schedule := testSchedule(base, tc.policy)
			schedule.NextRunAt = ptrTime(base.Add(1 * time.Minute))
			if err := st.CreateSchedule(context.Background(), schedule); err != nil {
				t.Fatalf("CreateSchedule returned error: %v", err)
			}

			pl := New(st, logging.New())
			pl.now = func() time.Time { return base.Add(3*time.Minute + 30*time.Second) }
			if err := pl.Tick(context.Background()); err != nil {
				t.Fatalf("Tick returned error: %v", err)
			}

			scheduleID := schedule.ID
			items, _, err := st.ListRuns(context.Background(), &scheduleID, nil, 10, "")
			if err != nil {
				t.Fatalf("ListRuns returned error: %v", err)
			}
			if len(items) != len(tc.wantStatuses) {
				t.Fatalf("expected %d runs, got %d", len(tc.wantStatuses), len(items))
			}
			// Sort by occurrence_key (embeds nominal time in RFC3339) for stable chronological order.
			sort.Slice(items, func(a, b int) bool {
				return items[a].OccurrenceKey < items[b].OccurrenceKey
			})
			for i, want := range tc.wantStatuses {
				if items[i].Status != want {
					t.Fatalf("run %d (key=%s) expected status %s, got %s", i, items[i].OccurrenceKey, want, items[i].Status)
				}
			}
		})
	}
}

func TestPlannerOnceScheduleMaterializesOnlyOnce(t *testing.T) {
	st := newTestStore(t)
	at := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	schedule := domain.Schedule{
		ID:        domain.NewID("sch"),
		Name:      "once-only",
		Enabled:   true,
		Timezone:  "UTC",
		Schedule:  domain.ScheduleSpec{Kind: domain.ScheduleKindOnce, At: &at},
		Target:    domain.TargetSpec{Kind: domain.TargetKindShell, Command: "/bin/echo"},
		Policy:    domain.DefaultPolicy(),
		Retry:     domain.DefaultRetryPolicy(),
		NextRunAt: &at,
		CreatedAt: at.Add(-time.Hour),
		UpdatedAt: at.Add(-time.Hour),
	}
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}

	pl := New(st, logging.New())
	pl.now = func() time.Time { return at.Add(time.Minute) }
	if err := pl.Tick(context.Background()); err != nil {
		t.Fatalf("first Tick returned error: %v", err)
	}
	if err := pl.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick returned error: %v", err)
	}

	scheduleID := schedule.ID
	items, _, err := st.ListRuns(context.Background(), &scheduleID, nil, 10, "")
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one run, got %d", len(items))
	}
	got, err := st.GetSchedule(context.Background(), schedule.ID)
	if err != nil {
		t.Fatalf("GetSchedule returned error: %v", err)
	}
	if got.NextRunAt != nil {
		t.Fatalf("expected once schedule next_run_at to be nil, got %v", got.NextRunAt)
	}
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wakeplane.db"))
	if err != nil {
		t.Fatalf("store.Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	return st
}

func testSchedule(base time.Time, misfire domain.MisfirePolicy) domain.Schedule {
	anchor := base
	return domain.Schedule{
		ID:       domain.NewID("sch"),
		Name:     string(misfire) + "-schedule",
		Enabled:  true,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{
			Kind:         domain.ScheduleKindInterval,
			EverySeconds: 60,
			AnchorAt:     &anchor,
		},
		Target: domain.TargetSpec{Kind: domain.TargetKindShell, Command: "/bin/echo"},
		Policy: domain.Policy{
			Overlap:        domain.OverlapAllow,
			Misfire:        misfire,
			TimeoutSeconds: 60,
			MaxConcurrency: 1,
		},
		Retry:     domain.DefaultRetryPolicy(),
		CreatedAt: base,
		UpdatedAt: base,
	}
}

func ptrTime(v time.Time) *time.Time {
	return &v
}
