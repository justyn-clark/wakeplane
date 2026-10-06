package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	maxRequestBytes = 16 * 1024
	maxSourceBytes  = 256 * 1024
	maxStateBytes   = 64 * 1024 * 1024
	maxJobs         = 500
	maxFeeds        = 5
	maxArticles     = 20
	maxDeliveries   = 3
)

type taskRequest struct {
	Task           string          `json:"task"`
	Repository     string          `json:"repository,omitempty"`
	Feeds          []string        `json:"feeds,omitempty"`
	NotifyURL      string          `json:"notify_url,omitempty"`
	WakeplaneEvent json.RawMessage `json:"_wakeplane_event,omitempty"`
}

type reportItem struct {
	Title       string `json:"title"`
	URL         string `json:"url,omitempty"`
	Source      string `json:"source"`
	PublishedAt string `json:"published_at,omitempty"`
	Excerpt     string `json:"excerpt,omitempty"`
}

type deliveryState struct {
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"last_error,omitempty"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}

type report struct {
	Title       string         `json:"title"`
	GeneratedAt time.Time      `json:"generated_at"`
	Summary     string         `json:"summary"`
	Items       []reportItem   `json:"items"`
	Delivery    *deliveryState `json:"delivery,omitempty"`
}

type jobProgress struct {
	Percent int    `json:"percent"`
	Message string `json:"message"`
}

type jobArtifact struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
}

// job's public fields match Wakeplane's external HTTP job contract. The request
// and fingerprint are disk-only and are never included in a status response.
type job struct {
	ID          string        `json:"job_id"`
	StatusURL   string        `json:"status_url"`
	Status      string        `json:"status"`
	Progress    jobProgress   `json:"progress"`
	Result      *report       `json:"result,omitempty"`
	Artifacts   []jobArtifact `json:"artifacts,omitempty"`
	Error       string        `json:"error,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Request     taskRequest   `json:"-"`
	Fingerprint string        `json:"-"`
}

type storedJob struct {
	Job         job         `json:"job"`
	Request     taskRequest `json:"request"`
	Fingerprint string      `json:"fingerprint"`
}

type diskState struct {
	Version int                  `json:"version"`
	Jobs    map[string]storedJob `json:"jobs"`
}

type runnerConfig struct {
	StateDir     string
	PublicURL    string
	Token        string
	GitHubURL    string
	GitHubToken  string
	NotifyToken  string
	NotifyOrigin string
	Client       *http.Client
	RetryDelay   time.Duration
}

type runner struct {
	config      runnerConfig
	client      *http.Client
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	jobs        map[string]job
	queue       chan string
	workers     sync.WaitGroup
	releaseLock func() error
	closed      bool // Protected by mu; fences handlers before releasing the disk lock.
}

func newRunner(ctx context.Context, config runnerConfig) (*runner, error) {
	if config.StateDir == "" {
		return nil, errors.New("state directory is required")
	}
	if config.PublicURL != "" {
		if _, err := validHTTPURL(config.PublicURL); err != nil {
			return nil, fmt.Errorf("public URL: %w", err)
		}
		config.PublicURL = strings.TrimRight(config.PublicURL, "/")
	}
	if config.GitHubURL == "" {
		config.GitHubURL = "https://api.github.com"
	}
	if _, err := validHTTPURL(config.GitHubURL); err != nil {
		return nil, fmt.Errorf("GitHub API URL: %w", err)
	}
	if config.NotifyToken != "" {
		if _, err := validHTTPURL(config.NotifyOrigin); err != nil {
			return nil, errors.New("a notification token requires AUTOMATION_RUNNER_NOTIFY_ORIGIN")
		}
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = time.Second
	}
	client := http.Client{Timeout: 15 * time.Second}
	if config.Client != nil {
		client = *config.Client
		if client.Timeout == 0 {
			client.Timeout = 15 * time.Second
		}
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	workerCtx, cancel := context.WithCancel(ctx)
	r := &runner{
		config: config, client: &client, ctx: workerCtx, cancel: cancel,
		jobs: make(map[string]job), queue: make(chan string, maxJobs),
	}
	if err := os.MkdirAll(config.StateDir, 0700); err != nil {
		cancel()
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	release, err := lockStateDirectory(config.StateDir)
	if err != nil {
		cancel()
		return nil, err
	}
	r.releaseLock = release
	if err := r.load(); err != nil {
		cancel()
		_ = release()
		return nil, err
	}
	for id, entry := range r.jobs {
		if entry.Status == "queued" || entry.Status == "running" {
			r.queue <- id
		}
	}
	for range 2 {
		r.workers.Add(1)
		go r.work()
	}
	return r, nil
}

func (r *runner) Close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.cancel()
	r.workers.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.releaseLock != nil {
		_ = r.releaseLock()
		r.releaseLock = nil
	}
}

func (r *runner) load() error {
	file, err := os.Open(filepath.Join(r.config.StateDir, "jobs.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open job state: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	if err != nil || len(data) > maxStateBytes {
		return errors.New("job state exceeds the storage limit or cannot be read")
	}
	var state diskState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode job state: %w", err)
	}
	if state.Version != 1 || len(state.Jobs) > maxJobs {
		return errors.New("unsupported job state version or capacity exceeded")
	}
	for id, stored := range state.Jobs {
		if id != stored.Job.ID || stored.Fingerprint == "" {
			return errors.New("invalid stored job identity")
		}
		entry := stored.Job
		entry.Request, entry.Fingerprint = stored.Request, stored.Fingerprint
		r.jobs[id] = entry
	}
	return nil
}

// persistLocked writes and syncs a replacement before atomically renaming it.
// A caller must hold r.mu and roll back an in-memory change on failure.
func (r *runner) persistLocked() error {
	state := diskState{Version: 1, Jobs: make(map[string]storedJob, len(r.jobs))}
	for id, entry := range r.jobs {
		state.Jobs[id] = storedJob{Job: entry, Request: entry.Request, Fingerprint: entry.Fingerprint}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > maxStateBytes {
		return errors.New("job state capacity exceeded")
	}
	file, err := os.CreateTemp(r.config.StateDir, ".jobs-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(r.config.StateDir, "jobs.json")); err != nil {
		return err
	}
	directory, err := os.Open(r.config.StateDir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func cloneJob(entry job) job {
	data, _ := json.Marshal(entry)
	var clone job
	_ = json.Unmarshal(data, &clone)
	clone.Request, clone.Fingerprint = entry.Request, entry.Fingerprint
	return clone
}

func (r *runner) get(id string) (job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.jobs[id]
	return cloneJob(entry), ok
}

func (r *runner) update(id string, change func(*job)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return context.Canceled
	}
	previous, ok := r.jobs[id]
	if !ok {
		return errors.New("job disappeared")
	}
	next := cloneJob(previous)
	change(&next)
	next.UpdatedAt = time.Now().UTC()
	r.jobs[id] = next
	if err := r.persistLocked(); err != nil {
		r.jobs[id] = previous
		return err
	}
	return nil
}

func (r *runner) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, struct {
			OK bool `json:"ok"`
		}{true})
	})
	mux.HandleFunc("POST /jobs", r.submit)
	mux.HandleFunc("GET /jobs/lookup", r.lookup)
	mux.HandleFunc("GET /jobs/{id}", r.status)
	mux.HandleFunc("GET /jobs/{id}/report", r.report)
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" && r.config.Token != "" {
			provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
			if request.Header.Get("Authorization") == provided || subtle.ConstantTimeCompare([]byte(provided), []byte(r.config.Token)) != 1 {
				writeError(w, http.StatusUnauthorized, "runner authentication required")
				return
			}
		}
		mux.ServeHTTP(w, request)
	})
}

func (r *runner) submit(w http.ResponseWriter, request *http.Request) {
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 512 {
		writeError(w, http.StatusBadRequest, "Idempotency-Key is required and must be at most 512 characters")
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var task taskRequest
	if err := decoder.Decode(&task); err != nil {
		writeError(w, http.StatusBadRequest, "invalid or oversized job request")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request must contain one JSON object")
		return
	}
	if err := r.validateTask(task); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	encoded, _ := json.Marshal(task)
	fingerprint := sha256.Sum256(encoded)
	identity := sha256.Sum256([]byte(key))
	id := "job_" + hex.EncodeToString(identity[:])
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "runner is shutting down")
		return
	}
	if existing, ok := r.jobs[id]; ok {
		r.mu.Unlock()
		if existing.Fingerprint != hex.EncodeToString(fingerprint[:]) {
			writeError(w, http.StatusConflict, "Idempotency-Key was already used with different instructions")
			return
		}
		writeJSON(w, http.StatusOK, existing)
		return
	}
	if len(r.jobs) >= maxJobs {
		r.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "runner job capacity reached; retained state requires operator review")
		return
	}
	baseURL := r.config.PublicURL
	if baseURL == "" {
		scheme := "http"
		if request.TLS != nil {
			scheme = "https"
		}
		baseURL = scheme + "://" + request.Host
	}
	now := time.Now().UTC()
	entry := job{
		ID: id, StatusURL: baseURL + "/jobs/" + id, Status: "queued",
		Progress:  jobProgress{Percent: 0, Message: "Accepted; waiting for execution"},
		CreatedAt: now, UpdatedAt: now, Request: task, Fingerprint: hex.EncodeToString(fingerprint[:]),
	}
	r.jobs[id] = entry
	if err := r.persistLocked(); err != nil {
		delete(r.jobs, id)
		r.mu.Unlock()
		writeError(w, http.StatusInternalServerError, "job could not be durably recorded")
		return
	}
	r.mu.Unlock()
	// This immutable response acknowledges acceptance, never task completion.
	writeJSON(w, http.StatusAccepted, entry)
	select {
	case r.queue <- id:
	case <-r.ctx.Done():
		// The queued job remains persisted and resumes on the next start.
	}
}

func (r *runner) status(w http.ResponseWriter, request *http.Request) {
	entry, ok := r.get(request.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// lookup observes a durably recorded acceptance when the original POST response
// was lost. A missing key is not permission to create a second job.
func (r *runner) lookup(w http.ResponseWriter, request *http.Request) {
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 512 {
		writeError(w, http.StatusBadRequest, "Idempotency-Key is required and must be at most 512 characters")
		return
	}
	identity := sha256.Sum256([]byte(key))
	entry, ok := r.get("job_" + hex.EncodeToString(identity[:]))
	if !ok {
		writeError(w, http.StatusNotFound, "no recorded job for this Idempotency-Key")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (r *runner) report(w http.ResponseWriter, request *http.Request) {
	entry, ok := r.get(request.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if entry.Result == nil {
		writeError(w, http.StatusConflict, "report is not ready")
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `inline; filename="automation-report.md"`)
	_, _ = io.WriteString(w, renderReport(*entry.Result))
}

var repositoryName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+$`)

func (r *runner) validateTask(task taskRequest) error {
	switch task.Task {
	case "repository-watch":
		if !repositoryName.MatchString(task.Repository) || strings.HasSuffix(task.Repository, "/.") || strings.HasSuffix(task.Repository, "/..") || len(task.Repository) > 200 {
			return errors.New("repository-watch requires repository as owner/name")
		}
	case "weekly-summary":
		if len(task.Feeds) == 0 || len(task.Feeds) > maxFeeds {
			return fmt.Errorf("weekly-summary requires between 1 and %d RSS or Atom feed URLs", maxFeeds)
		}
		for _, feed := range task.Feeds {
			if _, err := validHTTPURL(feed); err != nil {
				return fmt.Errorf("invalid feed URL: %w", err)
			}
		}
	default:
		return errors.New("task must be repository-watch or weekly-summary")
	}
	if task.NotifyURL != "" {
		notifyURL, err := validHTTPURL(task.NotifyURL)
		if err != nil {
			return fmt.Errorf("invalid notification URL: %w", err)
		}
		if r.config.NotifyToken != "" {
			origin, _ := validHTTPURL(r.config.NotifyOrigin)
			if !sameOrigin(notifyURL, origin) {
				return errors.New("notification URL must share the configured credential origin")
			}
		}
	}
	return nil
}

func validHTTPURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || len(value) > 2048 || parsed == nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("use an absolute HTTP or HTTPS URL without embedded credentials or a fragment")
	}
	return parsed, nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, struct {
		Error string `json:"error"`
	}{message})
}

func (r *runner) work() {
	defer r.workers.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case id := <-r.queue:
			if r.ctx.Err() != nil {
				return
			}
			r.execute(id)
		}
	}
}

func (r *runner) execute(id string) {
	entry, ok := r.get(id)
	if !ok || (entry.Status != "queued" && entry.Status != "running") {
		return
	}
	if err := r.update(id, func(current *job) {
		current.Status = "running"
		current.Progress = jobProgress{Percent: 10, Message: "Reading source information"}
	}); err != nil {
		log.Printf("runner job %s: persist start: %v", id, err)
		return
	}
	if entry.Result == nil {
		var result report
		var err error
		switch entry.Request.Task {
		case "repository-watch":
			result, err = r.repositoryReport(r.ctx, entry.Request.Repository)
		case "weekly-summary":
			result, err = r.feedReport(r.ctx, entry.Request.Feeds)
		}
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			r.finishFailed(id, err)
			return
		}
		if entry.Request.NotifyURL != "" {
			result.Delivery = &deliveryState{Status: "pending"}
		}
		if err := r.update(id, func(current *job) {
			current.Result = &result
			current.Artifacts = []jobArtifact{{Name: "Automation report", URL: current.StatusURL + "/report", ContentType: "text/markdown"}}
			current.Progress = jobProgress{Percent: 90, Message: "Report ready"}
		}); err != nil {
			log.Printf("runner job %s: persist report: %v", id, err)
			return
		}
	}
	entry, _ = r.get(id)
	if entry.Result.Delivery != nil && entry.Result.Delivery.Status != "sent" && entry.Result.Delivery.Status != "failed" {
		if err := r.deliver(id); err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("runner job %s: notification persistence: %v", id, err)
			}
			return
		}
	}
	if r.ctx.Err() != nil {
		return
	}
	if err := r.update(id, func(current *job) {
		current.Status = "succeeded"
		message := "Report completed"
		if current.Result.Delivery != nil {
			if current.Result.Delivery.Status == "sent" {
				message += "; notification sent"
			} else {
				message += "; notification failed (inspect delivery result)"
			}
		}
		current.Progress = jobProgress{Percent: 100, Message: message}
	}); err != nil {
		log.Printf("runner job %s: persist completion: %v", id, err)
	}
}

func (r *runner) finishFailed(id string, cause error) {
	if err := r.update(id, func(current *job) {
		current.Status = "failed"
		current.Error = truncate(cause.Error(), 1000)
		current.Progress = jobProgress{Percent: 100, Message: "Source information could not be read"}
	}); err != nil {
		log.Printf("runner job %s: persist failure: %v", id, err)
	}
}

func (r *runner) deliver(id string) error {
	for {
		entry, _ := r.get(id)
		delivery := entry.Result.Delivery
		if delivery.Attempts >= maxDeliveries {
			return r.update(id, func(current *job) {
				current.Result.Delivery.Status = "failed"
				if current.Result.Delivery.LastError == "" {
					current.Result.Delivery.LastError = "delivery remained unconfirmed after the maximum number of attempts"
				}
			})
		}
		if err := r.update(id, func(current *job) {
			current.Result.Delivery.Status = "pending"
			current.Result.Delivery.Attempts++
			current.Progress = jobProgress{Percent: 90, Message: "Report ready; delivering notification"}
		}); err != nil {
			return err
		}
		payloadReport := *entry.Result
		payloadReport.Delivery = nil // Every retry has the exact same payload.
		payload, _ := json.Marshal(struct {
			JobID  string `json:"job_id"`
			Task   string `json:"task"`
			Report report `json:"report"`
		}{entry.ID, entry.Request.Task, payloadReport})
		req, err := http.NewRequestWithContext(r.ctx, http.MethodPost, entry.Request.NotifyURL, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "wakeplane-notify:"+id)
		if r.config.NotifyToken != "" {
			req.Header.Set("Authorization", "Bearer "+r.config.NotifyToken)
		}
		response, sendErr := r.client.Do(req)
		if response != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if sendErr == nil && (response.StatusCode < 200 || response.StatusCode >= 300) {
				sendErr = fmt.Errorf("notification endpoint returned HTTP %d", response.StatusCode)
			}
		}
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
		if sendErr == nil {
			now := time.Now().UTC()
			return r.update(id, func(current *job) {
				current.Result.Delivery.Status = "sent"
				current.Result.Delivery.DeliveredAt = &now
				current.Result.Delivery.LastError = ""
			})
		}
		if err := r.update(id, func(current *job) {
			current.Result.Delivery.LastError = truncate(sendErr.Error(), 1000)
		}); err != nil {
			return err
		}
		entry, _ = r.get(id)
		if entry.Result.Delivery.Attempts >= maxDeliveries {
			return r.update(id, func(current *job) { current.Result.Delivery.Status = "failed" })
		}
		timer := time.NewTimer(r.config.RetryDelay * time.Duration(1<<(entry.Result.Delivery.Attempts-1)))
		select {
		case <-timer.C:
		case <-r.ctx.Done():
			timer.Stop()
			return r.ctx.Err()
		}
	}
}
