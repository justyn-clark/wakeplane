package store

import (
	"context"
	"strings"
	"testing"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestSQLiteRunHistoryIndexSupportsNormalizedOrder(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	schedule := insertTestSchedule(t, st)
	run := insertTestRun(t, st, schedule.ID, domain.RunSucceeded, mustParseTime(legacyInstants[1]))
	// Exercise an existing database without the index, then repeated migrations.
	if _, err := st.exec(ctx, "DROP INDEX IF EXISTS idx_schedule_runs_created_normalized_id"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := st.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	items, _, err := st.ListRuns(ctx, nil, nil, nil, 30, "")
	if err != nil || len(items) != 1 || items[0].ID != run.ID {
		t.Fatalf("migration changed run history: items=%v error=%v", items, err)
	}
	rows, err := st.query(ctx, `EXPLAIN QUERY PLAN
		SELECT sr.id FROM schedule_runs sr JOIN schedules s ON s.id = sr.schedule_id
		ORDER BY `+timestampSQL("sr.created_at")+` DESC, sr.id DESC LIMIT 31`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	details := strings.Join(plan, "\n")
	if !strings.Contains(details, "idx_schedule_runs_created_normalized_id") ||
		strings.Contains(details, "TEMP B-TREE") {
		t.Fatalf("run history must use its normalized-order index without a temporary sort:\n%s", details)
	}
}
