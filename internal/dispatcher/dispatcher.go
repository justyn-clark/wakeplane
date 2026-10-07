package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/executors"
	"github.com/justyn-clark/wakeplane/internal/store"
)

type Dispatcher struct {
	store     *store.Store
	registry  *executors.Registry
	logger    *slog.Logger
	workerID  string
	leaseTTL  time.Duration
	now       func() time.Time
	activeMu  sync.Mutex
	active    map[string]activeExecution
	activeWG  sync.WaitGroup
	lastError error
}

type activeExecution struct {
	token  string
	cancel context.CancelFunc
}

func New(st *store.Store, registry *executors.Registry, logger *slog.Logger, workerID string, leaseTTL time.Duration) *Dispatcher {
	return &Dispatcher{
		store:    st,
		registry: registry,
		logger:   logger,
		workerID: workerID,
		leaseTTL: leaseTTL,
		now: func() time.Time {
			return time.Now().UTC()
		},
		active: map[string]activeExecution{},
	}
}

func (d *Dispatcher) ActiveWorkers() int {
	d.activeMu.Lock()
	defer d.activeMu.Unlock()
	return len(d.active)
}

func (d *Dispatcher) Tick(ctx context.Context) error {
	now := d.now()
	if err := d.recoverExpiredLeases(ctx, now); err != nil {
		return err
	}
	candidates, err := d.store.ListCandidateRuns(ctx, now, 64)
	if err != nil {
		return err
	}
	for _, run := range candidates {
		schedule, err := d.store.GetSchedule(ctx, run.ScheduleID)
		if err != nil {
			d.logger.Error("load schedule for run", "run_id", run.ID, "error", err)
			continue
		}
		claimable, err := d.prepareScheduleForClaim(ctx, schedule, run)
		if err != nil {
			d.logger.Error("prepare schedule for claim", "schedule_id", schedule.ID, "run_id", run.ID, "error", err)
			continue
		}
		if !claimable {
			continue
		}
		claimed, token, err := d.store.ClaimRunWithToken(ctx, schedule, run.ID, d.workerID, now, d.leaseTTL)
		if err != nil || !claimed {
			continue
		}
		run.ClaimedByWorkerID = &d.workerID
		run.ExecutionLeaseToken = token
		expires := now.Add(d.leaseTTL)
		run.ClaimExpiresAt = &expires
		d.activeWG.Add(1)
		go func(schedule domain.Schedule, run domain.Run) {
			defer d.activeWG.Done()
			d.executeRun(ctx, schedule, run)
		}(schedule, run)
	}
	return nil
}

func (d *Dispatcher) recoverExpiredLeases(ctx context.Context, now time.Time) error {
	expired, err := d.store.ListExpiredLeases(ctx, now)
	if err != nil {
		return err
	}
	for _, item := range expired {
		switch item.Run.Status {
		case domain.RunClaimed:
			if err := d.store.ResetClaimedRun(ctx, item.Run.ID, now); err != nil {
				return err
			}
		case domain.RunRunning:
			job, err := d.store.GetExternalJob(ctx, item.Run.OccurrenceKey)
			if err != nil {
				return err
			}
			if job != nil || item.Schedule.Target.HTTPJob != nil {
				err := d.store.RequeueExternalRun(ctx, item.Run.ID, "", "", now, true)
				if err != nil && !errors.Is(err, store.ErrLeaseLost) {
					return err
				}
				if err == nil {
					d.cancelRun(item.Run.ID)
				}
				continue
			}
			result := executors.Result{
				ErrorText: "worker lease expired during execution",
			}
			err = d.completeFailureWithResult(ctx, item.Schedule, item.Run, result)
			if err != nil && !errors.Is(err, store.ErrLeaseLost) {
				return err
			}
			if err == nil {
				d.cancelRun(item.Run.ID)
			}
		default:
			if err := d.store.ClearLease(ctx, item.Run.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Dispatcher) prepareScheduleForClaim(ctx context.Context, schedule domain.Schedule, run domain.Run) (bool, error) {
	activeCount, err := d.store.ActiveOtherOccurrenceCount(ctx, schedule.ID, run.OccurrenceKey)
	if err != nil {
		return false, err
	}
	if activeCount >= schedule.Policy.MaxConcurrency {
		if err := d.enforceQueuePolicy(ctx, schedule); err != nil {
			return false, err
		}
		return false, nil
	}
	if activeCount == 0 {
		return true, nil
	}
	switch schedule.Policy.Overlap {
	case domain.OverlapAllow:
		return activeCount < schedule.Policy.MaxConcurrency, nil
	case domain.OverlapForbid:
		return false, nil
	case domain.OverlapQueueLatest:
		return false, d.enforceQueuePolicy(ctx, schedule)
	case domain.OverlapReplace:
		remoteCount, err := d.store.UnresolvedExternalJobCount(ctx, schedule.ID)
		if err != nil {
			return false, err
		}
		if remoteCount > 0 {
			// A live schedule edit cannot turn remote work into cancellable local
			// work. Keep tracking it until the runner confirms completion.
			return false, d.enforceQueuePolicy(ctx, schedule)
		}
		activeRuns, err := d.store.ListActiveRuns(ctx, schedule.ID)
		if err != nil {
			return false, err
		}
		for _, active := range activeRuns {
			d.cancelRun(active.ID)
		}
		return false, d.enforceQueuePolicy(ctx, schedule)
	default:
		return false, nil
	}
}

func (d *Dispatcher) enforceQueuePolicy(ctx context.Context, schedule domain.Schedule) error {
	pending, err := d.store.ListPendingRunsBySchedule(ctx, schedule.ID)
	if err != nil {
		return err
	}
	// Queue coalescing only skips work that has not been submitted. A pending
	// tracker may already represent active remote work after a restart/retry.
	unsubmitted := make([]domain.Run, 0, len(pending))
	for _, run := range pending {
		job, err := d.store.GetExternalJob(ctx, run.OccurrenceKey)
		if err != nil {
			return err
		}
		if job == nil {
			unsubmitted = append(unsubmitted, run)
		}
	}
	pending = unsubmitted
	if len(pending) <= 1 {
		return nil
	}
	now := d.now()
	keepID := pending[len(pending)-1].ID
	for _, run := range pending[:len(pending)-1] {
		if run.ID == keepID {
			continue
		}
		message := "superseded by newer queued occurrence"
		if schedule.Policy.Overlap == domain.OverlapReplace {
			message = "replace overlap downgraded to queued latest until current execution exits"
		}
		run.Status = domain.RunSkipped
		run.ErrorText = &message
		run.FinishedAt = &now
		run.UpdatedAt = now
		if err := d.store.FinishRun(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) executeRun(ctx context.Context, schedule domain.Schedule, run domain.Run) {
	startedAt := d.now()
	var err error
	run.ExternalJob, err = d.store.GetExternalJob(ctx, run.OccurrenceKey)
	if err != nil {
		d.logger.Error("load external job", "run_id", run.ID, "error", err)
		return
	}
	run.Event, err = d.store.GetRunEvent(ctx, run.OccurrenceKey)
	if err != nil {
		d.logger.Error("load trigger event", "run_id", run.ID, "error", err)
		return
	}
	external := run.ExternalJob != nil || schedule.Target.HTTPJob != nil
	if external && run.ExecutionLeaseToken == "" {
		run.ExecutionLeaseToken, err = d.store.ExecutionLeaseToken(ctx, run.ID, d.workerID)
	}
	if err == nil && run.ExecutionLeaseToken != "" {
		err = d.store.MarkRunRunningWithLease(ctx, run.ID, d.workerID, run.ExecutionLeaseToken, startedAt)
	} else if err == nil {
		err = d.store.MarkRunRunning(ctx, run.ID, startedAt)
	}
	if err != nil {
		d.logger.Error("mark run running", "run_id", run.ID, "error", err)
		return
	}
	if run.StartedAt == nil {
		run.StartedAt = &startedAt
	}
	deadline := startedAt.Add(time.Duration(schedule.Policy.TimeoutSeconds) * time.Second)
	if external {
		deadline = run.StartedAt.Add(time.Duration(schedule.Policy.TimeoutSeconds) * time.Second)
		if run.ExternalJob != nil {
			deadline = run.ExternalJob.DeadlineAt
		}
	}
	execCtx, cancel := context.WithDeadline(ctx, deadline)
	activeToken := domain.NewID("execution")
	d.registerCancel(run.ID, activeToken, cancel)
	defer func() {
		d.unregisterCancel(run.ID, activeToken)
		cancel()
	}()

	group, heartbeatCtx := errgroup.WithContext(execCtx)
	execDone := make(chan struct{})
	group.Go(func() error {
		ticker := time.NewTicker(d.leaseTTL / 2)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return nil
			case <-execDone:
				return nil
			case <-ticker.C:
				var err error
				if run.ExecutionLeaseToken != "" {
					err = d.store.RenewLeaseWithToken(context.Background(), run.ID, d.workerID, run.ExecutionLeaseToken, d.now(), d.leaseTTL)
				} else {
					err = d.store.RenewLease(context.Background(), run.ID, d.workerID, d.now(), d.leaseTTL)
				}
				if err != nil {
					return err
				}
			}
		}
	})

	kind := schedule.Target.Kind
	if run.ExternalJob != nil {
		kind = domain.TargetKindHTTP
	}
	executor, ok := d.registry.Get(kind)
	if !ok {
		d.completeFailure(context.Background(), schedule, run, fmt.Errorf("executor %q not registered", schedule.Target.Kind), true)
		return
	}

	var result executors.Result
	group.Go(func() error {
		defer close(execDone)
		result = executor.Execute(heartbeatCtx, executors.ExecuteRequest{
			Schedule: schedule, Run: run, Timeout: schedule.Policy.TimeoutSeconds,
			Checkpoint: func(checkpointCtx context.Context, job domain.ExternalJob) error {
				return d.store.SaveExternalJob(checkpointCtx, run, d.workerID, run.ExecutionLeaseToken, job, d.now())
			},
		})
		return nil
	})

	if err := group.Wait(); err != nil {
		if external {
			d.logger.Error("external job execution lease lost; resume after expiry", "run_id", run.ID, "error", err)
			return
		}
		d.completeFailure(context.Background(), schedule, run, err, false)
		return
	}
	if result.Deferred && external {
		if err := d.store.RequeueExternalRun(context.Background(), run.ID, d.workerID, run.ExecutionLeaseToken, d.now(), false); err != nil {
			d.logger.Error("release external job tracking", "run_id", run.ID, "error", err)
		}
		return
	}

	if result.ErrorText != "" {
		d.completeFailureWithResult(context.Background(), schedule, run, result)
		return
	}
	finishedAt := d.now()
	run.Status = domain.RunSucceeded
	run.ClaimExpiresAt = nil
	run.FinishedAt = &finishedAt
	run.HTTPStatusCode = result.HTTPStatusCode
	run.ExitCode = result.ExitCode
	run.ResultJSON = result.ResultJSON
	run.UpdatedAt = finishedAt
	if err := d.finishRun(context.Background(), run); err != nil {
		d.logger.Error("finish succeeded run", "run_id", run.ID, "error", err)
		return
	}
	for _, receipt := range result.Receipts {
		_ = d.store.InsertReceipt(context.Background(), domain.Receipt{
			ID:          domain.NewID("rcpt"),
			RunID:       run.ID,
			ReceiptKind: receipt.Kind,
			ContentType: receipt.ContentType,
			Body:        receipt.Body,
			CreatedAt:   finishedAt,
		})
	}
	_ = d.store.RecordScheduleCompletion(context.Background(), schedule.ID, finishedAt)
}

func (d *Dispatcher) completeFailure(ctx context.Context, schedule domain.Schedule, run domain.Run, err error, fatal bool) {
	result := executors.Result{ErrorText: err.Error()}
	if fatal {
		result.Cancelled = true
	}
	d.completeFailureWithResult(ctx, schedule, run, result)
}

func (d *Dispatcher) completeFailureWithResult(ctx context.Context, schedule domain.Schedule, run domain.Run, result executors.Result) error {
	finishedAt := d.now()
	run.StartedAt = timePtrOr(run.StartedAt, finishedAt)
	run.FinishedAt = &finishedAt
	run.ClaimExpiresAt = nil
	run.HTTPStatusCode = result.HTTPStatusCode
	run.ExitCode = result.ExitCode
	run.ResultJSON = result.ResultJSON
	run.UpdatedAt = finishedAt
	if result.Cancelled {
		run.Status = domain.RunCancelled
	} else if shouldRetry(schedule, run.Attempt) && !result.TerminalFailure {
		run.Status = domain.RunFailed
	} else {
		run.Status = domain.RunDeadLettered
	}
	if result.ErrorText != "" {
		run.ErrorText = &result.ErrorText
	}
	var retry *domain.Run
	var dead *domain.DeadLetter
	if run.Status == domain.RunFailed {
		retryAt := backoffFor(schedule.Retry, run.Attempt, finishedAt)
		retry = &domain.Run{ID: domain.NewID("run"), ScheduleID: run.ScheduleID, OccurrenceKey: run.OccurrenceKey, NominalTime: run.NominalTime, DueTime: retryAt, Status: domain.RunRetryScheduled, Attempt: run.Attempt + 1, RetryAvailableAt: &retryAt, CreatedAt: finishedAt, UpdatedAt: finishedAt}
	}
	if run.Status == domain.RunDeadLettered {
		dead = &domain.DeadLetter{ID: domain.NewID("dlq"), RunID: run.ID, ScheduleID: run.ScheduleID, OccurrenceKey: run.OccurrenceKey, Reason: result.ErrorText, PayloadJSON: run.ResultJSON, CreatedAt: finishedAt}
	}
	var finishErr error
	if run.ExecutionLeaseToken != "" {
		finishErr = d.store.FinishExternalFailure(ctx, run, d.workerID, run.ExecutionLeaseToken, retry, dead, finishedAt)
	} else {
		finishErr = d.store.FinishExpiredRunFailure(ctx, run, retry, dead, finishedAt)
	}
	if finishErr != nil {
		d.logger.Error("finish failed run", "run_id", run.ID, "error", finishErr)
		return finishErr
	}
	for _, receipt := range result.Receipts {
		_ = d.store.InsertReceipt(context.Background(), domain.Receipt{
			ID:          domain.NewID("rcpt"),
			RunID:       run.ID,
			ReceiptKind: receipt.Kind,
			ContentType: receipt.ContentType,
			Body:        receipt.Body,
			CreatedAt:   finishedAt,
		})
	}
	return nil
}

func (d *Dispatcher) finishRun(ctx context.Context, run domain.Run) error {
	if run.ExecutionLeaseToken != "" {
		return d.store.FinishRunWithLease(ctx, run, d.workerID, run.ExecutionLeaseToken, d.now())
	}
	return d.store.FinishRun(ctx, run)
}

func shouldRetry(schedule domain.Schedule, attempt int) bool {
	if schedule.Retry.Strategy == domain.RetryNone {
		return false
	}
	return attempt < schedule.Retry.MaxAttempts
}

func backoffFor(retry domain.RetryPolicy, attempt int, now time.Time) time.Time {
	delay := retry.InitialDelaySeconds
	for i := 1; i < attempt; i++ {
		if delay > retry.MaxDelaySeconds/2 {
			delay = retry.MaxDelaySeconds
			break
		}
		delay *= 2
		if delay >= retry.MaxDelaySeconds {
			delay = retry.MaxDelaySeconds
			break
		}
	}
	return now.Add(time.Duration(delay) * time.Second).UTC()
}

func (d *Dispatcher) registerCancel(runID, token string, cancel context.CancelFunc) {
	d.activeMu.Lock()
	defer d.activeMu.Unlock()
	d.active[runID] = activeExecution{token: token, cancel: cancel}
}

func (d *Dispatcher) unregisterCancel(runID, token string) {
	d.activeMu.Lock()
	defer d.activeMu.Unlock()
	if current, ok := d.active[runID]; ok && current.token == token {
		delete(d.active, runID)
	}
}

func (d *Dispatcher) cancelRun(runID string) {
	d.activeMu.Lock()
	active, ok := d.active[runID]
	d.activeMu.Unlock()
	if ok {
		active.cancel()
	}
}

func (d *Dispatcher) Shutdown(ctx context.Context) error {
	d.activeMu.Lock()
	active := len(d.active)
	cancels := make([]context.CancelFunc, 0, active)
	for _, active := range d.active {
		cancels = append(cancels, active.cancel)
	}
	d.activeMu.Unlock()
	d.logger.Info("dispatcher shutdown: cancelling active executions", "count", active)
	for _, cancel := range cancels {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		d.activeWG.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		d.logger.Warn("dispatcher shutdown timeout: active work did not drain", "remaining", d.ActiveWorkers())
		return ctx.Err()
	case <-done:
		d.logger.Info("dispatcher shutdown complete")
		return nil
	}
}

func timePtrOr(current *time.Time, fallback time.Time) *time.Time {
	if current != nil {
		return current
	}
	return &fallback
}
