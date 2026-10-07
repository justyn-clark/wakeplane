package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type notificationTransport func(*http.Request) (*http.Response, error)

func (f notificationTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func notificationResponse(req *http.Request, code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: req}
}

func notificationConfig(t *testing.T, transport notificationTransport) runnerConfig {
	t.Helper()
	return runnerConfig{StateDir: t.TempDir(), RetryDelay: time.Millisecond, Client: &http.Client{Transport: transport},
		DiscordWebhookURL: "https://discord.com/api/webhooks/123/test-secret",
		GmailFrom:         "jcn.agent.ops@gmail.com", GmailTo: "jcn.agent.ops@gmail.com",
		GmailClientID: "test-client", GmailClientSecret: "test-secret", GmailRefreshToken: "test-refresh"}
}

func seedNotification(t *testing.T, r *runner, channel, status string) job {
	t.Helper()
	entry := job{ID: "job-test", Fingerprint: "fixture-fingerprint", Status: "running", StatusURL: "https://runner.example/jobs/job-test", Request: taskRequest{Task: "repository-watch", Repository: "justyn-clark/wakeplane", NotifyChannel: channel}, Result: &report{Title: "Wakeplane résumé\r\nBcc: intruder@example.com", Summary: strings.Repeat("🛫", 1500) + " @everyone", GeneratedAt: time.Now().UTC(), Delivery: &deliveryState{Status: status}}}
	r.mu.Lock()
	r.jobs[entry.ID] = entry
	err := r.persistLocked()
	r.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestNativeNotificationPayloadAndConfirmation(t *testing.T) {
	for _, channel := range []string{"discord", "email"} {
		t.Run(channel, func(t *testing.T) {
			var sends, refreshes atomic.Int32
			config := notificationConfig(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == "oauth2.googleapis.com" {
					refreshes.Add(1)
					if err := req.ParseForm(); err != nil {
						t.Fatal(err)
					}
					if req.Form.Get("refresh_token") != "test-refresh" || req.Form.Get("grant_type") != "refresh_token" || req.Form.Get("client_secret") != "test-secret" {
						t.Fatal("incorrect OAuth refresh")
					}
					return notificationResponse(req, 200, `{"access_token":"access-test","token_type":"Bearer","expires_in":3600}`), nil
				}
				sends.Add(1)
				if req.Method != http.MethodPost {
					t.Fatal("notification must POST")
				}
				if req.Header.Get("Idempotency-Key") != "" {
					t.Fatal("provider idempotency is not guaranteed")
				}
				if channel == "discord" {
					if req.URL.Host != "discord.com" || req.URL.Query().Get("wait") != "true" || req.Header.Get("Authorization") != "" {
						t.Fatal("Discord credential/confirmation boundary")
					}
					var payload struct {
						Content         string `json:"content"`
						AllowedMentions struct {
							Parse []string `json:"parse"`
						} `json:"allowed_mentions"`
					}
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					units := 0
					for _, char := range payload.Content {
						units++
						if char > 0xffff {
							units++
						}
					}
					if units > 2000 || payload.AllowedMentions.Parse == nil || len(payload.AllowedMentions.Parse) != 0 {
						t.Fatal("content limit or mention suppression violated")
					}
				} else {
					if req.URL.String() != gmailSendURL || req.Header.Get("Authorization") != "Bearer access-test" {
						t.Fatal("Gmail credential destination")
					}
					var payload struct {
						Raw string `json:"raw"`
					}
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					raw, err := base64.RawURLEncoding.DecodeString(payload.Raw)
					if err != nil {
						t.Fatal(err)
					}
					message, err := mail.ReadMessage(strings.NewReader(string(raw)))
					if err != nil {
						t.Fatal(err)
					}
					if message.Header.Get("To") != "jcn.agent.ops@gmail.com" || message.Header.Get("Bcc") != "" || message.Header.Get("Message-ID") != "<wakeplane-job-test@wakeplane.invalid>" {
						t.Fatal("email headers unsafe")
					}
					body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
					if err != nil || !strings.Contains(string(body), "🛫") {
						t.Fatal("UTF-8 body encoding failed")
					}
				}
				return notificationResponse(req, 200, `{"id":"provider-123"}`), nil
			})
			r, _ := startTestRunner(t, config)
			entry := seedNotification(t, r, channel, "pending")
			r.execute(entry.ID)
			got, _ := r.get(entry.ID)
			if got.Status != "succeeded" || got.Result.Delivery.Status != "sent" || got.Result.Delivery.ProviderMessageID != "provider-123" || sends.Load() != 1 {
				t.Fatalf("delivery=%+v sends=%d", got.Result.Delivery, sends.Load())
			}
			r.execute(entry.ID)
			if sends.Load() != 1 {
				t.Fatal("confirmed delivery resent")
			}
			if channel == "email" && refreshes.Load() != 1 {
				t.Fatal("OAuth refresh not exercised")
			}
		})
	}
}

func TestNativeNotificationUnknownAndRateLimit(t *testing.T) {
	for _, mode := range []string{"rate-limit", "server-error", "missing-id", "transport-error"} {
		t.Run(mode, func(t *testing.T) {
			var sends atomic.Int32
			r, _ := startTestRunner(t, notificationConfig(t, func(req *http.Request) (*http.Response, error) {
				attempt := sends.Add(1)
				switch mode {
				case "rate-limit":
					if attempt == 1 {
						return notificationResponse(req, 429, `{}`), nil
					}
				case "server-error":
					return notificationResponse(req, 500, `{"token":"test-secret"}`), nil
				case "missing-id":
					return notificationResponse(req, 204, ""), nil
				case "transport-error":
					return nil, errors.New("lost response to https://discord.com/api/webhooks/123/test-secret")
				}
				return notificationResponse(req, 200, `{"id":"provider-123"}`), nil
			}))
			entry := seedNotification(t, r, "discord", "pending")
			r.execute(entry.ID)
			got, _ := r.get(entry.ID)
			if mode == "rate-limit" {
				if got.Result.Delivery.Status != "sent" || sends.Load() != 2 || got.Result.Delivery.Attempts != 2 {
					t.Fatalf("rate-limit=%+v sends=%d", got.Result.Delivery, sends.Load())
				}
			} else {
				if got.Result.Delivery.Status != "unknown" || sends.Load() != 1 || !strings.Contains(got.Progress.Message, "unconfirmed") {
					t.Fatalf("unknown=%+v sends=%d", got, sends.Load())
				}
				if strings.Contains(got.Result.Delivery.LastError, "test-secret") {
					t.Fatal("credential leaked into durable result")
				}
				r.execute(entry.ID)
				if sends.Load() != 1 {
					t.Fatal("ambiguous send retried")
				}
			}
		})
	}
}

func TestNativeNotificationRestartDuringSendDoesNotResend(t *testing.T) {
	var sends atomic.Int32
	config := notificationConfig(t, func(req *http.Request) (*http.Response, error) {
		sends.Add(1)
		return notificationResponse(req, 200, `{"id":"unexpected"}`), nil
	})
	r, _ := startTestRunner(t, config)
	entry := seedNotification(t, r, "discord", "sending")
	r.Close()
	r2, _ := startTestRunner(t, config)
	got := waitJob(t, r2, entry.ID, func(j job) bool { return j.Status == "succeeded" })
	if got.Result.Delivery.Status != "unknown" || sends.Load() != 0 {
		t.Fatalf("restart delivery=%+v sends=%d", got.Result.Delivery, sends.Load())
	}
}

func TestNotificationConfigurationAndDestinationBoundaries(t *testing.T) {
	for _, url := range []string{"http://discord.com/api/webhooks/123/token", "https://evil.example/api/webhooks/123/token", "https://discord.com/api/webhooks/123/token?redirect=evil", "https://discord.com:8443/api/webhooks/123/token"} {
		if err := validateDeliveryConfig(runnerConfig{DiscordWebhookURL: url}); err == nil {
			t.Fatalf("accepted unsafe webhook %s", url)
		}
	}
	if err := validateDeliveryConfig(runnerConfig{GmailRefreshToken: "partial"}); err == nil {
		t.Fatal("partial Gmail configuration accepted")
	}
	r, _ := startTestRunner(t, runnerConfig{})
	for _, task := range []taskRequest{{NotifyChannel: "email"}, {NotifyChannel: "discord"}, {NotifyChannel: "sms"}, {NotifyChannel: "webhook"}} {
		if err := r.validateDelivery(task); err == nil {
			t.Fatalf("unconfigured delivery accepted: %+v", task)
		}
	}
	r2, _ := startTestRunner(t, notificationConfig(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") }))
	if err := r2.validateDelivery(taskRequest{NotifyChannel: "discord", NotifyURL: "https://evil.example"}); err == nil {
		t.Fatal("per-request credential destination accepted")
	}
	config := notificationConfig(t, nil)
	config.GmailTo = "safe@example.com\r\nBcc: evil@example.com"
	if err := validateDeliveryConfig(config); err == nil {
		t.Fatal("header injection accepted")
	}
}
