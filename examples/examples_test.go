package examples_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/justyn-clark/wakeplane/internal/app"
	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestExampleManifestsParse(t *testing.T) {
	manifests, err := filepath.Glob("*.yaml")
	if err != nil {
		t.Fatalf("glob returned error: %v", err)
	}
	if len(manifests) == 0 {
		t.Fatal("expected at least one example manifest")
	}
	for _, path := range manifests {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read file: %v", err)
			}
			var req domain.CreateScheduleRequest
			if err := yaml.Unmarshal(data, &req); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if req.Name == "" {
				t.Fatal("expected name to be set")
			}
			if req.Schedule.Kind == "" {
				t.Fatal("expected schedule kind to be set")
			}
			if req.Target.Kind == "" {
				t.Fatal("expected target kind to be set")
			}
		})
	}
}

func TestEmbeddedServiceLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wakeplane.db")
	service, err := app.NewWithOptions(context.Background(), config.Config{
		DatabasePath:       dbPath,
		HTTPAddress:        "127.0.0.1:0",
		SchedulerInterval:  time.Second,
		DispatcherInterval: time.Second,
		LeaseTTL:           time.Second,
		WorkerID:           "wrk_example_test",
		Version:            "test",
	}, app.WithWorkflowHandler("sync.customers", func(ctx context.Context, input map[string]any) (map[string]any, error) {
		return map[string]any{"status": "completed", "source": input["source"]}, nil
	}))
	if err != nil {
		t.Fatalf("NewWithOptions returned error: %v", err)
	}

	// Create schedule from the nightly-sync manifest.
	data, err := os.ReadFile("nightly-sync.yaml")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var req domain.CreateScheduleRequest
	if err := yaml.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	schedule, errs, err := service.CreateSchedule(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateSchedule returned error: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("CreateSchedule returned validation errors: %+v", errs)
	}

	// Trigger a manual run and verify it is created.
	run, err := service.TriggerSchedule(context.Background(), schedule.ID, "smoke test")
	if err != nil {
		t.Fatalf("TriggerSchedule returned error: %v", err)
	}
	if run.Status != domain.RunPending {
		t.Fatalf("expected pending run, got %s", run.Status)
	}

	// Verify status is operational.
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Service != "wakeplane" {
		t.Fatalf("expected service name wakeplane, got %s", status.Service)
	}

	if err := service.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}
