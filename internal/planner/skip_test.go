package planner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/logging"
	"github.com/justyn-clark/wakeplane/internal/store"
)

func TestSkipNormalPollingAndBoundary(t *testing.T) {
	nominal := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	for _, kind := range []domain.ScheduleKind{domain.ScheduleKindOnce, domain.ScheduleKindInterval, domain.ScheduleKindCron} {
		for _, tc := range []struct {
			name   string
			delay  time.Duration
			status domain.RunStatus
		}{
			{"exact_slot", 0, domain.RunPending},
			{"ordinary_poll", time.Second, domain.RunPending},
			{"grace_boundary", 5 * time.Second, domain.RunPending},
			{"past_grace", 5*time.Second + time.Nanosecond, domain.RunSkipped},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				schedule := testSchedule(nominal, domain.MisfireSkip)
				schedule.Timezone = "America/Los_Angeles"
				switch kind {
				case domain.ScheduleKindOnce:
					schedule.Schedule = domain.ScheduleSpec{Kind: kind, At: &nominal}
				case domain.ScheduleKindCron:
					schedule.Schedule = domain.ScheduleSpec{Kind: kind, Expr: "0 9 * * 1-5"}
				}
				// EndAt is an inclusive nominal-slot bound, not a deadline.
				schedule.StartAt, schedule.EndAt, schedule.NextRunAt = &nominal, &nominal, &nominal
				if err := st.CreateSchedule(context.Background(), schedule); err != nil {
					t.Fatal(err)
				}
				pl := New(st, logging.New())
				pl.now = func() time.Time { return nominal.Add(tc.delay) }
				for i := 0; i < 2; i++ {
					if i == 1 {
						pl.now = func() time.Time { return nominal.Add(tc.delay).Add(time.Nanosecond) }
					}
					if err := pl.Tick(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				items := scheduleRuns(t, st, schedule.ID)
				if len(items) != 1 {
					t.Fatalf("expected one durable occurrence, got %d", len(items))
				}
				run := items[0]
				if run.Status != tc.status || !run.NominalTime.Equal(nominal) || !run.DueTime.Equal(nominal) || run.OccurrenceKey != domain.OccurrenceKey(schedule.ID, nominal) {
					t.Fatalf("unexpected occurrence: %+v", run)
				}
				if run.Attempt != 1 || (run.ErrorText != nil) != (tc.status == domain.RunSkipped) || (run.FinishedAt != nil) != (tc.status == domain.RunSkipped) {
					t.Fatalf("unexpected materialization metadata: %+v", run)
				}
				updated, err := st.GetSchedule(context.Background(), schedule.ID)
				if err != nil || updated.NextRunAt != nil {
					t.Fatalf("finite schedule did not exhaust: %+v, %v", updated, err)
				}
			})
		}
	}
}

func TestSkipUsesConfiguredPollingInterval(t *testing.T) {
	nominal := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		interval time.Duration
		delay    time.Duration
		want     domain.RunStatus
	}{
		{"short_boundary", time.Second, time.Second, domain.RunPending},
		{"short_expired", time.Second, time.Second + time.Nanosecond, domain.RunSkipped},
		{"long_boundary", 30 * time.Second, 30 * time.Second, domain.RunPending},
		{"long_expired", 30 * time.Second, 30*time.Second + time.Nanosecond, domain.RunSkipped},
		{"zero_defaults", 0, 5 * time.Second, domain.RunPending},
		{"negative_defaults", -time.Second, 5 * time.Second, domain.RunPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			schedule := testSchedule(nominal, domain.MisfireSkip)
			schedule.NextRunAt = &nominal
			if err := st.CreateSchedule(context.Background(), schedule); err != nil {
				t.Fatal(err)
			}
			pl := New(st, logging.New(), tc.interval)
			pl.now = func() time.Time { return nominal.Add(tc.delay) }
			if err := pl.Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			items := scheduleRuns(t, st, schedule.ID)
			if len(items) != 1 || items[0].Status != tc.want {
				t.Fatalf("unexpected runs: %+v", items)
			}
		})
	}
}

func TestSkipDoesNotMaterializeFutureOccurrence(t *testing.T) {
	st := newTestStore(t)
	nominal := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	schedule := testSchedule(nominal, domain.MisfireSkip)
	schedule.NextRunAt = &nominal
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatal(err)
	}
	pl := New(st, logging.New())
	pl.now = func() time.Time { return nominal.Add(-time.Nanosecond) }
	if err := pl.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if items := scheduleRuns(t, st, schedule.ID); len(items) != 0 {
		t.Fatalf("future occurrence materialized: %+v", items)
	}
}

func TestSkipBacklogAcrossReopenPreservesOccurrenceIdentities(t *testing.T) {
	for _, everySeconds := range []int{1, 60} {
		t.Run((time.Duration(everySeconds) * time.Second).String(), func(t *testing.T) {
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "wakeplane.db")
			st, err := store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			base := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
			schedule := testSchedule(base, domain.MisfireSkip)
			schedule.Schedule.EverySeconds = everySeconds
			first := base.Add(time.Duration(everySeconds) * time.Second)
			schedule.NextRunAt = &first
			if err := st.CreateSchedule(ctx, schedule); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err := st.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			now := base.Add(3*time.Minute + 5*time.Second)
			if everySeconds == 1 {
				now = base.Add(8 * time.Second)
			}
			pl := New(st, logging.New())
			pl.now = func() time.Time { return now }
			if err := pl.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			baseline := scheduleRuns(t, st, schedule.ID)
			wantCount, wantPending := 3, 1
			if everySeconds == 1 {
				wantCount, wantPending = 8, 6
			}
			if len(baseline) != wantCount {
				t.Fatalf("expected %d runs, got %d", wantCount, len(baseline))
			}
			pending := 0
			for _, run := range baseline {
				want := domain.RunSkipped
				if now.Sub(run.NominalTime) <= 5*time.Second {
					want = domain.RunPending
					pending++
				}
				if run.Status != want || run.OccurrenceKey != domain.OccurrenceKey(schedule.ID, run.NominalTime) {
					t.Fatalf("unexpected backlog occurrence: %+v", run)
				}
			}
			if pending != wantPending {
				t.Fatalf("expected %d current runs, got %d", wantPending, pending)
			}
			updated, err := st.GetSchedule(ctx, schedule.ID)
			if err != nil || updated.NextRunAt == nil || !updated.NextRunAt.After(now) {
				t.Fatalf("next slot did not advance: %+v, %v", updated, err)
			}
			// Re-read an old checkpoint after a restart/partial update. Durable
			// occurrence identities must prevent replay or status rewriting.
			updated.NextRunAt = &first
			if err := st.UpdateSchedule(ctx, updated); err != nil {
				t.Fatal(err)
			}
			pl = New(st, logging.New())
			pl.now = func() time.Time { return now.Add(time.Nanosecond) }
			for i := 0; i < 2; i++ {
				if err := pl.Tick(ctx); err != nil {
					t.Fatal(err)
				}
			}
			replayed := scheduleRuns(t, st, schedule.ID)
			if len(replayed) != len(baseline) {
				t.Fatalf("occurrence replay duplicated runs: %d -> %d", len(baseline), len(replayed))
			}
			for i, run := range replayed {
				if run.ID != baseline[i].ID || run.Status != baseline[i].Status || run.Attempt != baseline[i].Attempt {
					t.Fatalf("existing occurrence changed: %+v -> %+v", baseline[i], run)
				}
			}
		})
	}
}

func TestPostgresSkipBacklogGraceAndReplay(t *testing.T) {
	databaseURL := os.Getenv("WAKEPLANE_POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("set WAKEPLANE_POSTGRES_TEST_URL to run Postgres planner tests")
	}
	ctx := context.Background()
	st, err := store.OpenPostgres(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	schedule := testSchedule(base, domain.MisfireSkip)
	first := base.Add(time.Minute)
	schedule.NextRunAt = &first
	if err := st.CreateSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.DeleteSchedule(ctx, schedule.ID); err != nil {
			t.Errorf("delete own test schedule: %v", err)
		}
	}()
	pl := New(st, logging.New(), 2*time.Second)
	now := base.Add(3*time.Minute + 2*time.Second)
	pl.now = func() time.Time { return now }
	// Plan only this unique test schedule; never alter other test records.
	if err := pl.materializeSchedule(ctx, schedule, now); err != nil {
		t.Fatal(err)
	}
	baseline := scheduleRuns(t, st, schedule.ID)
	if len(baseline) != 3 {
		t.Fatalf("expected three durable occurrences, got %d", len(baseline))
	}
	for i, run := range baseline {
		want := domain.RunSkipped
		if i == 2 {
			want = domain.RunPending
		}
		if run.Status != want || run.Attempt != 1 || run.OccurrenceKey != domain.OccurrenceKey(schedule.ID, first.Add(time.Duration(i)*time.Minute)) {
			t.Fatalf("unexpected Postgres occurrence: %+v", run)
		}
	}
	updated, err := st.GetSchedule(ctx, schedule.ID)
	if err != nil || updated.NextRunAt == nil || !updated.NextRunAt.Equal(base.Add(4*time.Minute)) {
		t.Fatalf("unexpected next nominal slot: %+v, %v", updated, err)
	}
	// Replaying an old checkpoint one nanosecond after the grace expires
	// must neither duplicate the accepted occurrence nor rewrite its status.
	updated.NextRunAt = &first
	if err := st.UpdateSchedule(ctx, updated); err != nil {
		t.Fatal(err)
	}
	if err := pl.materializeSchedule(ctx, updated, now.Add(time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	replayed := scheduleRuns(t, st, schedule.ID)
	if len(replayed) != len(baseline) {
		t.Fatalf("Postgres replay duplicated runs: %d -> %d", len(baseline), len(replayed))
	}
	for i, run := range replayed {
		if run.ID != baseline[i].ID || run.Status != baseline[i].Status || run.Attempt != baseline[i].Attempt {
			t.Fatalf("Postgres replay rewrote occurrence: %+v -> %+v", baseline[i], run)
		}
	}
}

func scheduleRuns(t *testing.T, st *store.Store, scheduleID string) []domain.RunSummary {
	t.Helper()
	items, _, err := st.ListRuns(context.Background(), &scheduleID, nil, nil, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(items, func(a, b int) bool { return items[a].NominalTime.Before(items[b].NominalTime) })
	return items
}
