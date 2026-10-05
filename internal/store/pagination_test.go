package store

import (
	"context"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestSQLitePaginationDoesNotSkipBoundaryRecords(t *testing.T) {
	testPaginationBoundaries(t, openTestStore(t))
}

func TestPostgresPaginationDoesNotSkipBoundaryRecords(t *testing.T) {
	testPaginationBoundaries(t, openPostgresTestStore(t))
}

func testPaginationBoundaries(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	createdAt := time.Now().UTC().Add(-time.Hour)
	wantSchedules := map[string]bool{}
	wantRuns := map[string]bool{}
	// Equal timestamps exercise the ID tie-breaker as well as the date cursor.
	for index := range 5 {
		schedule := insertTestSchedule(t, st)
		schedule.CreatedAt = createdAt.Add(time.Duration(index/2) * time.Minute)
		if err := st.UpdateSchedule(ctx, schedule); err != nil {
			t.Fatal(err)
		}
		// UpdateSchedule deliberately preserves created_at; set a fixed fixture
		// timestamp directly to reproduce pages with identical creation times.
		if _, err := st.exec(ctx, `UPDATE schedules SET created_at = ? WHERE id = ?`, timeString(schedule.CreatedAt), schedule.ID); err != nil {
			t.Fatal(err)
		}
		wantSchedules[schedule.ID] = true
		run := insertTestRun(t, st, schedule.ID, domain.RunSucceeded, schedule.CreatedAt)
		wantRuns[run.ID] = true
	}
	for _, limit := range []int{1, 2, 3} {
		t.Run(string(rune('0'+limit)), func(t *testing.T) {
			seen := map[string]bool{}
			cursor := ""
			for range 10 {
				items, next, err := st.ListSchedules(ctx, nil, limit, cursor)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range items {
					if seen[item.ID] {
						t.Fatalf("duplicate schedule %s", item.ID)
					}
					seen[item.ID] = true
				}
				if next == nil {
					break
				}
				cursor = *next
			}
			if len(seen) != len(wantSchedules) {
				t.Fatalf("page limit%d skipped schedules: got%d want%d", limit, len(seen), len(wantSchedules))
			}
			seen = map[string]bool{}
			cursor = ""
			for range 10 {
				items, next, err := st.ListRuns(ctx, nil, nil, nil, limit, cursor)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range items {
					if seen[item.ID] {
						t.Fatalf("duplicate run %s", item.ID)
					}
					seen[item.ID] = true
				}
				if next == nil {
					break
				}
				cursor = *next
			}
			if len(seen) != len(wantRuns) {
				t.Fatalf("page limit%d skipped runs: got%d want%d", limit, len(seen), len(wantRuns))
			}
		})
	}
}
