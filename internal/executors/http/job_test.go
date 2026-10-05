package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/executors"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}}
}

func jobRequest() executors.ExecuteRequest {
	return executors.ExecuteRequest{
		Schedule: domain.Schedule{Target: domain.TargetSpec{Kind: domain.TargetKindHTTP, Method: "POST", URL: "https://runner.test/jobs", HTTPJob: &domain.HTTPJobSpec{PollIntervalSeconds: 1}}},
		Run:      domain.Run{ID: "run-test", OccurrenceKey: "occurrence-test"}, Timeout: 60,
	}
}

func captureJob(req *executors.ExecuteRequest, checkpoint **domain.ExternalJob) {
	req.Checkpoint = func(_ context.Context, job domain.ExternalJob) error {
		copy := job
		*checkpoint = &copy
		return nil
	}
}

func TestJobAcceptanceIsNotCompletion(t *testing.T) {
	req := jobRequest()
	observed := make(chan domain.ExternalJob, 3)
	req.Checkpoint = func(_ context.Context, job domain.ExternalJob) error { observed <- job; return nil }
	exec := New()
	exec.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := "running"
		if r.Method == "POST" {
			status = "queued"
		}
		return jsonResponse(202, `{"job_id":"job-1","status_url":"https://runner.test/jobs/1","status":"`+status+`"}`), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan executors.Result, 1)
	go func() { done <- exec.Execute(ctx, req) }()
	for _, want := range []domain.ExternalJobStatus{domain.ExternalJobSubmitting, domain.ExternalJobQueued, domain.ExternalJobRunning} {
		select {
		case job := <-observed:
			if job.Status != want {
				t.Fatalf("status=%s, want=%s", job.Status, want)
			}
		case <-time.After(time.Second):
			t.Fatal("checkpoint was not observed")
		}
	}
	select {
	case result := <-done:
		t.Fatalf("accepted job incorrectly completed: %+v", result)
	default:
	}
	cancel()
	result := <-done
	if !result.Deferred || result.Cancelled {
		t.Fatalf("shutdown must defer tracking without claiming remote cancellation: %+v", result)
	}
}

func TestJobRetryResumesStoredIdentityWithoutResubmission(t *testing.T) {
	req := jobRequest()
	var checkpoint *domain.ExternalJob
	captureJob(&req, &checkpoint)
	var submits, polls int
	exec := New()
	exec.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			submits++
			if r.Header.Get("Idempotency-Key") != req.Run.OccurrenceKey {
				t.Error("stable idempotency key missing")
			}
			return jsonResponse(202, `{"job_id":"job-1","status_url":"https://runner.test/jobs/1","status":"queued"}`), nil
		}
		polls++
		if polls == 1 {
			return nil, errors.New("temporary transport failure")
		}
		return jsonResponse(200, `{"job_id":"job-1","status":"succeeded","result":{"answer":42},"artifacts":[{"name":"report","url":"https://files.test/report","content_type":"text/plain"}]}`), nil
	})
	first := exec.Execute(context.Background(), req)
	if first.ErrorText == "" || checkpoint == nil || checkpoint.JobID != "job-1" {
		t.Fatalf("first attempt=%+v checkpoint=%+v", first, checkpoint)
	}
	deadline := checkpoint.DeadlineAt
	req.Run.ID = "retry-run"
	req.Run.ExternalJob = checkpoint
	// A changed live schedule cannot mutate the persisted submission target.
	req.Schedule.Target.URL = "https://another-runner.test/jobs"
	second := exec.Execute(context.Background(), req)
	if second.ErrorText != "" || submits != 1 || polls != 2 || !checkpoint.DeadlineAt.Equal(deadline) {
		t.Fatalf("resume=%+v submits=%d polls=%d checkpoint=%+v", second, submits, polls, checkpoint)
	}
	if len(checkpoint.Artifacts) != 1 || !strings.Contains(string(second.ResultJSON), `"answer":42`) {
		t.Fatalf("completed outcome not retained: %+v", second)
	}
}

func TestJobCrashBeforeAcceptanceCheckpointUsesSameIdempotencyKey(t *testing.T) {
	req := jobRequest()
	var checkpoint *domain.ExternalJob
	var failAcceptance = true
	req.Checkpoint = func(_ context.Context, job domain.ExternalJob) error {
		if job.JobID != "" && failAcceptance {
			failAcceptance = false
			return errors.New("storage unavailable")
		}
		copy := job
		checkpoint = &copy
		return nil
	}
	keys := map[string]bool{}
	submits := 0
	exec := New()
	exec.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			submits++
			keys[r.Header.Get("Idempotency-Key")] = true
			return jsonResponse(202, `{"job_id":"same-job","status_url":"https://runner.test/jobs/same","status":"queued"}`), nil
		}
		return jsonResponse(200, `{"status":"succeeded"}`), nil
	})
	if result := exec.Execute(context.Background(), req); result.ErrorText == "" {
		t.Fatal("expected checkpoint failure")
	}
	if checkpoint == nil || checkpoint.JobID != "" {
		t.Fatalf("submission intent not retained: %+v", checkpoint)
	}
	req.Run.ExternalJob = checkpoint
	if result := exec.Execute(context.Background(), req); result.ErrorText != "" {
		t.Fatalf("retry: %+v", result)
	}
	if submits != 2 || len(keys) != 1 || !keys["occurrence-test"] {
		t.Fatalf("submits=%d keys=%v", submits, keys)
	}
}

func TestJobDoesNotSubmitWithoutDurableIntent(t *testing.T) {
	req := jobRequest()
	req.Checkpoint = func(context.Context, domain.ExternalJob) error { return errors.New("storage unavailable") }
	var calls atomic.Int32
	exec := New()
	exec.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, errors.New("unexpected call") })
	if result := exec.Execute(context.Background(), req); result.ErrorText == "" || calls.Load() != 0 {
		t.Fatalf("result=%+v calls=%d", result, calls.Load())
	}
	req.Checkpoint = nil
	if result := exec.Execute(context.Background(), req); result.ErrorText == "" || calls.Load() != 0 {
		t.Fatalf("missing checkpoint: %+v", result)
	}
}

func TestJobDeadlineSurvivesRestart(t *testing.T) {
	req := jobRequest()
	old := time.Now().UTC().Add(-time.Hour)
	req.Run.ExternalJob = &domain.ExternalJob{JobID: "old-job", StatusURL: "https://runner.test/jobs/old", Status: domain.ExternalJobRunning, SubmittedAt: old, DeadlineAt: old.Add(time.Minute), RequestTarget: req.Schedule.Target}
	req.Checkpoint = func(context.Context, domain.ExternalJob) error { return nil }
	calls := 0
	exec := New()
	exec.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected call") })
	result := exec.Execute(context.Background(), req)
	if result.ErrorText == "" || !result.TerminalFailure || result.Cancelled || calls != 0 {
		t.Fatalf("expired job restarted: %+v calls=%d", result, calls)
	}
}

func TestJobRejectsUnsafeOrMalformedResponses(t *testing.T) {
	for _, test := range []struct {
		name, body string
		code       int
	}{
		{"cross origin", `{"job_id":"x","status_url":"https://evil.test/jobs/x","status":"queued"}`, 202},
		{"credentials", `{"job_id":"x","status_url":"https://user:secret@runner.test/jobs/x","status":"queued"}`, 202},
		{"unknown status", `{"job_id":"x","status_url":"https://runner.test/jobs/x","status":"accepted"}`, 202},
		{"missing identity", `{"status_url":"https://runner.test/jobs/x","status":"queued"}`, 202},
		{"invalid progress", `{"job_id":"x","status_url":"https://runner.test/jobs/x","status":"running","progress":{"percent":101}}`, 202},
		{"invalid artifact", `{"job_id":"x","status_url":"https://runner.test/jobs/x","status":"succeeded","artifacts":[{"name":"x","url":"file:///secret"}]}`, 200},
		{"malformed JSON", `{"status":`, 200},
		{"oversized JSON", strings.Repeat("x", maxJobResponseBytes+1), 200},
		{"redirect", "", 302},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := jobRequest()
			var checkpoint *domain.ExternalJob
			captureJob(&req, &checkpoint)
			calls := 0
			exec := New()
			exec.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return jsonResponse(test.code, test.body), nil })
			result := exec.Execute(context.Background(), req)
			if result.ErrorText == "" || calls != 1 || checkpoint.JobID != "" {
				t.Fatalf("unsafe response accepted: %+v calls=%d checkpoint=%+v", result, calls, checkpoint)
			}
		})
	}
}

func TestJobRemoteTerminalStates(t *testing.T) {
	for _, status := range []string{"succeeded", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			req := jobRequest()
			var checkpoint *domain.ExternalJob
			captureJob(&req, &checkpoint)
			exec := New()
			exec.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, `{"job_id":"x","status_url":"https://runner.test/jobs/x","status":"`+status+`","error":"remote failure"}`), nil
			})
			result := exec.Execute(context.Background(), req)
			if checkpoint.Status != domain.ExternalJobStatus(status) {
				t.Fatalf("checkpoint=%+v", checkpoint)
			}
			if (status == "succeeded") != (result.ErrorText == "") || result.Cancelled != (status == "cancelled") || result.TerminalFailure != (status == "failed") {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestJobEventEnvelopePreservesStaticBody(t *testing.T) {
	req := jobRequest()
	req.Run.Event = &domain.TriggerEvent{ID: "event-1", Source: "github", Data: map[string]any{"repository": "wakeplane"}}
	req.Schedule.Target.Body = map[string]any{"workflow": "review"}
	var checkpoint *domain.ExternalJob
	captureJob(&req, &checkpoint)
	exec := New()
	exec.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Wakeplane-Event-ID") != "event-1" || r.Header.Get("X-Wakeplane-Event-Source") != "github" {
			t.Error("event headers missing")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if string(body["workflow"]) != `"review"` || !strings.Contains(string(body["_wakeplane_event"]), `"repository":"wakeplane"`) {
			t.Errorf("body=%v", body)
		}
		return jsonResponse(200, `{"job_id":"x","status_url":"https://runner.test/jobs/x","status":"succeeded"}`), nil
	})
	if result := exec.Execute(context.Background(), req); result.ErrorText != "" {
		t.Fatalf("result=%+v", result)
	}
	if _, polluted := req.Schedule.Target.Body["_wakeplane_event"]; polluted {
		t.Fatal("static target mutated")
	}
}

func TestSameOriginNormalizesDefaultPortsAndRejectsSchemes(t *testing.T) {
	if !sameOrigin("https://RUNNER.test/jobs", "https://runner.test:443/jobs/1") {
		t.Fatal("equivalent origins rejected")
	}
	for _, other := range []string{"http://runner.test/jobs", "https://runner.test:444/jobs", "https://runner.test.evil/jobs", "//runner.test/jobs", "/jobs/1"} {
		if sameOrigin("https://runner.test/jobs", other) {
			t.Errorf("unsafe origin accepted: %s", other)
		}
	}
}

func TestReconcileObservesOnceWithoutSubmitOrDeadlineExtension(t *testing.T) {
	req := jobRequest()
	now := time.Now().UTC()
	job := domain.ExternalJob{JobID: "job-1", StatusURL: "https://runner.test/jobs/1", Status: domain.ExternalJobRunning, SubmittedAt: now.Add(-time.Hour), DeadlineAt: now.Add(-time.Minute), RequestTarget: req.Schedule.Target}
	calls := 0
	exec := New()
	exec.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.Header.Get("Idempotency-Key") != "" {
			t.Error("reconciliation must not submit work")
		}
		return jsonResponse(200, `{"job_id":"job-1","status":"succeeded","result":{"done":true}}`), nil
	})
	observed, err := exec.Reconcile(context.Background(), req.Run, job)
	if err != nil || calls != 1 || observed.Status != domain.ExternalJobSucceeded || !observed.DeadlineAt.Equal(job.DeadlineAt) {
		t.Fatalf("observation=%+v calls=%d err=%v", observed, calls, err)
	}
	if job.Status != domain.ExternalJobRunning {
		t.Fatal("reconciliation mutated expected checkpoint")
	}
}

func TestJobDoesNotFollowRedirectWithCredentials(t *testing.T) {
	req := jobRequest()
	req.Schedule.Target.Headers = map[string]string{"Authorization": "runner secret"}
	var checkpoint *domain.ExternalJob
	captureJob(&req, &checkpoint)
	calls := 0
	exec := New()
	exec.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "runner.test" {
			t.Errorf("credentials sent to redirect origin: %s", r.URL.Host)
		}
		resp := jsonResponse(302, "")
		resp.Header.Set("Location", "https://evil.test/jobs")
		return resp, nil
	})
	if result := exec.Execute(context.Background(), req); result.ErrorText == "" || calls != 1 {
		t.Fatalf("redirect followed: result=%+v calls=%d", result, calls)
	}
}

func TestLookupRecoversLostAcceptanceWithoutResubmission(t *testing.T) {
	req := jobRequest()
	req.Schedule.Target.HTTPJob.LookupURL = "https://runner.test/jobs/lookup"
	now := time.Now().UTC()
	job := domain.ExternalJob{Status: domain.ExternalJobSubmitting, SubmittedAt: now.Add(-time.Hour), DeadlineAt: now.Add(-time.Minute), RequestTarget: req.Schedule.Target}
	calls := 0
	exec := New()
	exec.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.String() != req.Schedule.Target.HTTPJob.LookupURL || r.Header.Get("Idempotency-Key") != req.Run.OccurrenceKey {
			t.Errorf("unsafe recovery request: %s %s headers=%v", r.Method, r.URL, r.Header)
		}
		return jsonResponse(200, `{"job_id":"previously-accepted-job","status_url":"https://runner.test/jobs/prior","status":"succeeded","result":{"recovered":true}}`), nil
	})
	observed, err := exec.Reconcile(context.Background(), req.Run, job)
	if err != nil || calls != 1 || observed.JobID != "previously-accepted-job" || observed.Status != domain.ExternalJobSucceeded || !observed.DeadlineAt.Equal(job.DeadlineAt) || observed.CanReconcile {
		t.Fatalf("lookup=%+v calls=%d err=%v", observed, calls, err)
	}
	if job.JobID != "" || job.Status != domain.ExternalJobSubmitting {
		t.Fatal("recovery mutated expected checkpoint")
	}
}

func TestLookupAbsenceOrUnsafeResponseRemainsUnresolved(t *testing.T) {
	for _, test := range []struct {
		name   string
		code   int
		body   string
		lookup string
	}{
		{"absent", 404, `{"error":"not found"}`, "https://runner.test/jobs/lookup"},
		{"unsafe identity", 200, `{"job_id":"x","status_url":"https://evil.test/jobs/x","status":"succeeded"}`, "https://runner.test/jobs/lookup"},
		{"no identity", 200, `{"status":"succeeded"}`, "https://runner.test/jobs/lookup"},
		{"no configured lookup", 200, `{}`, ""},
		{"unsafe lookup origin", 200, `{}`, "https://evil.test/jobs/lookup"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := jobRequest()
			req.Schedule.Target.HTTPJob.LookupURL = test.lookup
			job := domain.ExternalJob{Status: domain.ExternalJobSubmitting, SubmittedAt: time.Now().UTC(), DeadlineAt: time.Now().UTC().Add(-time.Minute), RequestTarget: req.Schedule.Target}
			calls := 0
			exec := New()
			exec.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet {
					t.Error("lookup submitted work")
				}
				return jsonResponse(test.code, test.body), nil
			})
			if _, err := exec.Reconcile(context.Background(), req.Run, job); err == nil {
				t.Fatal("absence/unsafe response accepted as completion")
			}
			if calls > 1 || job.Status != domain.ExternalJobSubmitting || job.JobID != "" {
				t.Fatalf("unresolved state mutated: %+v calls=%d", job, calls)
			}
		})
	}
}
