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
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		errs = append(errs, ValidationError{Field: "timezone", Message: "must be a valid IANA timezone"})
	}
	errs = append(errs, validateScheduleSpec(req.Schedule)...)
	errs = append(errs, validateTargetSpec(req.Target)...)
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
