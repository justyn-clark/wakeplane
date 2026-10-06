package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	stdhttp "net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/executors"
)

const maxJobResponseBytes = 256 * 1024

// jobResponse is an explicit adapter contract, not an arbitrary execution blob.
// A successful HTTP response only acknowledges the observation. Its status
// decides whether the remote work actually completed.
type jobResponse struct {
	JobID     string                   `json:"job_id"`
	StatusURL string                   `json:"status_url"`
	Status    domain.ExternalJobStatus `json:"status"`
	Progress  *domain.JobProgress      `json:"progress,omitempty"`
	Result    json.RawMessage          `json:"result,omitempty"`
	Artifacts []domain.JobArtifact     `json:"artifacts,omitempty"`
	Error     string                   `json:"error,omitempty"`
}

func (e *Executor) executeJob(ctx context.Context, req executors.ExecuteRequest) executors.Result {
	if req.Checkpoint == nil {
		return executors.Result{ErrorText: "external jobs require durable checkpoint storage", TerminalFailure: true}
	}
	job := req.Run.ExternalJob
	if job == nil {
		started := time.Now().UTC()
		if req.Run.StartedAt != nil {
			started = *req.Run.StartedAt
		}
		job = &domain.ExternalJob{
			Status: domain.ExternalJobSubmitting, SubmittedAt: started, UpdatedAt: started,
			DeadlineAt: started.Add(time.Duration(req.Timeout) * time.Second), RequestTarget: req.Schedule.Target,
		}
		job.CanReconcile = job.ReconciliationAvailable()
		if err := req.Checkpoint(ctx, *job); err != nil {
			return jobFailure(ctx, fmt.Errorf("persist submission intent: %w", err))
		}
	}
	if job.Terminal() {
		return jobResult(*job, nil)
	}
	if job.Compacted {
		return executors.Result{ErrorText: "external job checkpoint was pruned; occurrence cannot be resubmitted", TerminalFailure: true}
	}
	jobCtx, cancel := context.WithDeadline(ctx, job.DeadlineAt)
	defer cancel()
	if err := jobCtx.Err(); err != nil {
		return jobFailure(jobCtx, err)
	}
	target := job.RequestTarget
	if target.Kind != domain.TargetKindHTTP || target.HTTPJob == nil || strings.ToUpper(target.Method) != stdhttp.MethodPost || !safeHTTPURL(target.URL) {
		return executors.Result{ErrorText: "invalid persisted external job target", TerminalFailure: true}
	}
	interval := time.Duration(target.HTTPJob.PollIntervalSeconds) * time.Second
	if interval == 0 {
		interval = 5 * time.Second
	}
	if interval < time.Second || interval > 300*time.Second {
		return executors.Result{ErrorText: "invalid external job polling interval", TerminalFailure: true}
	}
	if job.JobID == "" {
		payload := target.Body
		if req.Run.Event != nil {
			payload = make(map[string]any, len(target.Body)+1)
			for key, value := range target.Body {
				payload[key] = value
			}
			if _, collision := payload["_wakeplane_event"]; collision {
				return executors.Result{ErrorText: "_wakeplane_event is reserved for the event envelope", TerminalFailure: true}
			}
			payload["_wakeplane_event"] = req.Run.Event
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return executors.Result{ErrorText: fmt.Sprintf("encode job request: %v", err), TerminalFailure: true}
		}
		response, status, err := e.observeJob(jobCtx, req, target, stdhttp.MethodPost, target.URL, body)
		if err != nil {
			result := jobFailure(jobCtx, err)
			result.HTTPStatusCode = status
			return result
		}
		if err := applyJobResponse(job, response, true); err != nil {
			return executors.Result{ErrorText: err.Error(), HTTPStatusCode: status, TerminalFailure: true}
		}
		if err := req.Checkpoint(jobCtx, *job); err != nil {
			return jobFailure(jobCtx, fmt.Errorf("persist accepted job: %w", err))
		}
		if job.Terminal() {
			return jobResult(*job, status)
		}
	}
	if !sameOrigin(target.URL, job.StatusURL) {
		return executors.Result{ErrorText: "external job status URL must share the submission origin", TerminalFailure: true}
	}
	for {
		response, status, err := e.observeJob(jobCtx, req, target, stdhttp.MethodGet, job.StatusURL, nil)
		if err != nil {
			result := jobFailure(jobCtx, err)
			result.HTTPStatusCode = status
			return result
		}
		if err := applyJobResponse(job, response, false); err != nil {
			return executors.Result{ErrorText: err.Error(), HTTPStatusCode: status, TerminalFailure: true}
		}
		if err := req.Checkpoint(jobCtx, *job); err != nil {
			return jobFailure(jobCtx, fmt.Errorf("persist job observation: %w", err))
		}
		if job.Terminal() {
			return jobResult(*job, status)
		}
		timer := time.NewTimer(interval)
		select {
		case <-jobCtx.Done():
			timer.Stop()
			return jobFailure(jobCtx, jobCtx.Err())
		case <-timer.C:
		}
	}
}

// Reconcile observes exactly once. It never submits work or extends a deadline.
// Its caller supplies a separate bounded operator-request context.
func (e *Executor) Reconcile(ctx context.Context, run domain.Run, job domain.ExternalJob) (domain.ExternalJob, error) {
	if job.Compacted || job.Terminal() {
		return domain.ExternalJob{}, errors.New("external job has no unresolved, safe remote identity to reconcile")
	}
	requestURL := job.StatusURL
	lookup := job.JobID == ""
	if lookup {
		if job.Status != domain.ExternalJobSubmitting || job.RequestTarget.HTTPJob == nil || job.RequestTarget.HTTPJob.LookupURL == "" {
			return domain.ExternalJob{}, errors.New("submission acceptance is unknown; configure a runner lookup URL before submitting jobs to enable recovery")
		}
		requestURL = job.RequestTarget.HTTPJob.LookupURL
	}
	if !sameOrigin(job.RequestTarget.URL, requestURL) {
		return domain.ExternalJob{}, errors.New("external job reconciliation URL must share the submission origin")
	}
	response, _, err := e.observeJob(ctx, executors.ExecuteRequest{Run: run}, job.RequestTarget, stdhttp.MethodGet, requestURL, nil)
	if err != nil {
		return domain.ExternalJob{}, err
	}
	if err := applyJobResponse(&job, response, lookup); err != nil {
		return domain.ExternalJob{}, err
	}
	return job, nil
}

func (e *Executor) observeJob(ctx context.Context, req executors.ExecuteRequest, target domain.TargetSpec, method, requestURL string, body []byte) (jobResponse, *int, error) {
	if !sameOrigin(target.URL, requestURL) {
		return jobResponse{}, nil, errors.New("external job URL must share the submission origin")
	}
	httpReq, err := stdhttp.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return jobResponse{}, nil, err
	}
	for key, value := range target.Headers {
		httpReq.Header.Set(key, value)
	}
	httpReq.Header.Set("Accept", "application/json")
	if method == stdhttp.MethodPost {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if method == stdhttp.MethodPost || (target.HTTPJob != nil && target.HTTPJob.LookupURL != "" && requestURL == target.HTTPJob.LookupURL) {
		httpReq.Header.Set("Idempotency-Key", req.Run.OccurrenceKey)
	}
	httpReq.Header.Set("X-Wakeplane-Occurrence-Key", req.Run.OccurrenceKey)
	setEventHeaders(httpReq, req.Run)
	// No automatic redirect may replay a submit request or send credentials to
	// another origin. Runners expose the final URLs in their JSON contract.
	client := *e.client
	client.CheckRedirect = func(_ *stdhttp.Request, _ []*stdhttp.Request) error { return stdhttp.ErrUseLastResponse }
	resp, err := client.Do(httpReq)
	if err != nil {
		return jobResponse{}, nil, err
	}
	defer resp.Body.Close()
	status := resp.StatusCode
	if status < 200 || status >= 300 {
		return jobResponse{}, &status, fmt.Errorf("external job endpoint returned HTTP %d", status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxJobResponseBytes+1))
	if err != nil {
		return jobResponse{}, &status, err
	}
	if len(data) > maxJobResponseBytes {
		return jobResponse{}, &status, fmt.Errorf("external job response exceeds %d bytes", maxJobResponseBytes)
	}
	var response jobResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return jobResponse{}, &status, fmt.Errorf("invalid external job JSON: %w", err)
	}
	return response, &status, nil
}

func applyJobResponse(job *domain.ExternalJob, response jobResponse, submitting bool) error {
	switch response.Status {
	case domain.ExternalJobQueued, domain.ExternalJobRunning, domain.ExternalJobSucceeded, domain.ExternalJobFailed, domain.ExternalJobCancelled:
	default:
		return errors.New("external job response requires a valid status")
	}
	if submitting {
		if strings.TrimSpace(response.JobID) == "" || len(response.JobID) > 256 || strings.IndexFunc(response.JobID, unicode.IsControl) >= 0 {
			return errors.New("external job submission requires a non-empty job_id of at most 256 characters without control characters")
		}
		if !sameOrigin(job.RequestTarget.URL, response.StatusURL) {
			return errors.New("external job submission requires an absolute same-origin status_url")
		}
	} else {
		if response.JobID != "" && response.JobID != job.JobID {
			return errors.New("external job response changed job_id")
		}
		if response.StatusURL != "" && response.StatusURL != job.StatusURL {
			return errors.New("external job response changed status_url")
		}
	}
	if response.Progress != nil && response.Progress.Percent != nil {
		percent := *response.Progress.Percent
		if math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 || percent > 100 {
			return errors.New("external job progress percent must be between 0 and 100")
		}
	}
	for _, artifact := range response.Artifacts {
		if strings.TrimSpace(artifact.Name) == "" || !safeHTTPURL(artifact.URL) {
			return errors.New("external job artifacts require a name and an absolute HTTP(S) URL without credentials or a fragment")
		}
	}
	if submitting {
		job.JobID, job.StatusURL = response.JobID, response.StatusURL
	}
	job.Status, job.Progress, job.Result, job.Artifacts, job.Error = response.Status, response.Progress, response.Result, response.Artifacts, response.Error
	job.UpdatedAt = time.Now().UTC()
	job.CanReconcile = job.ReconciliationAvailable()
	return nil
}

func safeHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && u.Hostname() != "" && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.Fragment == ""
}

func sameOrigin(a, b string) bool {
	if !safeHTTPURL(a) || !safeHTTPURL(b) {
		return false
	}
	left, _ := url.Parse(a)
	right, _ := url.Parse(b)
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return left.Scheme == right.Scheme && strings.EqualFold(left.Hostname(), right.Hostname()) && port(left) == port(right)
}

func jobFailure(ctx context.Context, err error) executors.Result {
	if errors.Is(ctx.Err(), context.Canceled) {
		return executors.Result{ErrorText: err.Error(), Deferred: true}
	}
	return executors.Result{ErrorText: err.Error(), TerminalFailure: errors.Is(ctx.Err(), context.DeadlineExceeded)}
}

func jobResult(job domain.ExternalJob, status *int) executors.Result {
	result := executors.Result{HTTPStatusCode: status, ResultJSON: domain.MustJSON(job)}
	switch job.Status {
	case domain.ExternalJobFailed:
		result.ErrorText = job.Error
		if result.ErrorText == "" {
			result.ErrorText = "external job failed"
		}
		result.TerminalFailure = true
	case domain.ExternalJobCancelled:
		result.ErrorText = "external job was cancelled by the runner"
		result.Cancelled = true
	}
	return result
}
