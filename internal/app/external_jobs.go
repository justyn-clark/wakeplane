package app

import (
	"context"
	"errors"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
	httpExec "github.com/justyn-clark/wakeplane/internal/executors/http"
	"github.com/justyn-clark/wakeplane/internal/store"
)

// ReconcileExternalJob refreshes authoritative remote status while retaining the
// original local run outcome (for example, a tracking timeout remains a timeout).
func (s *Service) ReconcileExternalJob(ctx context.Context, runID string) (domain.Run, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return domain.Run{}, err
	}
	if run.Status == domain.RunPending || run.Status == domain.RunClaimed || run.Status == domain.RunRunning || run.Status == domain.RunRetryScheduled {
		return domain.Run{}, domain.NewConflictError("active or retrying runs already own remote job tracking")
	}
	job := run.ExternalJob
	if job == nil || job.Compacted {
		return domain.Run{}, domain.NewBadRequestError("run has no unresolved external job identity")
	}
	if job.Terminal() {
		return run, nil
	}
	if !job.ReconciliationAvailable() {
		return domain.Run{}, domain.NewBadRequestError("run has no remote identity or configured lookup URL to reconcile")
	}
	observeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	observed, err := httpExec.New().Reconcile(observeCtx, run, *job)
	if err != nil {
		return domain.Run{}, &domain.APIError{Status: 502, Code: "runner_observation_failed", Message: err.Error()}
	}
	if err := s.store.ReconcileExternalJob(ctx, run, *job, observed, time.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return domain.Run{}, domain.NewConflictError("external job tracking changed during reconciliation; reload the run")
		}
		return domain.Run{}, err
	}
	return s.GetRun(ctx, runID)
}
