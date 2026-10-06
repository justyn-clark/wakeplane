package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func startTestRunner(t *testing.T, config runnerConfig) (*runner, *httptest.Server) {
	t.Helper()
	if config.StateDir == "" {
		config.StateDir = t.TempDir()
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = time.Millisecond
	}
	r, err := newRunner(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(r.Handler())
	t.Cleanup(func() { r.Close(); server.Close() })
	return r, server
}

func submitTestJob(t *testing.T, client *http.Client, address, key string, task taskRequest, token string) (job, int) {
	t.Helper()
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, address+"/jobs", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var entry job
	if err := json.NewDecoder(response.Body).Decode(&entry); err != nil {
		t.Fatal(err)
	}
	return entry, response.StatusCode
}

func waitJob(t *testing.T, r *runner, id string, accept func(job) bool) job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entry, ok := r.get(id)
		if ok && accept(entry) {
			return entry
		}
		time.Sleep(time.Millisecond)
	}
	entry, _ := r.get(id)
	t.Fatalf("job did not reach expected state: %+v", entry)
	return job{}
}

func terminal(entry job) bool { return entry.Status == "succeeded" || entry.Status == "failed" }

func repositoryFixture(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("unexpected write: %s", request.Method)
		}
		if calls != nil {
			calls.Add(1)
		}
		switch request.URL.Path {
		case "/repos/acme/project":
			_, _ = io.WriteString(w, `{"full_name":"acme/project","html_url":"https://github.com/acme/project","description":"Useful project","pushed_at":"2026-10-05T08:00:00Z","stargazers_count":12,"open_issues_count":3}`)
		case "/repos/acme/project/releases/latest":
			_, _ = io.WriteString(w, `{"name":"Version 1","tag_name":"v1","html_url":"https://github.com/acme/project/releases/tag/v1","published_at":"2026-10-04T08:00:00Z"}`)
		case "/repos/acme/project/pulls":
			if request.URL.Query().Get("state") != "open" {
				t.Error("pull query omitted open status")
			}
			_, _ = io.WriteString(w, `[{"number":7,"title":"Improve timing","html_url":"https://github.com/acme/project/pull/7","updated_at":"2026-10-05T09:00:00Z"}]`)
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestAcceptanceIsNotCompletionAndReportIsInspectable(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/repos/acme/project" {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
			_, _ = io.WriteString(w, `{"full_name":"acme/project","pushed_at":"2026-10-05T08:00:00Z"}`)
		} else if strings.HasSuffix(request.URL.Path, "/releases/latest") {
			w.WriteHeader(http.StatusNotFound)
		} else {
			_, _ = io.WriteString(w, `[]`)
		}
	}))
	t.Cleanup(source.Close)
	r, server := startTestRunner(t, runnerConfig{GitHubURL: source.URL})
	entry, code := submitTestJob(t, server.Client(), server.URL, "occurrence-1", taskRequest{Task: "repository-watch", Repository: "acme/project"}, "")
	if code != http.StatusAccepted || entry.Status != "queued" || entry.Result != nil {
		t.Fatalf("submission falsely completed work: code %d, %+v", code, entry)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("source work did not start")
	}
	inProgress, _ := r.get(entry.ID)
	if inProgress.Status != "running" || inProgress.Result != nil {
		t.Fatalf("unexpected running state: %+v", inProgress)
	}
	close(release)
	finished := waitJob(t, r, entry.ID, terminal)
	if finished.Status != "succeeded" || finished.Result == nil || len(finished.Artifacts) != 1 {
		t.Fatalf("missing completed report: %+v", finished)
	}
	if !strings.Contains(finished.Result.Summary, "No published stable release") {
		t.Fatal("404 release was not explained")
	}
	response, err := server.Client().Get(finished.Artifacts[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(data), "Repository watch") || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("report artifact missing or unsafe: %d %s", response.StatusCode, data)
	}
}

func TestConcurrentSubmissionDeduplicatesAndRejectsPayloadConflict(t *testing.T) {
	var calls atomic.Int32
	source := repositoryFixture(t, &calls)
	r, server := startTestRunner(t, runnerConfig{GitHubURL: source.URL})
	task := taskRequest{Task: "repository-watch", Repository: "acme/project"}
	var submissions sync.WaitGroup
	ids := make(chan string, 12)
	for range 12 {
		submissions.Add(1)
		go func() {
			defer submissions.Done()
			entry, code := submitTestJob(t, server.Client(), server.URL, "same-occurrence", task, "")
			if code != http.StatusAccepted && code != http.StatusOK {
				t.Errorf("unexpected submit status %d", code)
			}
			ids <- entry.ID
		}()
	}
	submissions.Wait()
	close(ids)
	id := ""
	for value := range ids {
		if id == "" {
			id = value
		} else if value != id {
			t.Fatal("duplicate key created another job")
		}
	}
	finished := waitJob(t, r, id, terminal)
	if finished.Status != "succeeded" || calls.Load() != 3 {
		t.Fatalf("work ran more than once: %d calls, %+v", calls.Load(), finished)
	}
	_, code := submitTestJob(t, server.Client(), server.URL, "same-occurrence", taskRequest{Task: "repository-watch", Repository: "acme/other"}, "")
	if code != http.StatusConflict {
		t.Fatalf("payload conflict returned %d", code)
	}
}

func TestCompletedJobsSurviveRestartWithoutRepeatingSources(t *testing.T) {
	var calls atomic.Int32
	source := repositoryFixture(t, &calls)
	directory := t.TempDir()
	r, server := startTestRunner(t, runnerConfig{GitHubURL: source.URL, StateDir: directory})
	task := taskRequest{Task: "repository-watch", Repository: "acme/project"}
	entry, _ := submitTestJob(t, server.Client(), server.URL, "restart-key", task, "")
	original := waitJob(t, r, entry.ID, terminal)
	r.Close()
	r2, server2 := startTestRunner(t, runnerConfig{GitHubURL: source.URL, StateDir: directory})
	repeated, code := submitTestJob(t, server2.Client(), server2.URL, "restart-key", task, "")
	if code != http.StatusOK || repeated.ID != entry.ID || repeated.Status != "succeeded" || !repeated.Result.GeneratedAt.Equal(original.Result.GeneratedAt) {
		t.Fatalf("restart lost completed job: %d %+v", code, repeated)
	}
	if calls.Load() != 3 {
		t.Fatalf("restart repeated source work: %d", calls.Load())
	}
	lookup, _ := http.NewRequest(http.MethodGet, server2.URL+"/jobs/lookup", nil)
	lookup.Header.Set("Idempotency-Key", "restart-key")
	response, err := server2.Client().Do(lookup)
	if err != nil {
		t.Fatal(err)
	}
	var recovered job
	decodeErr := json.NewDecoder(response.Body).Decode(&recovered)
	_ = response.Body.Close()
	if decodeErr != nil || response.StatusCode != http.StatusOK || recovered.ID != entry.ID || recovered.Status != "succeeded" {
		t.Fatalf("durable acceptance lookup failed: %+v %v", recovered, decodeErr)
	}
	lookup.Header.Set("Idempotency-Key", "not-recorded")
	response, err = server2.Client().Do(lookup)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound || calls.Load() != 3 {
		t.Fatal("missing acceptance lookup mutated source work")
	}
	if _, ok := r2.get(entry.ID); !ok {
		t.Fatal("stored job missing")
	}
	info, err := os.Stat(filepath.Join(directory, "jobs.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("state permissions are not private: %v %v", info, err)
	}
}

func TestInterruptedSourceResumesOnRestart(t *testing.T) {
	var first atomic.Bool
	started := make(chan struct{}, 1)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/repos/acme/project" {
			if !first.Swap(true) {
				started <- struct{}{}
				<-request.Context().Done()
				return
			}
			_, _ = io.WriteString(w, `{"full_name":"acme/project"}`)
		} else if strings.HasSuffix(request.URL.Path, "/releases/latest") {
			w.WriteHeader(http.StatusNotFound)
		} else {
			_, _ = io.WriteString(w, `[]`)
		}
	}))
	t.Cleanup(source.Close)
	directory := t.TempDir()
	r, server := startTestRunner(t, runnerConfig{StateDir: directory, GitHubURL: source.URL})
	entry, _ := submitTestJob(t, server.Client(), server.URL, "interrupted", taskRequest{Task: "repository-watch", Repository: "acme/project"}, "")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	r.Close()
	r2, _ := startTestRunner(t, runnerConfig{StateDir: directory, GitHubURL: source.URL})
	finished := waitJob(t, r2, entry.ID, terminal)
	if finished.Status != "succeeded" {
		t.Fatalf("interrupted job did not resume: %+v", finished)
	}
}

func TestRSSAndAtomSourcesProduceBoundedDeduplicatedReadingList(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/rss" {
			fmt.Fprint(w, `<rss version="2.0"><channel><title>Project feed</title>`)
			for i := range 25 {
				fmt.Fprintf(w, `<item><title>Item %d</title><link>/article/%d</link><pubDate>Mon, 05 Oct 2026 09:%02d:00 +0000</pubDate><description>&lt;p&gt;Read &amp;amp; learn&lt;/p&gt;</description></item>`, i, i, i)
			}
			fmt.Fprint(w, `</channel></rss>`)
		} else {
			fmt.Fprint(w, `<feed xmlns="http://www.w3.org/2005/Atom"><title>Atom feed</title><entry><title>Duplicate</title><link href="/article/24"/><updated>2026-10-05T09:24:00Z</updated><summary>Already included</summary></entry><entry><title>Newest &lt;script&gt;</title><link rel="alternate" href="/newest"/><published>2026-10-05T10:00:00Z</published><summary>&lt;b&gt;Actual source excerpt&lt;/b&gt;</summary></entry></feed>`)
		}
	}))
	t.Cleanup(source.Close)
	r, server := startTestRunner(t, runnerConfig{})
	entry, _ := submitTestJob(t, server.Client(), server.URL, "digest", taskRequest{Task: "weekly-summary", Feeds: []string{source.URL + "/rss", source.URL + "/atom"}}, "")
	finished := waitJob(t, r, entry.ID, terminal)
	if finished.Status != "succeeded" || len(finished.Result.Items) != maxArticles {
		t.Fatalf("reading list missing or unbounded: %+v", finished)
	}
	if finished.Result.Items[0].URL != source.URL+"/newest" || finished.Result.Items[0].Excerpt != "Actual source excerpt" {
		t.Fatalf("Atom entry not parsed/sorted: %+v", finished.Result.Items[0])
	}
	if !strings.Contains(finished.Result.Items[1].Excerpt, "Read & learn") {
		t.Fatal("source excerpt HTML was not converted to text")
	}
	if strings.Contains(renderReport(*finished.Result), "<script>") {
		t.Fatal("source markup was rendered as executable HTML")
	}
	data, _ := json.Marshal(finished)
	if len(data) > maxSourceBytes {
		t.Fatalf("job response exceeds contract size: %d", len(data))
	}
}

func TestSourceErrorsCannotBeReportedAsSuccess(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		status int
		body   string
	}{
		{"http failure", http.StatusServiceUnavailable, "not ready"},
		{"invalid XML", http.StatusOK, "not XML"},
		{"unexpected document", http.StatusOK, "<html>not a feed</html>"},
		{"oversized response", http.StatusOK, strings.Repeat("x", maxSourceBytes+1)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(fixture.status)
				_, _ = io.WriteString(w, fixture.body)
			}))
			t.Cleanup(source.Close)
			r, server := startTestRunner(t, runnerConfig{})
			entry, _ := submitTestJob(t, server.Client(), server.URL, fixture.name, taskRequest{Task: "weekly-summary", Feeds: []string{source.URL}}, "")
			finished := waitJob(t, r, entry.ID, terminal)
			if finished.Status != "failed" || finished.Error == "" || finished.Result != nil {
				t.Fatalf("source failure became success: %+v", finished)
			}
		})
	}
}

func TestNotificationRetriesUseStableKeyAndPayload(t *testing.T) {
	source := repositoryFixture(t, nil)
	var attempts atomic.Int32
	var mu sync.Mutex
	var bodies [][]byte
	var keys []string
	notify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		bodies = append(bodies, body)
		keys = append(keys, request.Header.Get("Idempotency-Key"))
		mu.Unlock()
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(notify.Close)
	r, server := startTestRunner(t, runnerConfig{GitHubURL: source.URL})
	entry, _ := submitTestJob(t, server.Client(), server.URL, "notify-retry", taskRequest{Task: "repository-watch", Repository: "acme/project", NotifyURL: notify.URL}, "")
	finished := waitJob(t, r, entry.ID, terminal)
	if finished.Status != "succeeded" || finished.Result.Delivery.Status != "sent" || finished.Result.Delivery.Attempts != 3 || finished.Result.Delivery.DeliveredAt == nil {
		t.Fatalf("notification status dishonest: %+v", finished)
	}
	mu.Lock()
	defer mu.Unlock()
	for i := range bodies {
		if !bytes.Equal(bodies[0], bodies[i]) || keys[i] != "wakeplane-notify:"+entry.ID {
			t.Fatal("notification retry changed payload or idempotency key")
		}
	}
}

func TestFailedNotificationIsRetainedAndVisible(t *testing.T) {
	source := repositoryFixture(t, nil)
	var attempts atomic.Int32
	notify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { attempts.Add(1); w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(notify.Close)
	r, server := startTestRunner(t, runnerConfig{GitHubURL: source.URL})
	entry, _ := submitTestJob(t, server.Client(), server.URL, "notify-failed", taskRequest{Task: "repository-watch", Repository: "acme/project", NotifyURL: notify.URL}, "")
	finished := waitJob(t, r, entry.ID, terminal)
	if finished.Status != "succeeded" || finished.Result.Delivery.Status != "failed" || attempts.Load() != maxDeliveries || !strings.Contains(finished.Progress.Message, "notification failed") || !strings.Contains(finished.Result.Delivery.LastError, "502") {
		t.Fatalf("delivery failure was hidden: %+v", finished)
	}
}

func TestPendingNotificationResumesWithoutRepeatingSourceReport(t *testing.T) {
	var sources atomic.Int32
	source := repositoryFixture(t, &sources)
	var attempts atomic.Int32
	started := make(chan struct{}, 1)
	var mu sync.Mutex
	var bodies [][]byte
	notify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if attempts.Add(1) == 1 {
			started <- struct{}{}
			<-request.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(notify.Close)
	directory := t.TempDir()
	r, server := startTestRunner(t, runnerConfig{StateDir: directory, GitHubURL: source.URL})
	entry, _ := submitTestJob(t, server.Client(), server.URL, "notify-resume", taskRequest{Task: "repository-watch", Repository: "acme/project", NotifyURL: notify.URL}, "")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("notification did not start")
	}
	r.Close()
	r2, _ := startTestRunner(t, runnerConfig{StateDir: directory, GitHubURL: source.URL})
	finished := waitJob(t, r2, entry.ID, terminal)
	if finished.Status != "succeeded" || finished.Result.Delivery.Status != "sent" || finished.Result.Delivery.Attempts != 2 || sources.Load() != 3 {
		t.Fatalf("notification restart repeated sources or lost state: %+v; sources=%d", finished, sources.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatal("restart changed notification payload")
	}
}

func TestRunnerAuthAndCredentialOriginBinding(t *testing.T) {
	source := repositoryFixture(t, nil)
	r, server := startTestRunner(t, runnerConfig{GitHubURL: source.URL, Token: "runner-token", NotifyToken: "notify-token", NotifyOrigin: "https://notify.example"})
	task := taskRequest{Task: "repository-watch", Repository: "acme/project"}
	_, code := submitTestJob(t, server.Client(), server.URL, "auth", task, "")
	if code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated submit returned %d", code)
	}
	entry, code := submitTestJob(t, server.Client(), server.URL, "auth", task, "runner-token")
	if code != http.StatusAccepted {
		t.Fatalf("authenticated submit returned %d", code)
	}
	_ = waitJob(t, r, entry.ID, terminal)
	response, err := server.Client().Get(server.URL + "/jobs/" + entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("job result escaped runner authentication")
	}
	task.NotifyURL = "https://other.example/hook"
	_, code = submitTestJob(t, server.Client(), server.URL, "leak", task, "runner-token")
	if code != http.StatusBadRequest {
		t.Fatalf("credential origin mismatch returned %d", code)
	}
	if _, err := newRunner(context.Background(), runnerConfig{StateDir: t.TempDir(), NotifyToken: "token"}); err == nil {
		t.Fatal("notification token accepted without origin binding")
	}
}

func TestInputValidationAndStateExclusivity(t *testing.T) {
	r, server := startTestRunner(t, runnerConfig{})
	for _, task := range []taskRequest{
		{Task: "unknown"},
		{Task: "repository-watch", Repository: "acme/.."},
		{Task: "repository-watch", Repository: "acme/project/extra"},
		{Task: "weekly-summary"},
		{Task: "weekly-summary", Feeds: []string{"file:///etc/passwd"}},
		{Task: "weekly-summary", Feeds: []string{"https://user:secret@example.com/feed"}},
	} {
		_, code := submitTestJob(t, server.Client(), server.URL, task.Task+task.Repository, task, "")
		if code != http.StatusBadRequest {
			t.Errorf("invalid task accepted: %+v (%d)", task, code)
		}
	}
	if _, err := newRunner(context.Background(), runnerConfig{StateDir: r.config.StateDir}); err == nil {
		t.Fatal("two runner processes could overwrite one state directory")
	}
	for _, body := range []string{`{"task":"repository-watch","repository":"acme/project"}{}`, strings.Repeat("x", maxRequestBytes+1)} {
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/jobs", strings.NewReader(body))
		req.Header.Set("Idempotency-Key", "invalid-json")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid JSON returned %d", response.StatusCode)
		}
	}
}
