package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestServiceCreateTriggerAndInspectRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")
	service, err := NewWithOptions(context.Background(), config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: 100 * time.Millisecond,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_test",
		Version:            "test",
	}, WithWorkflowHandler("sync.customers", func(ctx context.Context, input map[string]any) (map[string]any, error) {
		return map[string]any{"status": "completed", "input": input}, nil
	}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer service.Close()

	schedule, errs, err := service.CreateSchedule(context.Background(), domain.CreateScheduleRequest{
		Name:     "nightly-sync",
		Enabled:  true,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindWorkflow, WorkflowID: "sync.customers", Input: map[string]any{"source": "crm"}},
		Policy:   domain.DefaultPolicy(),
		Retry:    domain.DefaultRetryPolicy(),
	})
	if err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("CreateSchedule returned validation errors: %+v", errs)
	}

	run, err := service.TriggerSchedule(context.Background(), schedule.ID, "manual operator trigger")
	if err != nil {
		t.Fatalf("TriggerSchedule returned error: %v", err)
	}
	got, err := service.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if got.OccurrenceKey[:7] != "manual:" {
		t.Fatalf("expected manual occurrence key, got %s", got.OccurrenceKey)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected sqlite database at %s: %v", dbPath, err)
	}
}

func TestManualTriggerDoesNotCollideWithScheduledOccurrenceIdentity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")
	service, err := New(context.Background(), config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: time.Second,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_test",
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer service.Close()

	schedule, errs, err := service.CreateSchedule(context.Background(), domain.CreateScheduleRequest{
		Name:     "collision-check",
		Enabled:  true,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindShell, Command: "/bin/echo"},
		Policy:   domain.DefaultPolicy(),
		Retry:    domain.DefaultRetryPolicy(),
	})
	if err != nil || len(errs) > 0 {
		t.Fatalf("CreateSchedule failed: %v %+v", err, errs)
	}
	manual, err := service.TriggerSchedule(context.Background(), schedule.ID, "manual operator trigger")
	if err != nil {
		t.Fatalf("TriggerSchedule returned error: %v", err)
	}
	if schedule.NextRunAt == nil {
		t.Fatalf("expected next_run_at to be set")
	}
	scheduledKey := domain.OccurrenceKey(schedule.ID, *schedule.NextRunAt)
	if manual.OccurrenceKey == scheduledKey {
		t.Fatalf("manual occurrence key collided with scheduled occurrence key")
	}
}

func TestStatusIncludesOperationalCounts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")
	service, err := New(context.Background(), config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: 100 * time.Millisecond,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_test",
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer service.Close()

	schedule, errs, err := service.CreateSchedule(context.Background(), domain.CreateScheduleRequest{
		Name:     "status-check",
		Enabled:  true,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindShell, Command: "/bin/echo"},
		Policy:   domain.DefaultPolicy(),
		Retry:    domain.DefaultRetryPolicy(),
	})
	if err != nil || len(errs) > 0 {
		t.Fatalf("CreateSchedule failed: %v %+v", err, errs)
	}
	if _, err := service.TriggerSchedule(context.Background(), schedule.ID, "manual operator trigger"); err != nil {
		t.Fatalf("TriggerSchedule returned error: %v", err)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Scheduler.DueRuns == 0 {
		t.Fatalf("expected due runs to be > 0")
	}
	if status.Scheduler.NextDueScheduleID == "" {
		t.Fatalf("expected next due schedule id to be populated")
	}
}

func TestPausedSchedulePersistsAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")
	service, err := New(context.Background(), config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: time.Second,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_test",
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	schedule, errs, err := service.CreateSchedule(context.Background(), domain.CreateScheduleRequest{
		Name:     "pause-check",
		Enabled:  true,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindShell, Command: "/bin/echo"},
		Policy:   domain.DefaultPolicy(),
		Retry:    domain.DefaultRetryPolicy(),
	})
	if err != nil || len(errs) > 0 {
		t.Fatalf("CreateSchedule failed: %v %+v", err, errs)
	}
	if _, err := service.PauseSchedule(context.Background(), schedule.ID); err != nil {
		t.Fatalf("PauseSchedule returned error: %v", err)
	}
	_ = service.Close()

	reopened, err := New(context.Background(), config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: time.Second,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_test",
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("reopened New returned error: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.GetSchedule(context.Background(), schedule.ID)
	if err != nil {
		t.Fatalf("GetSchedule returned error: %v", err)
	}
	if got.Enabled {
		t.Fatalf("expected paused schedule to remain disabled after restart")
	}
	if got.PausedAt == nil {
		t.Fatalf("expected paused_at to persist after restart")
	}
}

func TestServiceHasNoDefaultWorkflowHandlers(t *testing.T) {
	service, err := New(context.Background(), config.Config{
		DatabasePath:       filepath.Join(t.TempDir(), "wakeplane.db"),
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: time.Second,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_test",
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer service.Close()

	if _, err := service.workflowRegistry.Execute(context.Background(), "sync.customers", map[string]any{}); err == nil {
		t.Fatalf("expected no default workflow handler to be registered")
	}
}

func TestBlockingWorkflowRunIsCancelledAndRecoveredAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")
	started := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	service, err := NewWithOptions(context.Background(), config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  10 * time.Millisecond,
		DispatcherInterval: 10 * time.Millisecond,
		LeaseTTL:           200 * time.Millisecond,
		WorkerID:           "wrk_test",
		Version:            "test",
	}, WithWorkflowHandler("blocking.workflow", func(ctx context.Context, input map[string]any) (map[string]any, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	}))
	if err != nil {
		t.Fatalf("NewWithOptions returned error: %v", err)
	}

	schedule, errs, err := service.CreateSchedule(context.Background(), domain.CreateScheduleRequest{
		Name:     "blocking-workflow",
		Enabled:  false,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindWorkflow, WorkflowID: "blocking.workflow"},
		Policy:   domain.DefaultPolicy(),
		Retry:    domain.DefaultRetryPolicy(),
	})
	if err != nil || len(errs) > 0 {
		t.Fatalf("CreateSchedule failed: %v %+v", err, errs)
	}
	run, err := service.TriggerSchedule(context.Background(), schedule.ID, "manual operator trigger")
	if err != nil {
		t.Fatalf("TriggerSchedule returned error: %v", err)
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- service.Run(runCtx)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for workflow execution to start")
	}

	if err := waitForRunStatus(service, run.ID, domain.RunRunning, 2*time.Second); err != nil {
		t.Fatalf("waitForRunStatus running returned error: %v", err)
	}

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelClose()
	if err := service.CloseContext(closeCtx); err != nil {
		t.Fatalf("CloseContext returned error: %v", err)
	}

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for workflow handler cancellation")
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for service run loop to stop")
	}

	reopened, err := New(context.Background(), config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: time.Second,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_reopen",
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("reopened New returned error: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if got.Status != domain.RunCancelled {
		t.Fatalf("expected cancelled run after shutdown, got %s", got.Status)
	}
	if got.FinishedAt == nil {
		t.Fatalf("expected finished_at to be recorded after shutdown")
	}
	status, err := reopened.Status(context.Background())
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Workers.Active != 0 {
		t.Fatalf("expected no active workers after restart, got %d", status.Workers.Active)
	}
	if status.Runs.Running != 0 {
		t.Fatalf("expected no running runs after restart, got %d", status.Runs.Running)
	}
}

func TestCloseContextTimesOutWhenWorkflowDelaysCancellation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")
	started := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	service, err := NewWithOptions(context.Background(), config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  10 * time.Millisecond,
		DispatcherInterval: 10 * time.Millisecond,
		LeaseTTL:           200 * time.Millisecond,
		WorkerID:           "wrk_test",
		Version:            "test",
	}, WithWorkflowHandler("slow.cancel", func(ctx context.Context, input map[string]any) (map[string]any, error) {
		once.Do(func() { close(started) })
		time.Sleep(250 * time.Millisecond)
		close(finished)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return map[string]any{"status": "completed"}, nil
	}))
	if err != nil {
		t.Fatalf("NewWithOptions returned error: %v", err)
	}
	defer func() {
		_ = service.Close()
	}()

	schedule, errs, err := service.CreateSchedule(context.Background(), domain.CreateScheduleRequest{
		Name:     "slow-cancel-workflow",
		Enabled:  false,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindWorkflow, WorkflowID: "slow.cancel"},
		Policy:   domain.DefaultPolicy(),
		Retry:    domain.DefaultRetryPolicy(),
	})
	if err != nil || len(errs) > 0 {
		t.Fatalf("CreateSchedule failed: %v %+v", err, errs)
	}
	run, err := service.TriggerSchedule(context.Background(), schedule.ID, "manual operator trigger")
	if err != nil {
		t.Fatalf("TriggerSchedule returned error: %v", err)
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- service.Run(runCtx)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for workflow execution to start")
	}
	if err := waitForRunStatus(service, run.ID, domain.RunRunning, 2*time.Second); err != nil {
		t.Fatalf("waitForRunStatus running returned error: %v", err)
	}

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelClose()
	if err := service.CloseContext(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected CloseContext deadline exceeded, got %v", err)
	}

	ready := service.Ready(context.Background())
	if ok, _ := ready["ok"].(bool); !ok {
		t.Fatalf("expected store to remain open after timed-out CloseContext, got %+v", ready)
	}

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delayed workflow completion")
	}
	if err := waitForRunStatus(service, run.ID, domain.RunCancelled, 2*time.Second); err != nil {
		t.Fatalf("waitForRunStatus cancelled returned error: %v", err)
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for service run loop to stop")
	}
}

func TestCloseContextTimesOutWithNonCooperativeExecutor(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	service, err := NewWithOptions(context.Background(), config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  10 * time.Millisecond,
		DispatcherInterval: 10 * time.Millisecond,
		LeaseTTL:           200 * time.Millisecond,
		WorkerID:           "wrk_test",
		Version:            "test",
	}, WithWorkflowHandler("noncooperative.workflow", func(ctx context.Context, input map[string]any) (map[string]any, error) {
		once.Do(func() { close(started) })
		// Deliberately ignore ctx.Done(); only unblock when the test releases us.
		<-release
		return nil, errors.New("released by test teardown")
	}))
	if err != nil {
		t.Fatalf("NewWithOptions returned error: %v", err)
	}
	defer func() {
		close(release)
		// Allow background goroutines to drain after release.
		time.Sleep(50 * time.Millisecond)
	}()

	schedule, errs, err := service.CreateSchedule(context.Background(), domain.CreateScheduleRequest{
		Name:     "noncooperative-workflow",
		Enabled:  false,
		Timezone: "UTC",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindWorkflow, WorkflowID: "noncooperative.workflow"},
		Policy:   domain.DefaultPolicy(),
		Retry:    domain.DefaultRetryPolicy(),
	})
	if err != nil || len(errs) > 0 {
		t.Fatalf("CreateSchedule failed: %v %+v", err, errs)
	}
	run, err := service.TriggerSchedule(context.Background(), schedule.ID, "manual operator trigger")
	if err != nil {
		t.Fatalf("TriggerSchedule returned error: %v", err)
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- service.Run(runCtx)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for workflow execution to start")
	}
	if err := waitForRunStatus(service, run.ID, domain.RunRunning, 2*time.Second); err != nil {
		t.Fatalf("waitForRunStatus running returned error: %v", err)
	}

	// Use a very short timeout. The workflow will NOT honor cancellation.
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelClose()
	closeErr := service.CloseContext(closeCtx)
	if !errors.Is(closeErr, context.DeadlineExceeded) {
		t.Fatalf("expected CloseContext deadline exceeded for non-cooperative executor, got %v", closeErr)
	}

	// Store must remain open even though shutdown timed out (store.Close was never reached).
	ready := service.Ready(context.Background())
	if ok, _ := ready["ok"].(bool); !ok {
		t.Fatalf("expected store to remain open after timed-out CloseContext, got %+v", ready)
	}

	// The run should still show as running since the executor never cooperated.
	got, err := service.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if got.Status != domain.RunRunning {
		t.Fatalf("expected run to remain running after non-cooperative shutdown timeout, got %s", got.Status)
	}
}

func waitForRunStatus(service *Service, runID string, want domain.RunStatus, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := service.GetRun(context.Background(), runID)
		if err != nil {
			return err
		}
		if run.Status == want {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, err := service.GetRun(context.Background(), runID)
	if err != nil {
		return err
	}
	return errors.New("last observed run status: " + string(run.Status))
}
