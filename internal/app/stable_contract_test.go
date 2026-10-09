package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestCreateAndReplacePreservePartialPolicyAndRetry(t *testing.T) {
	ctx := context.Background()
	service, err := New(ctx, config.Config{DatabasePath: filepath.Join(t.TempDir(), "contract.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	req := domain.CreateScheduleRequest{
		Name: "partial-defaults", Timezone: "America/Los_Angeles",
		Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 60},
		Target:   domain.TargetSpec{Kind: domain.TargetKindHTTP, URL: "https://example.com/job", Method: "POST"},
		Policy:   domain.Policy{Overlap: domain.OverlapAllow, Misfire: domain.MisfireCatchUp, MaxConcurrency: 3},
		Retry:    domain.RetryPolicy{MaxAttempts: 2, InitialDelaySeconds: 7, MaxDelaySeconds: 60},
	}
	check := func(t *testing.T, got domain.Schedule) {
		t.Helper()
		wantPolicy := req.Policy
		wantPolicy.TimeoutSeconds = domain.DefaultPolicy().TimeoutSeconds
		wantRetry := req.Retry
		wantRetry.Strategy = domain.DefaultRetryPolicy().Strategy
		if got.Policy != wantPolicy {
			t.Errorf("explicit policy lost: got %+v, want %+v", got.Policy, wantPolicy)
		}
		if got.Retry != wantRetry {
			t.Errorf("explicit retry lost: got %+v, want %+v", got.Retry, wantRetry)
		}
	}
	created, errs, err := service.CreateSchedule(ctx, req)
	if err != nil || len(errs) > 0 {
		t.Fatalf("create: %v %+v", err, errs)
	}
	t.Run("create", func(t *testing.T) { check(t, created) })
	req.Name = "partial-replacement"
	replaced, errs, err := service.ReplaceSchedule(ctx, created.ID, req)
	if err != nil || len(errs) > 0 {
		t.Fatalf("replace: %v %+v", err, errs)
	}
	t.Run("replace", func(t *testing.T) { check(t, replaced) })
	stored, err := service.GetSchedule(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("persisted", func(t *testing.T) { check(t, stored) })
	req.Policy.Overlap = "invalid"
	if _, errs, err = service.CreateSchedule(ctx, req); err != nil || len(errs) == 0 {
		t.Fatalf("invalid partial policy accepted: %v %+v", err, errs)
	}
	req.Policy = domain.Policy{}
	req.Retry.MaxAttempts = -1
	if _, errs, err = service.CreateSchedule(ctx, req); err != nil || len(errs) == 0 {
		t.Fatalf("invalid partial retry accepted: %v %+v", err, errs)
	}
}
