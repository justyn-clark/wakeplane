package dispatcher

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/justyn-clark/timekeeper/internal/domain"
	"github.com/justyn-clark/timekeeper/internal/executors"
	"github.com/justyn-clark/timekeeper/internal/store"
)

type Dispatcher struct {
	store     *store.Store
	registry  *executors.Registry
	logger    *slog.Logger
	workerID  string
	leaseTTL  time.Duration
	now       func() time.Time
	activeMu  sync.Mutex
	active    map[string]context.CancelFunc
	lastError error
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
		active: map[string]context.CancelFunc{},
	}
}

func (d *Dispatcher) ActiveWorkers() int {
	d.activeMu.Lock()
	defer d.activeMu.Unlock()
	return len(d.active)
}

func (d *Dispatcher) Tick(ctx context.Context) error {
	now := d.now()
	if err := d.store.RecoverExpiredClaims(ctx, now); err != nil {
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
		claimed, err := d.store.ClaimRun(ctx, run.ID, d.workerID, now, d.leaseTTL)
		if err != nil || !claimed {
			continue
		}
		run.ClaimedByWorkerID = &d.workerID
		expires := now.Add(d.leaseTTL)
		run.ClaimExpiresAt = &expires
		go d.executeRun(context.Background(), schedule, run)
	}
	return nil
}

func (d *Dispatcher) prepareScheduleForClaim(ctx context.Context, schedule domain.Schedule, run domain.Run) (bool, error) {
	activeCount, err := d.store.ActiveRunCount(ctx, schedule.ID)
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
	if err := d.store.MarkRunRunning(ctx, run.ID, startedAt); err != nil {
		d.logger.Error("mark run running", "run_id", run.ID, "error", err)
		return
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(schedule.Policy.TimeoutSeconds)*time.Second)
	d.registerCancel(run.ID, cancel)
	defer func() {
		d.unregisterCancel(run.ID)
		cancel()
	}()

	group, heartbeatCtx := errgroup.WithContext(execCtx)
	group.Go(func() error {
		ticker := time.NewTicker(d.leaseTTL / 2)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return nil
			case <-ticker.C:
				if err := d.store.RenewLease(context.Background(), run.ID, d.workerID, d.now(), d.leaseTTL); err != nil {
					return err
				}
			}
		}
	})

	executor, ok := d.registry.Get(schedule.Target.Kind)
	if !ok {
		d.completeFailure(context.Background(), schedule, run, fmt.Errorf("executor %q not registered", schedule.Target.Kind), true)
		return
	}

	var result executors.Result
	group.Go(func() error {
		result = executor.Execute(execCtx, executors.ExecuteRequest{Schedule: schedule, Run: run, Timeout: schedule.Policy.TimeoutSeconds})
		return nil
	})

	if err := group.Wait(); err != nil {
		d.completeFailure(context.Background(), schedule, run, err, false)
		return
	}

	if result.ErrorText != "" {
		d.completeFailureWithResult(context.Background(), schedule, run, result)
		return
	}
	finishedAt := d.now()
	run.Status = domain.RunSucceeded
	run.ClaimExpiresAt = nil
	run.StartedAt = &startedAt
	run.FinishedAt = &finishedAt
	run.HTTPStatusCode = result.HTTPStatusCode
	run.ExitCode = result.ExitCode
	run.ResultJSON = result.ResultJSON
	run.UpdatedAt = finishedAt
	if err := d.store.FinishRun(context.Background(), run); err != nil {
		d.logger.Error("finish succeeded run", "run_id", run.ID, "error", err)
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
	schedule.LastRunAt = &finishedAt
	schedule.UpdatedAt = finishedAt
	_ = d.store.UpdateSchedule(context.Background(), schedule)
}

func (d *Dispatcher) completeFailure(ctx context.Context, schedule domain.Schedule, run domain.Run, err error, fatal bool) {
	result := executors.Result{ErrorText: err.Error()}
	if fatal {
		result.Cancelled = true
	}
	d.completeFailureWithResult(ctx, schedule, run, result)
}

func (d *Dispatcher) completeFailureWithResult(ctx context.Context, schedule domain.Schedule, run domain.Run, result executors.Result) {
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
	} else if shouldRetry(schedule, run.Attempt) {
		run.Status = domain.RunFailed
	} else {
		run.Status = domain.RunDeadLettered
	}
	if result.ErrorText != "" {
		run.ErrorText = &result.ErrorText
	}
	if err := d.store.FinishRun(ctx, run); err != nil {
		d.logger.Error("finish failed run", "run_id", run.ID, "error", err)
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
	if shouldRetry(schedule, run.Attempt) && !result.Cancelled {
		retryAt := backoffFor(schedule.Retry, run.Attempt, finishedAt)
		nextRun := domain.Run{
			ID:               domain.NewID("run"),
			ScheduleID:       run.ScheduleID,
			OccurrenceKey:    run.OccurrenceKey,
			NominalTime:      run.NominalTime,
			DueTime:          retryAt,
			Status:           domain.RunRetryScheduled,
			Attempt:          run.Attempt + 1,
			RetryAvailableAt: &retryAt,
			CreatedAt:        finishedAt,
			UpdatedAt:        finishedAt,
		}
		if err := d.store.InsertRun(context.Background(), nextRun); err != nil && err != store.ErrAlreadyExists {
			d.logger.Error("insert retry attempt", "run_id", run.ID, "error", err)
		}
		return
	}
	if run.Status == domain.RunDeadLettered {
		_ = d.store.InsertDeadLetter(context.Background(), domain.DeadLetter{
			ID:            domain.NewID("dlq"),
			RunID:         run.ID,
			ScheduleID:    run.ScheduleID,
			OccurrenceKey: run.OccurrenceKey,
			Reason:        result.ErrorText,
			PayloadJSON:   run.ResultJSON,
			CreatedAt:     finishedAt,
		})
	}
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
		delay *= 2
		if delay >= retry.MaxDelaySeconds {
			delay = retry.MaxDelaySeconds
			break
		}
	}
	return now.Add(time.Duration(delay) * time.Second).UTC()
}

func (d *Dispatcher) registerCancel(runID string, cancel context.CancelFunc) {
	d.activeMu.Lock()
	defer d.activeMu.Unlock()
	d.active[runID] = cancel
}

func (d *Dispatcher) unregisterCancel(runID string) {
	d.activeMu.Lock()
	defer d.activeMu.Unlock()
	delete(d.active, runID)
}

func (d *Dispatcher) cancelRun(runID string) {
	d.activeMu.Lock()
	cancel, ok := d.active[runID]
	d.activeMu.Unlock()
	if ok {
		cancel()
	}
}

func timePtrOr(current *time.Time, fallback time.Time) *time.Time {
	if current != nil {
		return current
	}
	return &fallback
}
