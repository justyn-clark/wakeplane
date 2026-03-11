package domain

import (
	"testing"
	"time"
)

func TestValidateCreateSchedule(t *testing.T) {
	now := time.Now().UTC()
	req := CreateScheduleRequest{
		Name:     "",
		Enabled:  true,
		Timezone: "bad/tz",
		Schedule: ScheduleSpec{Kind: ScheduleKindCron, Expr: "bad cron"},
		Target:   TargetSpec{Kind: TargetKindHTTP, Method: "", URL: "://bad"},
		Policy:   Policy{Overlap: "bad", Misfire: "bad", TimeoutSeconds: 0, MaxConcurrency: 0},
		Retry:    RetryPolicy{MaxAttempts: -1, Strategy: "bad", InitialDelaySeconds: -1, MaxDelaySeconds: -1},
		StartAt:  &now,
		EndAt:    &now,
	}

	errs := ValidateCreateSchedule(req)
	if len(errs) < 8 {
		t.Fatalf("expected validation errors, got %d", len(errs))
	}
}

func TestApplyPatch(t *testing.T) {
	current := Schedule{
		Name:     "nightly-sync",
		Enabled:  true,
		Timezone: "UTC",
		Schedule: ScheduleSpec{Kind: ScheduleKindCron, Expr: "0 2 * * *"},
		Target:   TargetSpec{Kind: TargetKindWorkflow, WorkflowID: "sync.customers"},
		Policy:   DefaultPolicy(),
		Retry:    DefaultRetryPolicy(),
	}
	name := "new-name"
	timeout := 900
	patch := PatchScheduleRequest{
		Name: &name,
		Policy: &PatchPolicy{
			TimeoutSeconds: &timeout,
		},
	}
	next := ApplyPatch(current, patch)
	if next.Name != name {
		t.Fatalf("expected patched name %q, got %q", name, next.Name)
	}
	if next.Policy.TimeoutSeconds != timeout {
		t.Fatalf("expected timeout %d, got %d", timeout, next.Policy.TimeoutSeconds)
	}
}
