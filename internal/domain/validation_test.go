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

func TestExternalJobTargetValidation(t *testing.T) {
	base := CreateScheduleRequest{Name: "job", Timezone: "UTC", Schedule: ScheduleSpec{Kind: ScheduleKindInterval, EverySeconds: 60}, Target: TargetSpec{Kind: TargetKindHTTP, Method: "POST", URL: "https://runner.test/jobs", HTTPJob: &HTTPJobSpec{PollIntervalSeconds: 1}}, Policy: DefaultPolicy(), Retry: DefaultRetryPolicy()}
	if errs := ValidateCreateSchedule(base); len(errs) != 0 {
		t.Fatalf("valid job: %+v", errs)
	}
	for _, test := range []struct {
		name   string
		mutate func(*CreateScheduleRequest)
	}{
		{"empty timezone", func(r *CreateScheduleRequest) { r.Timezone = "" }},
		{"host timezone", func(r *CreateScheduleRequest) { r.Timezone = "Local" }},
		{"wrong method", func(r *CreateScheduleRequest) { r.Target.Method = "GET" }},
		{"wrong target", func(r *CreateScheduleRequest) { r.Target.Kind = TargetKindShell; r.Target.Command = "echo" }},
		{"relative URL", func(r *CreateScheduleRequest) { r.Target.URL = "/jobs" }},
		{"credential URL", func(r *CreateScheduleRequest) { r.Target.URL = "https://secret@runner.test/jobs" }},
		{"replace overlap", func(r *CreateScheduleRequest) { r.Policy.Overlap = OverlapReplace }},
		{"reserved field", func(r *CreateScheduleRequest) { r.Target.Body = map[string]any{"_wakeplane_event": "collision"} }},
		{"negative interval", func(r *CreateScheduleRequest) { r.Target.HTTPJob = &HTTPJobSpec{PollIntervalSeconds: -1} }},
		{"long interval", func(r *CreateScheduleRequest) { r.Target.HTTPJob = &HTTPJobSpec{PollIntervalSeconds: 301} }},
		{"overflow interval", func(r *CreateScheduleRequest) { r.Schedule.EverySeconds = int((1<<63-1)/int64(time.Second)) + 1 }},
		{"overflow timeout", func(r *CreateScheduleRequest) { r.Policy.TimeoutSeconds = int((1<<63-1)/int64(time.Second)) + 1 }},
		{"overflow backoff", func(r *CreateScheduleRequest) { r.Retry.MaxDelaySeconds = int((1<<63-1)/int64(time.Second)) + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := base
			test.mutate(&req)
			if errs := ValidateCreateSchedule(req); len(errs) == 0 {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestExternalJobLookupMustShareSubmissionOrigin(t *testing.T) {
	base := CreateScheduleRequest{Name: "job", Timezone: "UTC", Schedule: ScheduleSpec{Kind: ScheduleKindInterval, EverySeconds: 60}, Target: TargetSpec{Kind: TargetKindHTTP, Method: "POST", URL: "https://runner.test/jobs", HTTPJob: &HTTPJobSpec{}}, Policy: DefaultPolicy(), Retry: DefaultRetryPolicy()}
	for _, lookup := range []string{"https://runner.test/jobs/lookup", "https://RUNNER.test:443/lookup"} {
		req := base
		req.Target.HTTPJob = &HTTPJobSpec{LookupURL: lookup}
		if errs := ValidateCreateSchedule(req); len(errs) != 0 {
			t.Fatalf("same-origin lookup rejected: %s %+v", lookup, errs)
		}
	}
	for _, lookup := range []string{"/lookup", "https://evil.test/lookup", "http://runner.test/lookup", "https://secret@runner.test/lookup", "https://runner.test:444/lookup", "https://runner.test/lookup#fragment"} {
		req := base
		req.Target.HTTPJob = &HTTPJobSpec{LookupURL: lookup}
		if errs := ValidateCreateSchedule(req); len(errs) == 0 {
			t.Fatalf("unsafe lookup accepted: %s", lookup)
		}
	}
}
