package domain

import (
	"encoding/json"
	"time"
)

type ScheduleKind string

const (
	ScheduleKindCron     ScheduleKind = "cron"
	ScheduleKindInterval ScheduleKind = "interval"
	ScheduleKindOnce     ScheduleKind = "once"
)

type TargetKind string

const (
	TargetKindHTTP     TargetKind = "http"
	TargetKindShell    TargetKind = "shell"
	TargetKindWorkflow TargetKind = "workflow"
)

type OverlapPolicy string

const (
	OverlapAllow       OverlapPolicy = "allow"
	OverlapForbid      OverlapPolicy = "forbid"
	OverlapQueueLatest OverlapPolicy = "queue_latest"
	OverlapReplace     OverlapPolicy = "replace"
)

type MisfirePolicy string

const (
	MisfireSkip          MisfirePolicy = "skip"
	MisfireRunOnceIfLate MisfirePolicy = "run_once_if_late"
	MisfireCatchUp       MisfirePolicy = "catch_up"
)

type RetryStrategy string

const (
	RetryNone        RetryStrategy = "none"
	RetryExponential RetryStrategy = "exponential"
)

type RunStatus string

const (
	RunPending        RunStatus = "pending"
	RunClaimed        RunStatus = "claimed"
	RunRunning        RunStatus = "running"
	RunSucceeded      RunStatus = "succeeded"
	RunFailed         RunStatus = "failed"
	RunRetryScheduled RunStatus = "retry_scheduled"
	RunDeadLettered   RunStatus = "dead_lettered"
	RunCancelled      RunStatus = "cancelled"
	RunSkipped        RunStatus = "skipped"
)

type ScheduleSpec struct {
	Kind         ScheduleKind `json:"kind" yaml:"kind"`
	Expr         string       `json:"expr,omitempty" yaml:"expr,omitempty"`
	EverySeconds int          `json:"every_seconds,omitempty" yaml:"every_seconds,omitempty"`
	AnchorAt     *time.Time   `json:"anchor_at,omitempty" yaml:"anchor_at,omitempty"`
	At           *time.Time   `json:"at,omitempty" yaml:"at,omitempty"`
}

type TargetSpec struct {
	Kind       TargetKind        `json:"kind" yaml:"kind"`
	Method     string            `json:"method,omitempty" yaml:"method,omitempty"`
	URL        string            `json:"url,omitempty" yaml:"url,omitempty"`
	Headers    map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Body       map[string]any    `json:"body,omitempty" yaml:"body,omitempty"`
	Command    string            `json:"command,omitempty" yaml:"command,omitempty"`
	Args       []string          `json:"args,omitempty" yaml:"args,omitempty"`
	WorkflowID string            `json:"workflow_id,omitempty" yaml:"workflow_id,omitempty"`
	Input      map[string]any    `json:"input,omitempty" yaml:"input,omitempty"`
}

type Policy struct {
	Overlap        OverlapPolicy `json:"overlap" yaml:"overlap"`
	Misfire        MisfirePolicy `json:"misfire" yaml:"misfire"`
	TimeoutSeconds int           `json:"timeout_seconds" yaml:"timeout_seconds"`
	MaxConcurrency int           `json:"max_concurrency" yaml:"max_concurrency"`
}

type RetryPolicy struct {
	MaxAttempts         int           `json:"max_attempts" yaml:"max_attempts"`
	Strategy            RetryStrategy `json:"strategy" yaml:"strategy"`
	InitialDelaySeconds int           `json:"initial_delay_seconds" yaml:"initial_delay_seconds"`
	MaxDelaySeconds     int           `json:"max_delay_seconds" yaml:"max_delay_seconds"`
}

type Schedule struct {
	ID        string       `json:"id"`
	Name      string       `json:"name" yaml:"name"`
	Enabled   bool         `json:"enabled" yaml:"enabled"`
	Timezone  string       `json:"timezone" yaml:"timezone"`
	Schedule  ScheduleSpec `json:"schedule" yaml:"schedule"`
	Target    TargetSpec   `json:"target" yaml:"target"`
	Policy    Policy       `json:"policy" yaml:"policy"`
	Retry     RetryPolicy  `json:"retry" yaml:"retry"`
	StartAt   *time.Time   `json:"start_at"`
	EndAt     *time.Time   `json:"end_at"`
	PausedAt  *time.Time   `json:"paused_at"`
	NextRunAt *time.Time   `json:"next_run_at"`
	LastRunAt *time.Time   `json:"last_run_at"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

type Receipt struct {
	ID          string    `json:"id"`
	RunID       string    `json:"run_id,omitempty"`
	ReceiptKind string    `json:"receipt_kind"`
	ContentType string    `json:"content_type"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"created_at"`
}

type Run struct {
	ID                string          `json:"id"`
	ScheduleID        string          `json:"schedule_id"`
	OccurrenceKey     string          `json:"occurrence_key"`
	NominalTime       time.Time       `json:"nominal_time"`
	DueTime           time.Time       `json:"due_time"`
	Status            RunStatus       `json:"status"`
	Attempt           int             `json:"attempt"`
	ClaimedByWorkerID *string         `json:"claimed_by_worker_id,omitempty"`
	ClaimExpiresAt    *time.Time      `json:"claim_expires_at,omitempty"`
	StartedAt         *time.Time      `json:"started_at,omitempty"`
	FinishedAt        *time.Time      `json:"finished_at,omitempty"`
	HTTPStatusCode    *int            `json:"http_status_code,omitempty"`
	ExitCode          *int            `json:"exit_code,omitempty"`
	ResultJSON        json.RawMessage `json:"result_json,omitempty"`
	ErrorText         *string         `json:"error_text,omitempty"`
	RetryAvailableAt  *time.Time      `json:"retry_available_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	Receipts          []Receipt       `json:"receipts,omitempty"`
}

type DeadLetter struct {
	ID            string          `json:"id"`
	RunID         string          `json:"run_id"`
	ScheduleID    string          `json:"schedule_id"`
	OccurrenceKey string          `json:"occurrence_key"`
	Reason        string          `json:"reason"`
	PayloadJSON   json.RawMessage `json:"payload_json,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

type CreateScheduleRequest struct {
	Name     string       `json:"name" yaml:"name"`
	Enabled  bool         `json:"enabled" yaml:"enabled"`
	Timezone string       `json:"timezone" yaml:"timezone"`
	Schedule ScheduleSpec `json:"schedule" yaml:"schedule"`
	Target   TargetSpec   `json:"target" yaml:"target"`
	Policy   Policy       `json:"policy" yaml:"policy"`
	Retry    RetryPolicy  `json:"retry" yaml:"retry"`
	StartAt  *time.Time   `json:"start_at" yaml:"start_at"`
	EndAt    *time.Time   `json:"end_at" yaml:"end_at"`
}

type UpdateScheduleRequest = CreateScheduleRequest

type PatchPolicy struct {
	Overlap        *OverlapPolicy `json:"overlap,omitempty"`
	Misfire        *MisfirePolicy `json:"misfire,omitempty"`
	TimeoutSeconds *int           `json:"timeout_seconds,omitempty"`
	MaxConcurrency *int           `json:"max_concurrency,omitempty"`
}

type PatchRetryPolicy struct {
	MaxAttempts         *int           `json:"max_attempts,omitempty"`
	Strategy            *RetryStrategy `json:"strategy,omitempty"`
	InitialDelaySeconds *int           `json:"initial_delay_seconds,omitempty"`
	MaxDelaySeconds     *int           `json:"max_delay_seconds,omitempty"`
}

type PatchScheduleRequest struct {
	Name     *string           `json:"name,omitempty"`
	Enabled  *bool             `json:"enabled,omitempty"`
	Timezone *string           `json:"timezone,omitempty"`
	Schedule *ScheduleSpec     `json:"schedule,omitempty"`
	Target   *TargetSpec       `json:"target,omitempty"`
	Policy   *PatchPolicy      `json:"policy,omitempty"`
	Retry    *PatchRetryPolicy `json:"retry,omitempty"`
	StartAt  **time.Time       `json:"start_at,omitempty"`
	EndAt    **time.Time       `json:"end_at,omitempty"`
}

type TriggerRequest struct {
	Reason string `json:"reason"`
}

type StatusResponse struct {
	Service   string `json:"service"`
	Version   string `json:"version"`
	StartedAt string `json:"started_at"`
	Database  struct {
		Driver string `json:"driver"`
		Path   string `json:"path"`
	} `json:"database"`
	Scheduler struct {
		LoopIntervalSeconds int    `json:"loop_interval_seconds"`
		LastTickAt          string `json:"last_tick_at"`
	} `json:"scheduler"`
	Workers struct {
		Active int `json:"active"`
	} `json:"workers"`
}

type ListResponse[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

type ScheduleSummary struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Enabled    bool         `json:"enabled"`
	Timezone   string       `json:"timezone"`
	Schedule   ScheduleSpec `json:"schedule"`
	TargetKind TargetKind   `json:"target_kind"`
	PausedAt   *time.Time   `json:"paused_at"`
	NextRunAt  *time.Time   `json:"next_run_at"`
	LastRunAt  *time.Time   `json:"last_run_at"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

type RunSummary struct {
	ID            string     `json:"id"`
	ScheduleID    string     `json:"schedule_id"`
	OccurrenceKey string     `json:"occurrence_key"`
	Status        RunStatus  `json:"status"`
	Attempt       int        `json:"attempt"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error   string            `json:"error"`
	Details []ValidationError `json:"details,omitempty"`
}
