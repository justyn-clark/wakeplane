package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

var legacyInstants = []string{
	"2026-10-09T22:10:00Z",
	"2026-10-09T22:10:00.1Z",
	"2026-10-09T22:10:00.100000000Z",
	"2026-10-09T22:10:00.100000001Z",
	"2026-10-09T22:10:00.11Z",
}

func TestSQLiteLegacyTimestampQueries(t *testing.T) { testLegacyTimestampQueries(t, openTestStore(t)) }
func TestPostgresLegacyTimestampQueries(t *testing.T) {
	testLegacyTimestampQueries(t, openPostgresTestStore(t))
}

func testLegacyTimestampQueries(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	now := mustParseTime(legacyInstants[1])
	var schedules []domain.Schedule
	var pending []domain.Run
	var retries []domain.Run
	for _, raw := range legacyInstants {
		schedule := insertTestSchedule(t, st)
		if _, err := st.exec(ctx, `UPDATE schedules SET created_at = ? WHERE id = ?`, raw, schedule.ID); err != nil {
			t.Fatal(err)
		}
		schedule.CreatedAt = mustParseTime(raw)
		schedules = append(schedules, schedule)
		for _, status := range []domain.RunStatus{domain.RunPending, domain.RunRetryScheduled} {
			run := insertTestRun(t, st, schedule.ID, status, now)
			if _, err := st.exec(ctx, `UPDATE schedule_runs SET created_at = ?, due_time = ?, retry_available_at = ? WHERE id = ?`, raw, raw, raw, run.ID); err != nil {
				t.Fatal(err)
			}
			run.CreatedAt = mustParseTime(raw)
			if status == domain.RunPending {
				pending = append(pending, run)
			} else {
				retries = append(retries, run)
			}
		}
	}
	t.Run("due and retry nanosecond boundary", func(t *testing.T) {
		for _, checkAt := range []time.Time{now.Add(-time.Nanosecond), now, now.Add(time.Nanosecond)} {
			items, err := st.ListCandidateRuns(ctx, checkAt, 100)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for index, raw := range legacyInstants {
				if !mustParseTime(raw).After(checkAt) {
					want = append(want, pending[index].ID, retries[index].ID)
				}
			}
			got := make([]string, 0, len(items))
			for index, item := range items {
				if index > 0 && item.DueTime.Before(items[index-1].DueTime) {
					t.Fatal("candidate runs are not in chronological due order")
				}
				got = append(got, item.ID)
			}
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("at %s candidates=%v, want=%v", checkAt, got, want)
			}
			count, err := st.DueRunCount(ctx, checkAt)
			if err != nil || count != len(want) {
				t.Fatalf("due count=%d want=%d error=%v", count, len(want), err)
			}
		}
	})
	t.Run("earliest due schedule", func(t *testing.T) {
		next, err := st.NextDueSchedule(ctx, now)
		if err != nil || next == nil || next.ScheduleID != schedules[0].ID || !next.DueTime.Equal(mustParseTime(legacyInstants[0])) {
			t.Fatalf("next=%+v error=%v", next, err)
		}
	})
	t.Run("schedule cursor order and equal instants", func(t *testing.T) {
		want := slices.Clone(schedules)
		slices.SortFunc(want, func(a, b domain.Schedule) int {
			if cmp := b.CreatedAt.Compare(a.CreatedAt); cmp != 0 {
				return cmp
			}
			return strings.Compare(b.ID, a.ID)
		})
		cursor := ""
		for index, schedule := range want {
			items, next, err := st.ListSchedules(ctx, nil, 1, cursor)
			if err != nil || len(items) != 1 || items[0].ID != schedule.ID {
				t.Fatalf("page %d=%+v want=%s error=%v", index, items, schedule.ID, err)
			}
			if index < len(want)-1 {
				if next == nil {
					t.Fatal("cursor ended before all schedules")
				}
				cursor = *next
			} else if next != nil {
				t.Fatal("unexpected final cursor")
			}
		}
	})
	t.Run("run cursor order and equal instants", func(t *testing.T) {
		want := append(slices.Clone(pending), retries...)
		slices.SortFunc(want, func(a, b domain.Run) int {
			if cmp := b.CreatedAt.Compare(a.CreatedAt); cmp != 0 {
				return cmp
			}
			return strings.Compare(b.ID, a.ID)
		})
		cursor := ""
		for index, run := range want {
			items, next, err := st.ListRuns(ctx, nil, nil, nil, 1, cursor)
			if err != nil || len(items) != 1 || items[0].ID != run.ID {
				t.Fatalf("page %d=%+v want=%s error=%v", index, items, run.ID, err)
			}
			if index < len(want)-1 {
				if next == nil {
					t.Fatal("cursor ended before all runs")
				}
				cursor = *next
			} else if next != nil {
				t.Fatal("unexpected final cursor")
			}
		}
	})
	t.Run("legacy bytes unchanged by reads", func(t *testing.T) {
		for index, schedule := range schedules {
			var raw string
			if err := st.queryRow(ctx, `SELECT created_at FROM schedules WHERE id = ?`, schedule.ID).Scan(&raw); err != nil || raw != legacyInstants[index] {
				t.Fatalf("timestamp rewritten: raw=%s error=%v", raw, err)
			}
		}
	})
}

func TestSQLiteLegacyTimestampLeaseAndRetention(t *testing.T) {
	testLegacyTimestampLeaseAndRetention(t, openTestStore(t))
}
func TestPostgresLegacyTimestampLeaseAndRetention(t *testing.T) {
	testLegacyTimestampLeaseAndRetention(t, openPostgresTestStore(t))
}

func testLegacyTimestampLeaseAndRetention(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	now := mustParseTime(legacyInstants[1])
	var claimed []domain.Run
	for _, raw := range legacyInstants {
		schedule := insertTestSchedule(t, st)
		run := insertTestRun(t, st, schedule.ID, domain.RunPending, now)
		if ok, err := st.ClaimRun(ctx, schedule, run.ID, "worker", now.Add(-time.Minute), time.Minute); err != nil || !ok {
			t.Fatalf("claim=%t error=%v", ok, err)
		}
		if _, err := st.exec(ctx, `UPDATE worker_leases SET expires_at = ? WHERE run_id = ?`, raw, run.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := st.exec(ctx, `UPDATE schedule_runs SET claim_expires_at = ? WHERE id = ?`, raw, run.ID); err != nil {
			t.Fatal(err)
		}
		claimed = append(claimed, run)
	}
	t.Run("lease exact and nanosecond boundary", func(t *testing.T) {
		for _, checkAt := range []time.Time{now.Add(-time.Nanosecond), now, now.Add(time.Nanosecond)} {
			items, err := st.ListExpiredLeases(ctx, checkAt)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for index, raw := range legacyInstants {
				if !mustParseTime(raw).After(checkAt) {
					want = append(want, claimed[index].ID)
				}
			}
			var got []string
			for _, item := range items {
				got = append(got, item.Run.ID)
			}
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("at %s expired=%v want=%v", checkAt, got, want)
			}
			count, err := st.ClaimedExpiredCount(ctx, checkAt)
			if err != nil || count != len(want) {
				t.Fatalf("expired count=%d want=%d error=%v", count, len(want), err)
			}
		}
		if err := st.RecoverExpiredClaims(ctx, now); err != nil {
			t.Fatal(err)
		}
		for index, run := range claimed {
			got, err := st.GetRun(ctx, run.ID)
			want := domain.RunClaimed
			if !mustParseTime(legacyInstants[index]).After(now) {
				want = domain.RunPending
			}
			if err != nil || got.Status != want {
				t.Fatalf("run %s status=%s want=%s error=%v", run.ID, got.Status, want, err)
			}
		}
	})
	t.Run("owned lease expires at the exact nanosecond", func(t *testing.T) {
		schedule := insertTestSchedule(t, st)
		run := insertTestRun(t, st, schedule.ID, domain.RunPending, now)
		ok, token, err := st.ClaimRunWithToken(ctx, schedule, run.ID, "boundary-worker", now, time.Nanosecond)
		if err != nil || !ok {
			t.Fatalf("claim=%t error=%v", ok, err)
		}
		if err := st.MarkRunRunningWithLease(ctx, run.ID, "boundary-worker", token, now); err != nil {
			t.Fatalf("valid lease rejected one nanosecond before expiry: %v", err)
		}
		if err := st.MarkRunRunningWithLease(ctx, run.ID, "boundary-worker", token, now.Add(time.Nanosecond)); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("expired lease accepted: %v", err)
		}
	})
	t.Run("retention receipts and audit bytes", func(t *testing.T) {
		schedule := insertTestSchedule(t, st)
		var terminal []domain.Run
		for _, raw := range legacyInstants {
			run := insertTestRun(t, st, schedule.ID, domain.RunSucceeded, mustParseTime(raw))
			if _, err := st.exec(ctx, `UPDATE schedule_runs SET finished_at = ? WHERE id = ?`, raw, run.ID); err != nil {
				t.Fatal(err)
			}
			terminal = append(terminal, run)
		}
		for index, raw := range legacyInstants {
			id := "timestamp_" + string(rune('0'+index))
			if err := st.InsertReceipt(ctx, domain.Receipt{ID: id, RunID: terminal[4].ID, ReceiptKind: "summary", ContentType: "text/plain", Body: "preserved", CreatedAt: mustParseTime(raw)}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.exec(ctx, `UPDATE execution_receipts SET created_at = ? WHERE id = ?`, raw, id); err != nil {
				t.Fatal(err)
			}
			if err := st.InsertRequestAudit(ctx, domain.RequestAudit{ID: id, Method: "GET", Path: "/v1/status", StatusCode: 200, CreatedAt: mustParseTime(raw)}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.exec(ctx, `UPDATE request_audit_logs SET created_at = ? WHERE id = ?`, raw, id); err != nil {
				t.Fatal(err)
			}
		}
		receipts, err := st.ListReceipts(ctx, terminal[4].ID)
		if err != nil || len(receipts) != len(legacyInstants) {
			t.Fatalf("receipts=%d error=%v", len(receipts), err)
		}
		for index, receipt := range receipts {
			if receipt.ID != "timestamp_"+string(rune('0'+index)) || receipt.Body != "preserved" {
				t.Fatalf("receipt order/body changed: %+v", receipt)
			}
		}
		pruned, err := st.PruneTerminalRunsBefore(ctx, now)
		if err != nil || pruned != 1 {
			t.Fatalf("pruned=%d want=1 error=%v", pruned, err)
		}
		for index, raw := range legacyInstants {
			id := "timestamp_" + string(rune('0'+index))
			for _, table := range []string{"execution_receipts", "request_audit_logs"} {
				var got string
				if err := st.queryRow(ctx, `SELECT created_at FROM `+table+` WHERE id = ?`, id).Scan(&got); err != nil || got != raw {
					t.Fatalf("%s history rewritten: %s want=%s error=%v", table, got, raw, err)
				}
			}
		}
	})
}
