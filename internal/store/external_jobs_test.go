package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestSQLiteExternalJobInvariants(t *testing.T) { runExternalJobInvariants(t, openTestStore(t)) }
func TestPostgresExternalJobInvariants(t *testing.T) {
	runExternalJobInvariants(t, openPostgresTestStore(t))
}

func runExternalJobInvariants(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("repeat additive migrations: %v", err)
	}
	schedule := insertTestSchedule(t, st)
	now := time.Now().UTC()
	run := insertTestRun(t, st, schedule.ID, domain.RunPending, now)
	worker := "external-worker"
	claimed, err := st.ClaimRun(ctx, schedule, run.ID, worker, now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%t err=%v", claimed, err)
	}
	token, err := st.ExecutionLeaseToken(ctx, run.ID, worker)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRunRunningWithLease(ctx, run.ID, worker, token, now); err != nil {
		t.Fatal(err)
	}
	job := domain.ExternalJob{Status: domain.ExternalJobSubmitting, SubmittedAt: now, DeadlineAt: now.Add(10 * time.Minute), RequestTarget: domain.TargetSpec{Kind: domain.TargetKindHTTP, Method: "POST", URL: "https://runner.test/jobs", HTTPJob: &domain.HTTPJobSpec{PollIntervalSeconds: 1}}}
	if err := st.SaveExternalJob(ctx, run, worker, token, job, now); err != nil {
		t.Fatal(err)
	}
	job.JobID, job.StatusURL, job.Status = "job-1", "https://runner.test/jobs/1", domain.ExternalJobQueued
	if err := st.SaveExternalJob(ctx, run, worker, token, job, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRun(ctx, run.ID)
	if err != nil || got.ExternalJob == nil || got.ExternalJob.JobID != "job-1" || got.Status != domain.RunRunning || !got.ExternalJob.DeadlineAt.Equal(job.DeadlineAt) {
		t.Fatalf("run=%+v err=%v", got, err)
	}
	if len(got.Receipts) != 2 {
		t.Fatalf("expected immutable submission and acceptance observations, got %d", len(got.Receipts))
	}
	if err := st.SaveExternalJob(ctx, run, worker, token, job, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	receipts, err := st.ListReceipts(ctx, run.ID)
	if err != nil || len(receipts) != 2 {
		t.Fatalf("identical polls should not grow ledger: count=%d err=%v", len(receipts), err)
	}
	for _, mutate := range []func(*domain.ExternalJob){
		func(j *domain.ExternalJob) { j.JobID = "another-job" },
		func(j *domain.ExternalJob) { j.StatusURL = "https://runner.test/jobs/2" },
		func(j *domain.ExternalJob) { j.DeadlineAt = j.DeadlineAt.Add(time.Hour) },
		func(j *domain.ExternalJob) { j.RequestTarget.URL = "https://other.test/jobs" },
	} {
		bad := job
		mutate(&bad)
		if err := st.SaveExternalJob(ctx, run, worker, token, bad, now.Add(3*time.Second)); err == nil {
			t.Fatal("immutable identity/deadline/target changed")
		}
	}
	// Lease expiry resumes the same attempt. Reclaiming with the SAME worker ID
	// still produces a new token and fences all writes by the older execution.
	recoveredAt := now.Add(2 * time.Minute)
	if err := st.RequeueExternalRun(ctx, run.ID, "", "", recoveredAt, true); err != nil {
		t.Fatal(err)
	}
	claimed, err = st.ClaimRun(ctx, schedule, run.ID, worker, recoveredAt, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("reclaim=%t err=%v", claimed, err)
	}
	newToken, err := st.ExecutionLeaseToken(ctx, run.ID, worker)
	if err != nil || newToken == token {
		t.Fatalf("new token=%q old=%q err=%v", newToken, token, err)
	}
	if err := st.MarkRunRunningWithLease(ctx, run.ID, worker, newToken, recoveredAt); err != nil {
		t.Fatal(err)
	}
	job.Status = domain.ExternalJobSucceeded
	if err := st.SaveExternalJob(ctx, run, worker, token, job, recoveredAt); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale checkpoint err=%v", err)
	}
	run.Status = domain.RunSucceeded
	run.FinishedAt = &recoveredAt
	if err := st.FinishRunWithLease(ctx, run, worker, token, recoveredAt); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale finalize err=%v", err)
	}
	if err := st.RenewLeaseWithToken(ctx, run.ID, worker, token, recoveredAt, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale renewal err=%v", err)
	}
	if err := st.RequeueExternalRun(ctx, run.ID, worker, token, recoveredAt, false); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale release err=%v", err)
	}
	if err := st.RequeueExternalRun(ctx, run.ID, "", "", recoveredAt, true); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("unexpired lease recovered: %v", err)
	}
	if err := st.SaveExternalJob(ctx, run, worker, newToken, job, recoveredAt); err != nil {
		t.Fatal(err)
	}
	changed := job
	changed.Status = domain.ExternalJobRunning
	if err := st.SaveExternalJob(ctx, run, worker, newToken, changed, recoveredAt); err == nil {
		t.Fatal("terminal job status changed")
	}
	if err := st.FinishRunWithLease(ctx, run, worker, newToken, recoveredAt); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetRun(ctx, run.ID)
	if err != nil || got.Status != domain.RunSucceeded || got.Attempt != 1 || got.ExternalJob.Status != domain.ExternalJobSucceeded || (got.ClaimedByWorkerID == nil || *got.ClaimedByWorkerID != worker) {
		t.Fatalf("completed run=%+v err=%v", got, err)
	}
}

func TestSQLiteExternalJobPolicyAndReconciliation(t *testing.T) {
	runExternalJobPolicyAndReconciliation(t, openTestStore(t))
}
func TestPostgresExternalJobPolicyAndReconciliation(t *testing.T) {
	runExternalJobPolicyAndReconciliation(t, openPostgresTestStore(t))
}

func runExternalJobPolicyAndReconciliation(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	schedule := insertTestSchedule(t, st)
	schedule.Policy.Overlap = domain.OverlapForbid
	schedule.Policy.MaxConcurrency = 1
	if err := st.UpdateSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	run := insertTestRun(t, st, schedule.ID, domain.RunPending, now)
	worker := "worker"
	if claimed, err := st.ClaimRun(ctx, schedule, run.ID, worker, now, time.Minute); err != nil || !claimed {
		t.Fatalf("claim=%t err=%v", claimed, err)
	}
	token, err := st.ExecutionLeaseToken(ctx, run.ID, worker)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRunRunningWithLease(ctx, run.ID, worker, token, now); err != nil {
		t.Fatal(err)
	}
	job := domain.ExternalJob{JobID: "remote-1", StatusURL: "https://runner.test/jobs/1", Status: domain.ExternalJobRunning, SubmittedAt: now, DeadlineAt: now.Add(time.Hour), RequestTarget: domain.TargetSpec{Kind: domain.TargetKindHTTP, Method: "POST", URL: "https://runner.test/jobs", HTTPJob: &domain.HTTPJobSpec{}, Headers: map[string]string{"Authorization": "old secret"}}}
	if err := st.SaveExternalJob(ctx, run, worker, token, job, now); err != nil {
		t.Fatal(err)
	}
	// A tracking failure and retry must be one transaction.
	run.Status = domain.RunFailed
	run.FinishedAt = &now
	retry := run
	retry.ID = domain.NewID("run")
	retry.Status = domain.RunRetryScheduled
	retry.Attempt = 2
	retry.FinishedAt = nil
	if err := st.FinishExternalFailure(ctx, run, worker, token, &retry, nil, now); err != nil {
		t.Fatal(err)
	}
	other := insertTestRun(t, st, schedule.ID, domain.RunPending, now.Add(time.Second))
	if claimed, err := st.ClaimRun(ctx, schedule, other.ID, "new-worker", now, time.Minute); err != nil || claimed {
		t.Fatalf("unresolved remote job lost overlap slot: claim=%t err=%v", claimed, err)
	}
	if count, err := st.ActiveOtherOccurrenceCount(ctx, schedule.ID, other.OccurrenceKey); err != nil || count != 1 {
		t.Fatalf("remote occurrence count=%d err=%v", count, err)
	}
	stored, err := st.GetExternalJob(ctx, run.OccurrenceKey)
	if err != nil {
		t.Fatal(err)
	}
	observed := *stored
	observed.Status = domain.ExternalJobSucceeded
	if err := st.ReconcileExternalJob(ctx, run, *stored, observed, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("reconciled concurrently with retry: %v", err)
	}
	if claimed, err := st.ClaimRun(ctx, schedule, retry.ID, worker, now, time.Minute); err != nil || !claimed {
		t.Fatalf("own remote job resume blocked by its slot: claim=%t err=%v", claimed, err)
	}
	newToken, err := st.ExecutionLeaseToken(ctx, retry.ID, worker)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRunRunningWithLease(ctx, retry.ID, worker, newToken, now); err != nil {
		t.Fatal(err)
	}
	retry.Status = domain.RunDeadLettered
	retry.FinishedAt = &now
	if err := st.FinishRunWithLease(ctx, retry, worker, newToken, now); err != nil {
		t.Fatal(err)
	}
	// An unresolved remote run survives retention so its observation endpoint and
	// credential snapshot remain available for truthful operator reconciliation.
	if _, err := st.PruneTerminalRunsBefore(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetRun(ctx, retry.ID); err != nil {
		t.Fatalf("unresolved run pruned: %v", err)
	}
	if err := st.ReconcileExternalJob(ctx, retry, *stored, observed, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRun(ctx, retry.ID)
	if err != nil || got.Status != domain.RunDeadLettered || got.ExternalJob.Status != domain.ExternalJobSucceeded {
		t.Fatalf("reconciliation rewrote history: %+v err=%v", got, err)
	}
	if claimed, err := st.ClaimRun(ctx, schedule, other.ID, "new-worker", now.Add(time.Second), time.Minute); err != nil || !claimed {
		t.Fatalf("confirmed completion did not release slot: claim=%t err=%v", claimed, err)
	}
	if err := st.ReconcileExternalJob(ctx, retry, *stored, observed, now.Add(time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale reconciliation accepted: %v", err)
	}
	if _, err := st.PruneTerminalRunsBefore(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	tombstone, err := st.GetExternalJob(ctx, run.OccurrenceKey)
	if err != nil || tombstone == nil || !tombstone.Compacted || tombstone.JobID != "remote-1" || tombstone.Status != domain.ExternalJobSucceeded || tombstone.StatusURL != "" || tombstone.RequestTarget.Headers != nil {
		t.Fatalf("snapshot not compacted: %+v err=%v", tombstone, err)
	}
	var targetJSON string
	if err := st.queryRow(ctx, `SELECT target_snapshot_json FROM external_jobs WHERE occurrence_key = ?`, run.OccurrenceKey).Scan(&targetJSON); err != nil || targetJSON != "{}" {
		t.Fatalf("private snapshot retained: %s err=%v", targetJSON, err)
	}
	if _, err := st.PruneTerminalRunsBefore(ctx, now.Add(time.Hour)); err != nil {
		t.Fatalf("repeat prune: %v", err)
	}
}

func TestSQLiteNoConcurrentAttemptsForSameOccurrence(t *testing.T) {
	runNoConcurrentAttempts(t, openTestStore(t))
}
func TestPostgresNoConcurrentAttemptsForSameOccurrence(t *testing.T) {
	runNoConcurrentAttempts(t, openPostgresTestStore(t))
}

func runNoConcurrentAttempts(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	schedule := insertTestSchedule(t, st)
	schedule.Policy.Overlap = domain.OverlapAllow
	schedule.Policy.MaxConcurrency = 10
	if err := st.UpdateSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	first := insertTestRun(t, st, schedule.ID, domain.RunPending, now)
	second := first
	second.ID = domain.NewID("run")
	second.Attempt = 2
	if err := st.InsertRun(ctx, second); err != nil {
		t.Fatal(err)
	}
	if claimed, err := st.ClaimRun(ctx, schedule, first.ID, "worker-a", now, time.Minute); err != nil || !claimed {
		t.Fatalf("first claim=%t err=%v", claimed, err)
	}
	if claimed, err := st.ClaimRun(ctx, schedule, second.ID, "worker-b", now, time.Minute); err != nil || claimed {
		t.Fatalf("same occurrence executed twice: claim=%t err=%v", claimed, err)
	}
}

func TestSQLiteLostAcceptanceReconciliationFreezesRecoveredIdentity(t *testing.T) {
	runLostAcceptanceReconciliation(t, openTestStore(t))
}
func TestPostgresLostAcceptanceReconciliationFreezesRecoveredIdentity(t *testing.T) {
	runLostAcceptanceReconciliation(t, openPostgresTestStore(t))
}

func runLostAcceptanceReconciliation(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	schedule := insertTestSchedule(t, st)
	run := insertTestRun(t, st, schedule.ID, domain.RunPending, now)
	claimed, token, err := st.ClaimRunWithToken(ctx, schedule, run.ID, "worker", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%t err=%v", claimed, err)
	}
	if err := st.MarkRunRunningWithLease(ctx, run.ID, "worker", token, now); err != nil {
		t.Fatal(err)
	}
	job := domain.ExternalJob{Status: domain.ExternalJobSubmitting, SubmittedAt: now, DeadlineAt: now.Add(time.Minute), RequestTarget: domain.TargetSpec{Kind: domain.TargetKindHTTP, Method: "POST", URL: "https://runner.test/jobs", HTTPJob: &domain.HTTPJobSpec{LookupURL: "https://runner.test/jobs/lookup"}}}
	if err := st.SaveExternalJob(ctx, run, "worker", token, job, now); err != nil {
		t.Fatal(err)
	}
	run.Status = domain.RunDeadLettered
	run.FinishedAt = &now
	if err := st.FinishRunWithLease(ctx, run, "worker", token, now); err != nil {
		t.Fatal(err)
	}
	expected, err := st.GetExternalJob(ctx, run.OccurrenceKey)
	if err != nil || !expected.CanReconcile || expected.JobID != "" {
		t.Fatalf("recovery capability=%+v err=%v", expected, err)
	}
	observed := *expected
	observed.JobID = "accepted-job"
	observed.StatusURL = "https://runner.test/jobs/accepted"
	observed.Status = domain.ExternalJobRunning
	if err := st.ReconcileExternalJob(ctx, run, *expected, observed, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	recovered, err := st.GetExternalJob(ctx, run.OccurrenceKey)
	if err != nil || recovered.JobID != "accepted-job" || !recovered.CanReconcile || !recovered.DeadlineAt.Equal(expected.DeadlineAt) {
		t.Fatalf("identity recovery=%+v err=%v", recovered, err)
	}
	if err := st.ReconcileExternalJob(ctx, run, *expected, observed, now.Add(time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale pre-acceptance lookup overwrote identity: %v", err)
	}
	changed := *recovered
	changed.JobID = "another-job"
	changed.Status = domain.ExternalJobSucceeded
	if err := st.ReconcileExternalJob(ctx, run, *recovered, changed, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("recovered identity changed: %v", err)
	}
}

func TestSQLiteStaleClaimRecoveryCannotDeleteRenewedOrRunningLease(t *testing.T) {
	runStaleClaimRecovery(t, openTestStore(t))
}
func TestPostgresStaleClaimRecoveryCannotDeleteRenewedOrRunningLease(t *testing.T) {
	runStaleClaimRecovery(t, openPostgresTestStore(t))
}

func runStaleClaimRecovery(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	schedule := insertTestSchedule(t, st)
	run := insertTestRun(t, st, schedule.ID, domain.RunPending, now)
	claimed, token, err := st.ClaimRunWithToken(ctx, schedule, run.ID, "worker", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%t err=%v", claimed, err)
	}
	if err := st.ResetClaimedRun(ctx, run.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRun(ctx, run.ID)
	if err != nil || got.Status != domain.RunClaimed {
		t.Fatalf("renewed claim reset: %+v err=%v", got, err)
	}
	if current, err := st.ExecutionLeaseToken(ctx, run.ID, "worker"); err != nil || current != token {
		t.Fatalf("renewed claim lease deleted: %q err=%v", current, err)
	}
	if err := st.MarkRunRunningWithLease(ctx, run.ID, "worker", token, now); err != nil {
		t.Fatal(err)
	}
	if err := st.ResetClaimedRun(ctx, run.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if current, err := st.ExecutionLeaseToken(ctx, run.ID, "worker"); err != nil || current != token {
		t.Fatalf("stale claimed snapshot deleted a now-running lease: %q err=%v", current, err)
	}
}
