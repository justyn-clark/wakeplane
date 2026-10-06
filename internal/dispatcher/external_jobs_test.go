package dispatcher

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/executors"
	"github.com/justyn-clark/wakeplane/internal/logging"
	"github.com/justyn-clark/wakeplane/internal/store"
)

type jobExecutorFunc func(context.Context, executors.ExecuteRequest) executors.Result

func (f jobExecutorFunc) Kind() domain.TargetKind { return domain.TargetKindHTTP }
func (f jobExecutorFunc) Execute(ctx context.Context, req executors.ExecuteRequest) executors.Result {
	return f(ctx, req)
}

func TestExternalJobTrackingResumesAcrossReopenWithoutNewAttempt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "jobs.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	schedule := dispatcherSchedule(now)
	schedule.Target = domain.TargetSpec{Kind: domain.TargetKindHTTP, Method: "POST", URL: "https://runner.test/jobs", HTTPJob: &domain.HTTPJobSpec{PollIntervalSeconds: 1}}
	if err := st.CreateSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: domain.NewID("run"), ScheduleID: schedule.ID, OccurrenceKey: domain.OccurrenceKey(schedule.ID, now), NominalTime: now, DueTime: now, Status: domain.RunPending, Attempt: 1, CreatedAt: now, UpdatedAt: now}
	if err := st.InsertRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	exec := jobExecutorFunc(func(ctx context.Context, req executors.ExecuteRequest) executors.Result {
		job := domain.ExternalJob{JobID: "durable-job", StatusURL: "https://runner.test/jobs/1", Status: domain.ExternalJobRunning, SubmittedAt: now, DeadlineAt: now.Add(time.Hour), RequestTarget: req.Schedule.Target}
		if err := req.Checkpoint(ctx, job); err != nil {
			t.Error(err)
		}
		return executors.Result{Deferred: true}
	})
	d := New(st, executors.NewRegistry(exec), logging.New(), "first-worker", time.Minute)
	if claimed, err := st.ClaimRun(ctx, schedule, run.ID, d.workerID, now, time.Minute); err != nil || !claimed {
		t.Fatalf("claim=%t err=%v", claimed, err)
	}
	d.executeRun(ctx, schedule, run)
	got, err := st.GetRun(ctx, run.ID)
	if err != nil || got.Status != domain.RunPending || got.ExternalJob == nil || got.Attempt != 1 || got.FinishedAt != nil {
		t.Fatalf("released run=%+v err=%v", got, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := got.ExternalJob.DeadlineAt
	exec = jobExecutorFunc(func(ctx context.Context, req executors.ExecuteRequest) executors.Result {
		if req.Run.ExternalJob == nil || req.Run.ExternalJob.JobID != "durable-job" || !req.Run.ExternalJob.DeadlineAt.Equal(deadline) {
			t.Fatalf("resume lost identity/deadline: %+v", req.Run.ExternalJob)
		}
		job := *req.Run.ExternalJob
		job.Status = domain.ExternalJobSucceeded
		if err := req.Checkpoint(ctx, job); err != nil {
			t.Error(err)
		}
		return executors.Result{ResultJSON: domain.MustJSON(job)}
	})
	d = New(st, executors.NewRegistry(exec), logging.New(), "second-worker", time.Minute)
	// Schedule changes while tracking must survive completion.
	schedule.Name = "edited while remote work runs"
	if err := st.UpdateSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	if claimed, err := st.ClaimRun(ctx, schedule, run.ID, d.workerID, time.Now().UTC(), time.Minute); err != nil || !claimed {
		t.Fatalf("reclaim=%t err=%v", claimed, err)
	}
	d.executeRun(ctx, schedule, got)
	got, err = st.GetRun(ctx, run.ID)
	if err != nil || got.Status != domain.RunSucceeded || got.Attempt != 1 || len(got.Attempts) != 1 {
		t.Fatalf("resumed run=%+v err=%v", got, err)
	}
	storedSchedule, err := st.GetSchedule(ctx, schedule.ID)
	if err != nil || storedSchedule.Name != schedule.Name {
		t.Fatalf("schedule edit overwritten: %+v err=%v", storedSchedule, err)
	}
}

func TestRecoverExpiredExternalJobResumesRatherThanFailing(t *testing.T) {
	runExpiredExternalRecovery(t, newDispatcherStore(t))
}
func TestPostgresRecoverExpiredExternalJobResumesRatherThanFailing(t *testing.T) {
	runExpiredExternalRecovery(t, openPostgresDispatcherStore(t))
}

func runExpiredExternalRecovery(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	schedule := dispatcherSchedule(now)
	schedule.Target = domain.TargetSpec{Kind: domain.TargetKindHTTP, Method: "POST", URL: "https://runner.test/jobs", HTTPJob: &domain.HTTPJobSpec{}}
	if err := st.CreateSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: domain.NewID("run"), ScheduleID: schedule.ID, OccurrenceKey: domain.OccurrenceKey(schedule.ID, now), NominalTime: now, DueTime: now, Status: domain.RunPending, Attempt: 1, CreatedAt: now, UpdatedAt: now}
	if err := st.InsertRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-time.Minute)
	if claimed, err := st.ClaimRun(ctx, schedule, run.ID, "old-worker", old, time.Second); err != nil || !claimed {
		t.Fatalf("claim=%t err=%v", claimed, err)
	}
	if err := st.MarkRunRunning(ctx, run.ID, old); err != nil {
		t.Fatal(err)
	}
	d := New(st, executors.NewRegistry(), logging.New(), "new-worker", time.Minute)
	if err := d.recoverExpiredLeases(ctx, now); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRun(ctx, run.ID)
	if err != nil || got.Status != domain.RunPending || got.Attempt != 1 || got.ErrorText != nil || got.FinishedAt != nil || len(got.Attempts) != 1 {
		t.Fatalf("expiry incorrectly failed or retried remote work: %+v err=%v", got, err)
	}
}

func TestRemoteTerminalFailureDoesNotCreateDuplicateRetry(t *testing.T) {
	st := newDispatcherStore(t)
	now := time.Now().UTC()
	schedule := dispatcherSchedule(now)
	schedule.Retry.MaxAttempts = 5
	schedule.Target = domain.TargetSpec{Kind: domain.TargetKindHTTP, Method: "POST", URL: "https://runner.test/jobs", HTTPJob: &domain.HTTPJobSpec{}}
	if err := st.CreateSchedule(context.Background(), schedule); err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: domain.NewID("run"), ScheduleID: schedule.ID, OccurrenceKey: domain.OccurrenceKey(schedule.ID, now), NominalTime: now, DueTime: now, Status: domain.RunPending, Attempt: 1, CreatedAt: now, UpdatedAt: now}
	if err := st.InsertRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	exec := jobExecutorFunc(func(context.Context, executors.ExecuteRequest) executors.Result {
		return executors.Result{ErrorText: "runner reports failed", TerminalFailure: true}
	})
	d := New(st, executors.NewRegistry(exec), logging.New(), "worker", time.Minute)
	if claimed, err := st.ClaimRun(context.Background(), schedule, run.ID, "worker", now, time.Minute); err != nil || !claimed {
		t.Fatalf("claim=%t err=%v", claimed, err)
	}
	d.executeRun(context.Background(), schedule, run)
	got, err := st.GetRun(context.Background(), run.ID)
	if err != nil || got.Status != domain.RunDeadLettered || len(got.Attempts) != 1 || got.DeadLetter == nil {
		t.Fatalf("terminal remote failure retried: %+v err=%v", got, err)
	}
}

func TestReplacePolicyAfterTargetEditCannotCancelRemoteTracker(t *testing.T) {
	st := newDispatcherStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	schedule := dispatcherSchedule(now)
	schedule.Target = domain.TargetSpec{Kind: domain.TargetKindHTTP, Method: "POST", URL: "https://runner.test/jobs", HTTPJob: &domain.HTTPJobSpec{}}
	schedule.Policy.MaxConcurrency = 2
	if err := st.CreateSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: domain.NewID("run"), ScheduleID: schedule.ID, OccurrenceKey: domain.OccurrenceKey(schedule.ID, now), NominalTime: now, DueTime: now, Status: domain.RunPending, Attempt: 1, CreatedAt: now, UpdatedAt: now}
	if err := st.InsertRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	claimed, token, err := st.ClaimRunWithToken(ctx, schedule, run.ID, "worker", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%t err=%v", claimed, err)
	}
	if err := st.MarkRunRunningWithLease(ctx, run.ID, "worker", token, now); err != nil {
		t.Fatal(err)
	}
	job := domain.ExternalJob{JobID: "job-1", StatusURL: "https://runner.test/jobs/1", Status: domain.ExternalJobRunning, SubmittedAt: now, DeadlineAt: now.Add(time.Hour), RequestTarget: schedule.Target}
	if err := st.SaveExternalJob(ctx, run, "worker", token, job, now); err != nil {
		t.Fatal(err)
	}
	schedule.Target = domain.TargetSpec{Kind: domain.TargetKindWorkflow, WorkflowID: "new-workflow"}
	schedule.Policy.Overlap = domain.OverlapReplace
	if err := st.UpdateSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	next := run
	next.ID = domain.NewID("run")
	next.OccurrenceKey = domain.OccurrenceKey(schedule.ID, now.Add(time.Minute))
	next.Status = domain.RunPending
	if err := st.InsertRun(ctx, next); err != nil {
		t.Fatal(err)
	}
	d := New(st, executors.NewRegistry(), logging.New(), "worker", time.Minute)
	cancelled := false
	d.registerCancel(run.ID, "old-tracker", func() { cancelled = true })
	claimable, err := d.prepareScheduleForClaim(ctx, schedule, next)
	if err != nil || claimable || cancelled {
		t.Fatalf("live target edit cancelled remote tracking: claimable=%t cancelled=%t err=%v", claimable, cancelled, err)
	}
}

func TestOldExecutionCleanupCannotRemoveNewTracker(t *testing.T) {
	d := New(newDispatcherStore(t), executors.NewRegistry(), logging.New(), "worker", time.Minute)
	d.registerCancel("same-run", "old", func() {})
	newCancelled := false
	d.registerCancel("same-run", "new", func() { newCancelled = true })
	d.unregisterCancel("same-run", "old")
	d.cancelRun("same-run")
	if !newCancelled || d.ActiveWorkers() != 1 {
		t.Fatal("old execution removed a new cancellation registration")
	}
	d.unregisterCancel("same-run", "new")
	if d.ActiveWorkers() != 0 {
		t.Fatal("new registration not cleaned up")
	}
}

func TestQueueCoalescingCannotSkipPreviouslySubmittedTracker(t *testing.T) {
	st := newDispatcherStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	schedule := dispatcherSchedule(now)
	schedule.Policy.Overlap = domain.OverlapQueueLatest
	schedule.Target = domain.TargetSpec{Kind: domain.TargetKindHTTP, Method: "POST", URL: "https://runner.test/jobs", HTTPJob: &domain.HTTPJobSpec{}}
	if err := st.CreateSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: domain.NewID("run"), ScheduleID: schedule.ID, OccurrenceKey: domain.OccurrenceKey(schedule.ID, now), NominalTime: now, DueTime: now, Status: domain.RunPending, Attempt: 1, CreatedAt: now, UpdatedAt: now}
	if err := st.InsertRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	claimed, token, err := st.ClaimRunWithToken(ctx, schedule, run.ID, "worker", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%t err=%v", claimed, err)
	}
	if err := st.MarkRunRunningWithLease(ctx, run.ID, "worker", token, now); err != nil {
		t.Fatal(err)
	}
	job := domain.ExternalJob{JobID: "job-1", StatusURL: "https://runner.test/jobs/1", Status: domain.ExternalJobRunning, SubmittedAt: now, DeadlineAt: now.Add(time.Hour), RequestTarget: schedule.Target}
	if err := st.SaveExternalJob(ctx, run, "worker", token, job, now); err != nil {
		t.Fatal(err)
	}
	if err := st.RequeueExternalRun(ctx, run.ID, "worker", token, now, false); err != nil {
		t.Fatal(err)
	}
	for _, minutes := range []int{1, 2} {
		next := run
		next.ID = domain.NewID("run")
		next.OccurrenceKey = domain.OccurrenceKey(schedule.ID, now.Add(time.Duration(minutes)*time.Minute))
		next.DueTime = now.Add(time.Duration(minutes) * time.Minute)
		if err := st.InsertRun(ctx, next); err != nil {
			t.Fatal(err)
		}
	}
	d := New(st, executors.NewRegistry(), logging.New(), "worker", time.Minute)
	if err := d.enforceQueuePolicy(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRun(ctx, run.ID)
	if err != nil || got.Status != domain.RunPending || got.ErrorText != nil {
		t.Fatalf("submitted remote work marked skipped: %+v err=%v", got, err)
	}
	pending, err := st.ListPendingRunsBySchedule(ctx, schedule.ID)
	if err != nil || len(pending) != 2 {
		t.Fatalf("want tracker+newest unsubmitted occurrence, got %d err=%v", len(pending), err)
	}
}
