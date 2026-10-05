package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The state directory lock must fence writes from the retired process, including
// requests that were already decoding their body when shutdown began.
func TestClosedRunnerCannotOverwriteReplacementState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Keep queued jobs from making any source requests in this test.
	config := runnerConfig{StateDir: t.TempDir(), PublicURL: "https://runner.example"}
	old, err := newRunner(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	oldHandler := old.Handler()
	old.Close()
	replacement, err := newRunner(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(replacement.Close)
	post := func(handler http.Handler, key string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "https://runner.example/jobs", strings.NewReader(`{"task":"repository-watch","repository":"acme/project"}`))
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := post(replacement.Handler(), "new-process"); response.Code != http.StatusAccepted {
		t.Fatalf("replacement acceptance = %d: %s", response.Code, response.Body.String())
	}
	if response := post(oldHandler, "retired-process"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed runner accepted a stale write: status = %d, body = %s", response.Code, response.Body.String())
	}
	replacement.Close()
	restarted, err := newRunner(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	request := httptest.NewRequest(http.MethodGet, "https://runner.example/jobs/lookup", nil)
	request.Header.Set("Idempotency-Key", "new-process")
	response := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("replacement's saved job was lost: status = %d, body = %s", response.Code, response.Body.String())
	}
}

type pausedRequestBody struct {
	*strings.Reader
	reading chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (body *pausedRequestBody) Read(p []byte) (int, error) {
	body.once.Do(func() {
		close(body.reading)
		<-body.resume
	})
	return body.Reader.Read(p)
}

func (body *pausedRequestBody) Close() error { return nil }

func TestRequestDecodingAcrossCloseCannotWriteReplacementState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	config := runnerConfig{StateDir: t.TempDir(), PublicURL: "https://runner.example"}
	old, err := newRunner(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	body := &pausedRequestBody{
		Reader:  strings.NewReader(`{"task":"repository-watch","repository":"acme/project"}`),
		reading: make(chan struct{}), resume: make(chan struct{}),
	}
	request := httptest.NewRequest(http.MethodPost, "https://runner.example/jobs", body)
	request.Header.Set("Idempotency-Key", "old-in-flight")
	response := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		old.Handler().ServeHTTP(response, request)
		close(finished)
	}()
	<-body.reading
	old.Close()
	replacement, err := newRunner(ctx, config)
	if err != nil {
		close(body.resume)
		<-finished
		t.Fatal(err)
	}
	defer replacement.Close()
	replacementRequest := httptest.NewRequest(http.MethodPost, "https://runner.example/jobs", strings.NewReader(`{"task":"repository-watch","repository":"acme/project"}`))
	replacementRequest.Header.Set("Idempotency-Key", "replacement-in-flight")
	replacementResponse := httptest.NewRecorder()
	replacement.Handler().ServeHTTP(replacementResponse, replacementRequest)
	close(body.resume)
	<-finished
	if replacementResponse.Code != http.StatusAccepted {
		t.Fatalf("replacement acceptance = %d: %s", replacementResponse.Code, replacementResponse.Body.String())
	}
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("late body decode persisted after lock release: status = %d: %s", response.Code, response.Body.String())
	}
	replacement.Close()
	restarted, err := newRunner(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	lookup := httptest.NewRequest(http.MethodGet, "https://runner.example/jobs/lookup", nil)
	lookup.Header.Set("Idempotency-Key", "replacement-in-flight")
	lookupResponse := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(lookupResponse, lookup)
	if lookupResponse.Code != http.StatusOK {
		t.Fatalf("replacement state was lost after late body decode: %d: %s", lookupResponse.Code, lookupResponse.Body.String())
	}
}
