package domain

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	cronlib "github.com/robfig/cron/v3"
)

var cronParser = cronlib.NewParser(cronlib.Minute | cronlib.Hour | cronlib.Dom | cronlib.Month | cronlib.Dow)

func ValidateCreateSchedule(req CreateScheduleRequest) []ValidationError {
	var errs []ValidationError
	if strings.TrimSpace(req.Name) == "" {
		errs = append(errs, ValidationError{Field: "name", Message: "must be non-empty"})
	}
	if _, err := time.LoadLocation(req.Timezone); strings.TrimSpace(req.Timezone) == "" || req.Timezone == "Local" || err != nil {
		errs = append(errs, ValidationError{Field: "timezone", Message: "must be a valid IANA timezone"})
	}
	errs = append(errs, validateScheduleSpec(req.Schedule)...)
	errs = append(errs, validateTargetSpec(req.Target)...)
	if req.Target.HTTPJob != nil && req.Policy.Overlap == OverlapReplace {
		errs = append(errs, ValidationError{Field: "policy.overlap", Message: "replace is unsupported for external jobs without remote cancellation"})
	}
	errs = append(errs, validatePolicy(req.Policy)...)
	errs = append(errs, validateRetry(req.Retry)...)
	if req.StartAt != nil && req.EndAt != nil && req.StartAt.After(*req.EndAt) {
		errs = append(errs, ValidationError{Field: "start_at", Message: "must be <= end_at"})
	}
	return errs
}

func ValidatePatch(current Schedule, patch PatchScheduleRequest) []ValidationError {
	next := ApplyPatch(current, patch)
	return ValidateCreateSchedule(CreateScheduleRequest{
		Name:     next.Name,
		Enabled:  next.Enabled,
		Timezone: next.Timezone,
		Schedule: next.Schedule,
		Target:   next.Target,
		Policy:   next.Policy,
		Retry:    next.Retry,
		StartAt:  next.StartAt,
		EndAt:    next.EndAt,
	})
}

func ApplyPatch(current Schedule, patch PatchScheduleRequest) Schedule {
	next := current
	if patch.Name != nil {
		next.Name = *patch.Name
	}
	if patch.Enabled != nil {
		next.Enabled = *patch.Enabled
	}
	if patch.Timezone != nil {
		next.Timezone = *patch.Timezone
	}
	if patch.Schedule != nil {
		next.Schedule = *patch.Schedule
	}
	if patch.Target != nil {
		next.Target = *patch.Target
	}
	if patch.Policy != nil {
		if patch.Policy.Overlap != nil {
			next.Policy.Overlap = *patch.Policy.Overlap
		}
		if patch.Policy.Misfire != nil {
			next.Policy.Misfire = *patch.Policy.Misfire
		}
		if patch.Policy.TimeoutSeconds != nil {
			next.Policy.TimeoutSeconds = *patch.Policy.TimeoutSeconds
		}
		if patch.Policy.MaxConcurrency != nil {
			next.Policy.MaxConcurrency = *patch.Policy.MaxConcurrency
		}
	}
	if patch.Retry != nil {
		if patch.Retry.MaxAttempts != nil {
			next.Retry.MaxAttempts = *patch.Retry.MaxAttempts
		}
		if patch.Retry.Strategy != nil {
			next.Retry.Strategy = *patch.Retry.Strategy
		}
		if patch.Retry.InitialDelaySeconds != nil {
			next.Retry.InitialDelaySeconds = *patch.Retry.InitialDelaySeconds
		}
		if patch.Retry.MaxDelaySeconds != nil {
			next.Retry.MaxDelaySeconds = *patch.Retry.MaxDelaySeconds
		}
	}
	if patch.StartAt != nil {
		next.StartAt = *patch.StartAt
	}
	if patch.EndAt != nil {
		next.EndAt = *patch.EndAt
	}
	return next
}

func validateScheduleSpec(spec ScheduleSpec) []ValidationError {
	switch spec.Kind {
	case ScheduleKindCron:
		if strings.TrimSpace(spec.Expr) == "" {
			return []ValidationError{{Field: "schedule.expr", Message: "is required for cron"}}
		}
		if _, err := cronParser.Parse(spec.Expr); err != nil {
			return []ValidationError{{Field: "schedule.expr", Message: "must parse as a cron expression"}}
		}
	case ScheduleKindInterval:
		if spec.EverySeconds <= 0 {
			return []ValidationError{{Field: "schedule.every_seconds", Message: "must be > 0"}}
		}
		if !safeDurationSeconds(spec.EverySeconds) {
			return []ValidationError{{Field: "schedule.every_seconds", Message: "exceeds the supported duration"}}
		}
	case ScheduleKindOnce:
		if spec.At == nil {
			return []ValidationError{{Field: "schedule.at", Message: "is required for once"}}
		}
	default:
		return []ValidationError{{Field: "schedule.kind", Message: "must be one of cron, interval, once"}}
	}
	return nil
}

func validateTargetSpec(spec TargetSpec) []ValidationError {
	if spec.HTTPJob != nil && spec.Kind != TargetKindHTTP {
		return []ValidationError{{Field: "target.http_job", Message: "is only supported for http targets"}}
	}
	switch spec.Kind {
	case TargetKindHTTP:
		var errs []ValidationError
		if strings.TrimSpace(spec.Method) == "" {
			errs = append(errs, ValidationError{Field: "target.method", Message: "is required for http"})
		} else if _, ok := httpMethods[strings.ToUpper(spec.Method)]; !ok {
			errs = append(errs, ValidationError{Field: "target.method", Message: "must be a valid HTTP method"})
		}
		if strings.TrimSpace(spec.URL) == "" {
			errs = append(errs, ValidationError{Field: "target.url", Message: "is required for http"})
		} else if _, err := url.ParseRequestURI(spec.URL); err != nil {
			errs = append(errs, ValidationError{Field: "target.url", Message: "must be a valid URL"})
		}
		if spec.HTTPJob != nil {
			if strings.ToUpper(spec.Method) != http.MethodPost {
				errs = append(errs, ValidationError{Field: "target.method", Message: "must be POST for external jobs"})
			}
			parsed, err := url.Parse(spec.URL)
			if err != nil || parsed.Host == "" || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
				errs = append(errs, ValidationError{Field: "target.url", Message: "external jobs require an absolute HTTP(S) URL without credentials or a fragment"})
			}
			if spec.HTTPJob.LookupURL != "" && !sameHTTPOrigin(spec.URL, spec.HTTPJob.LookupURL) {
				errs = append(errs, ValidationError{Field: "target.http_job.lookup_url", Message: "must be an absolute HTTP(S) URL with the same origin as target.url, without credentials or a fragment"})
			}
			if spec.HTTPJob.PollIntervalSeconds < 0 || spec.HTTPJob.PollIntervalSeconds > 300 {
				errs = append(errs, ValidationError{Field: "target.http_job.poll_interval_seconds", Message: "must be 0 (default) or between 1 and 300"})
			}
			if _, reserved := spec.Body["_wakeplane_event"]; reserved {
				errs = append(errs, ValidationError{Field: "target.body._wakeplane_event", Message: "is reserved for the event envelope"})
			}
		}
		return errs
	case TargetKindShell:
		if strings.TrimSpace(spec.Command) == "" {
			return []ValidationError{{Field: "target.command", Message: "is required for shell"}}
		}
	case TargetKindWorkflow:
		if strings.TrimSpace(spec.WorkflowID) == "" {
			return []ValidationError{{Field: "target.workflow_id", Message: "is required for workflow"}}
		}
	default:
		return []ValidationError{{Field: "target.kind", Message: "must be one of http, shell, workflow"}}
	}
	return nil
}

func validatePolicy(policy Policy) []ValidationError {
	var errs []ValidationError
	switch policy.Overlap {
	case OverlapAllow, OverlapForbid, OverlapQueueLatest, OverlapReplace:
	default:
		errs = append(errs, ValidationError{Field: "policy.overlap", Message: "must be a valid enum"})
	}
	switch policy.Misfire {
	case MisfireSkip, MisfireRunOnceIfLate, MisfireCatchUp:
	default:
		errs = append(errs, ValidationError{Field: "policy.misfire", Message: "must be a valid enum"})
	}
	if policy.TimeoutSeconds <= 0 {
		errs = append(errs, ValidationError{Field: "policy.timeout_seconds", Message: "must be > 0"})
	}
	if !safeDurationSeconds(policy.TimeoutSeconds) {
		errs = append(errs, ValidationError{Field: "policy.timeout_seconds", Message: "exceeds the supported duration"})
	}
	if policy.MaxConcurrency < 1 {
		errs = append(errs, ValidationError{Field: "policy.max_concurrency", Message: "must be >= 1"})
	}
	return errs
}

func validateRetry(retry RetryPolicy) []ValidationError {
	var errs []ValidationError
	if retry.MaxAttempts < 0 {
		errs = append(errs, ValidationError{Field: "retry.max_attempts", Message: "must be >= 0"})
	}
	if !safeDurationSeconds(retry.InitialDelaySeconds) || !safeDurationSeconds(retry.MaxDelaySeconds) {
		errs = append(errs, ValidationError{Field: "retry", Message: "delay exceeds the supported duration"})
	}
	switch retry.Strategy {
	case RetryNone, RetryExponential:
	default:
		errs = append(errs, ValidationError{Field: "retry.strategy", Message: "must be one of none, exponential"})
	}
	if retry.Strategy == RetryExponential {
		if retry.InitialDelaySeconds <= 0 {
			errs = append(errs, ValidationError{Field: "retry.initial_delay_seconds", Message: "must be > 0"})
		}
		if retry.MaxDelaySeconds <= 0 {
			errs = append(errs, ValidationError{Field: "retry.max_delay_seconds", Message: "must be > 0"})
		}
		if retry.MaxDelaySeconds < retry.InitialDelaySeconds {
			errs = append(errs, ValidationError{Field: "retry.max_delay_seconds", Message: "must be >= initial_delay_seconds"})
		}
	}
	return errs
}

func safeDurationSeconds(seconds int) bool {
	return int64(seconds) <= int64((1<<63-1)/int64(time.Second))
}

func sameHTTPOrigin(left, right string) bool {
	a, errA := url.Parse(left)
	b, errB := url.Parse(right)
	if errA != nil || errB != nil || a.Hostname() == "" || b.Hostname() == "" || a.User != nil || b.User != nil || a.Fragment != "" || b.Fragment != "" || (a.Scheme != "http" && a.Scheme != "https") || a.Scheme != b.Scheme {
		return false
	}
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func RequireNoValidationErrors(errs []ValidationError) error {
	if len(errs) == 0 {
		return nil
	}
	return errors.New("validation failed")
}

func DefaultPolicy() Policy {
	return Policy{
		Overlap:        OverlapForbid,
		Misfire:        MisfireRunOnceIfLate,
		TimeoutSeconds: 300,
		MaxConcurrency: 1,
	}
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:         0,
		Strategy:            RetryExponential,
		InitialDelaySeconds: 30,
		MaxDelaySeconds:     900,
	}
}

func ValidateTriggerReason(reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("reason must be non-empty")
	}
	return nil
}

var httpMethods = map[string]struct{}{
	http.MethodDelete:  {},
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodOptions: {},
	http.MethodPatch:   {},
	http.MethodPost:    {},
	http.MethodPut:     {},
}
