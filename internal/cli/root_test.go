package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/app"
	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestRunServeCancelsBlockingWorkflowOnShutdown(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")
	started := make(chan struct{})
	finished := make(chan struct{})
	addrCh := make(chan string, 1)
	serviceCh := make(chan *app.Service, 1)
	var once sync.Once

	cfg := config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  10 * time.Millisecond,
		DispatcherInterval: 10 * time.Millisecond,
		LeaseTTL:           200 * time.Millisecond,
		WorkerID:           "wrk_cli",
		Version:            "test",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- runServe(ctx, cfg, func(ctx context.Context, cfg config.Config) (*app.Service, error) {
			service, err := app.NewWithOptions(ctx, cfg, app.WithWorkflowHandler("blocking.workflow", func(ctx context.Context, input map[string]any) (map[string]any, error) {
				once.Do(func() { close(started) })
				<-ctx.Done()
				close(finished)
				return nil, ctx.Err()
			}))
			if err == nil {
				serviceCh <- service
			}
			return service, err
		}, serveHooks{onListening: func(addr string) {
			addrCh <- addr
		}})
	}()

	service := <-serviceCh
	addr := <-addrCh

	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		t.Fatalf("NewRequest returned error: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("health request returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected healthz 200, got %d", resp.StatusCode)
	}

	schedule, errs, err := service.CreateSchedule(context.Background(), domain.CreateScheduleRequest{
		Name:     "daemon-blocking-workflow",
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

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for workflow execution to start")
	}
	if err := waitForRunStatus(service, run.ID, domain.RunRunning, 2*time.Second); err != nil {
		t.Fatalf("waitForRunStatus returned error: %v", err)
	}

	cancel()

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for workflow handler cancellation")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runServe returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runServe to exit")
	}

	reopened, err := app.New(context.Background(), config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: time.Second,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_reopen",
		Version:            "test",
	})
	if err != nil {
		t.Fatalf("reopened app.New returned error: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if got.Status != domain.RunCancelled {
		t.Fatalf("expected cancelled run after serve shutdown, got %s", got.Status)
	}
	if got.FinishedAt == nil {
		t.Fatalf("expected finished_at after serve shutdown")
	}
}

func TestVersionCommandPrintsVersion(t *testing.T) {
	cmd := NewRootCmd("0.2.0-beta.1")
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if got := stdout.String(); got != "0.2.0-beta.1\n" {
		t.Fatalf("expected version output, got %q", got)
	}
}

func waitForRunStatus(service *app.Service, runID string, want domain.RunStatus, timeout time.Duration) error {
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
	body, _ := json.Marshal(run)
	return errors.New(string(body))
}
