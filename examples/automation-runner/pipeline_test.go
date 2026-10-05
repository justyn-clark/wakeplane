package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/api"
	"github.com/justyn-clark/wakeplane/internal/app"
	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestWakeplaneRecipesThroughTerminalDelivery(t *testing.T) {
	for _, task := range []string{"repository-watch", "weekly-summary"} {
		t.Run(task, func(t *testing.T) {
			var deliveries atomic.Int32
			source := repositoryFixture(t, nil)
			feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `<rss version="2.0"><channel><title>Project feed</title><item><title>Verified update</title><link>https://example.com/update</link><description>Source excerpt</description></item></channel></rss>`)
			}))
			t.Cleanup(feed.Close)
			notify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodPost || req.Header.Get("Idempotency-Key") == "" {
					t.Error("notification omitted stable delivery contract")
				}
				var payload struct {
					JobID  string `json:"job_id"`
					Report report `json:"report"`
				}
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil || payload.JobID == "" || len(payload.Report.Items) == 0 {
					t.Errorf("notification report missing content: %v", err)
				}
				deliveries.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(notify.Close)
			runner, runnerHTTP := startTestRunner(t, runnerConfig{GitHubURL: source.URL})
			service, err := app.New(context.Background(), config.Config{
				DatabasePath:      filepath.Join(t.TempDir(), "wakeplane.db"),
				SchedulerInterval: time.Millisecond * 20, DispatcherInterval: time.Millisecond * 20,
				LeaseTTL: time.Second * 5, WorkerID: "recipe-test", AuthToken: "operator-test",
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- service.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				if err := service.Close(); err != nil {
					t.Error(err)
				}
				if err := <-done; err != nil {
					t.Error(err)
				}
			})
			control := httptest.NewServer(api.NewMux(service))
			t.Cleanup(control.Close)
			body := map[string]any{"task": task, "notify_url": notify.URL}
			if task == "repository-watch" {
				body["repository"] = "acme/project"
			} else {
				body["feeds"] = []string{feed.URL}
			}
			at := time.Now().UTC().Add(time.Hour)
			schedule, errs, err := service.CreateSchedule(ctx, domain.CreateScheduleRequest{
				Name: task, Enabled: true, Timezone: "UTC",
				Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindOnce, At: &at},
				Target:   domain.TargetSpec{Kind: domain.TargetKindHTTP, Method: "POST", URL: runnerHTTP.URL + "/jobs", Body: body, HTTPJob: &domain.HTTPJobSpec{PollIntervalSeconds: 1}},
				Policy:   domain.Policy{Overlap: domain.OverlapForbid, Misfire: domain.MisfireSkip, TimeoutSeconds: 10, MaxConcurrency: 1},
				Retry:    domain.RetryPolicy{MaxAttempts: 1},
			})
			if err != nil || len(errs) > 0 {
				t.Fatalf("create recipe: %v %+v", err, errs)
			}
			event := []byte(`{"id":"delivery-1","source":"fixture","data":{"record_id":9007199254740993}}`)
			var firstRunID string
			for i, expected := range []int{http.StatusCreated, http.StatusOK} {
				req, _ := http.NewRequest(http.MethodPost, control.URL+"/v1/schedules/"+schedule.ID+"/events", bytes.NewReader(event))
				req.Header.Set("Authorization", "Bearer operator-test")
				resp, err := control.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				var result struct {
					Run domain.Run `json:"run"`
				}
				err = json.NewDecoder(resp.Body).Decode(&result)
				_ = resp.Body.Close()
				if err != nil || resp.StatusCode != expected {
					t.Fatalf("event %d: HTTP%d %v", i, resp.StatusCode, err)
				}
				if i == 0 {
					firstRunID = result.Run.ID
				} else if result.Run.ID != firstRunID {
					t.Fatal("redelivery created another occurrence")
				}
			}
			var completed domain.Run
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				completed, err = service.GetRun(ctx, firstRunID)
				if err != nil {
					t.Fatal(err)
				}
				if completed.Status == domain.RunSucceeded {
					break
				}
				time.Sleep(time.Millisecond * 20)
			}
			if completed.Status != domain.RunSucceeded || completed.ExternalJob == nil || completed.ExternalJob.Status != domain.ExternalJobSucceeded {
				t.Fatalf("recipe did not reach remote success: %+v", completed)
			}
			remote, ok := runner.get(completed.ExternalJob.JobID)
			if !ok || remote.Result == nil || remote.Result.Delivery == nil || remote.Result.Delivery.Status != "sent" || deliveries.Load() != 1 {
				t.Fatalf("notification path incomplete: %+v deliveries=%d", remote, deliveries.Load())
			}
			if !strings.Contains(string(remote.Request.WakeplaneEvent), "9007199254740993") {
				t.Fatal("event number lost precision on outbound runner request")
			}
			if len(completed.ExternalJob.Artifacts) != 1 || len(completed.Receipts) == 0 {
				t.Fatal("missing inspectable output or execution receipts")
			}
			resp, err := runnerHTTP.Client().Get(completed.ExternalJob.Artifacts[0].URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("artifact HTTP%d", resp.StatusCode)
			}
		})
	}
}
