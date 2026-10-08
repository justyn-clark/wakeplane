package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"

	"golang.org/x/oauth2"
)

const gmailSendURL = "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"

var discordWebhookPath = regexp.MustCompile(`^/api(?:/v[0-9]+)?/webhooks/[0-9]+/[A-Za-z0-9_-]+$`)

type notificationError struct {
	message     string
	unconfirmed bool
	retryable   bool
}

func (e *notificationError) Error() string { return e.message }

func nativeDelivery(task taskRequest) bool {
	return task.NotifyChannel == "discord" || task.NotifyChannel == "email"
}

func validateDeliveryConfig(config runnerConfig) error {
	if config.DiscordWebhookURL != "" {
		u, err := validHTTPURL(config.DiscordWebhookURL)
		if err != nil || u.Scheme != "https" || u.Host != "discord.com" || !discordWebhookPath.MatchString(u.Path) || u.RawQuery != "" {
			return errors.New("Discord webhook must be an HTTPS discord.com API webhook URL without query parameters")
		}
	}
	fields := []string{config.GmailFrom, config.GmailTo, config.GmailClientID, config.GmailClientSecret, config.GmailRefreshToken}
	count := 0
	for _, value := range fields {
		if value != "" {
			count++
		}
	}
	if count != 0 && count != len(fields) {
		return errors.New("Gmail delivery requires FROM, TO, CLIENT_ID, CLIENT_SECRET and REFRESH_TOKEN settings")
	}
	if count != 0 {
		for _, value := range []string{config.GmailFrom, config.GmailTo} {
			address, err := mail.ParseAddress(value)
			if err != nil || strings.ContainsAny(value, "\r\n") || address.Address != value {
				return errors.New("Gmail FROM and TO must each be a single plain email address")
			}
		}
	}
	return nil
}

func gmailHTTPClient(ctx context.Context, config runnerConfig, client *http.Client) *http.Client {
	if config.GmailRefreshToken == "" {
		return nil
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, client)
	oauthConfig := oauth2.Config{ClientID: config.GmailClientID, ClientSecret: config.GmailClientSecret,
		Endpoint: oauth2.Endpoint{TokenURL: "https://oauth2.googleapis.com/token", AuthStyle: oauth2.AuthStyleInParams}}
	result := oauth2.NewClient(ctx, oauthConfig.TokenSource(ctx, &oauth2.Token{RefreshToken: config.GmailRefreshToken}))
	result.Timeout = client.Timeout
	result.CheckRedirect = client.CheckRedirect
	return result
}

func (r *runner) validateDelivery(task taskRequest) error {
	switch task.NotifyChannel {
	case "", "webhook":
		if task.NotifyChannel == "webhook" && task.NotifyURL == "" {
			return errors.New("webhook delivery requires notify_url")
		}
	case "discord":
		if r.config.DiscordWebhookURL == "" {
			return errors.New("Discord delivery is not configured on this runner")
		}
	case "email":
		if r.gmailClient == nil {
			return errors.New("Gmail delivery is not configured on this runner")
		}
	default:
		return errors.New("notify_channel must be webhook, discord or email")
	}
	if nativeDelivery(task) && task.NotifyURL != "" {
		return errors.New("Discord/email destinations are runner configuration; omit notify_url")
	}
	return nil
}

func (r *runner) sendNotification(entry job) (string, error) {
	payloadReport := *entry.Result
	payloadReport.Delivery = nil
	client := r.client
	address := entry.Request.NotifyURL
	var payload []byte
	switch entry.Request.NotifyChannel {
	case "discord":
		u, _ := validHTTPURL(r.config.DiscordWebhookURL)
		query := u.Query()
		query.Set("wait", "true")
		u.RawQuery = query.Encode()
		address = u.String()
		// Provider content has a 2000-character limit. Count UTF-16 code units
		// conservatively: astral characters consume two units on the provider.
		content := discordContent(renderReport(payloadReport))
		payload, _ = json.Marshal(struct {
			Content         string `json:"content"`
			AllowedMentions struct {
				Parse []string `json:"parse"`
			} `json:"allowed_mentions"`
		}{Content: content, AllowedMentions: struct {
			Parse []string `json:"parse"`
		}{Parse: []string{}}})
	case "email":
		address = gmailSendURL
		client = r.gmailClient
		payload, _ = json.Marshal(struct {
			Raw string `json:"raw"`
		}{Raw: base64.RawURLEncoding.EncodeToString(r.emailMessage(entry, payloadReport))})
	default:
		payload, _ = json.Marshal(struct {
			JobID  string `json:"job_id"`
			Task   string `json:"task"`
			Report report `json:"report"`
		}{entry.ID, entry.Request.Task, payloadReport})
	}
	req, err := http.NewRequestWithContext(r.ctx, http.MethodPost, address, bytes.NewReader(payload))
	if err != nil {
		return "", errors.New("notification request could not be constructed")
	}
	req.Header.Set("Content-Type", "application/json")
	if !nativeDelivery(entry.Request) {
		req.Header.Set("Idempotency-Key", "wakeplane-notify:"+entry.ID)
		if r.config.NotifyToken != "" {
			req.Header.Set("Authorization", "Bearer "+r.config.NotifyToken)
		}
	}
	response, err := client.Do(req)
	if response != nil {
		defer response.Body.Close()
	}
	if err != nil {
		// HTTP errors can contain the full URL (including the Discord token),
		// and OAuth error bodies can contain provider details. Never persist them.
		return "", &notificationError{message: "notification transport or authorization failed; acceptance unconfirmed", unconfirmed: nativeDelivery(entry.Request), retryable: !nativeDelivery(entry.Request)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		if !nativeDelivery(entry.Request) {
			return "", fmt.Errorf("notification endpoint returned HTTP %d", response.StatusCode)
		}
		return "", &notificationError{message: fmt.Sprintf("notification provider returned HTTP %d", response.StatusCode), unconfirmed: response.StatusCode >= 500, retryable: response.StatusCode == http.StatusTooManyRequests}
	}
	if !nativeDelivery(entry.Request) {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return "", nil
	}
	var confirmation struct {
		ID string `json:"id"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4096))
	if err := decoder.Decode(&confirmation); err != nil || confirmation.ID == "" {
		return "", &notificationError{message: "notification response did not confirm a provider message ID", unconfirmed: true}
	}
	return confirmation.ID, nil
}

func discordContent(text string) string {
	units := 0
	var result strings.Builder
	for _, char := range text {
		size := 1
		if char > 0xffff {
			size = 2
		}
		if units+size > 1900 {
			result.WriteString("\n[Report shortened; open the runner report for full details.]")
			break
		}
		result.WriteRune(char)
		units += size
	}
	return result.String()
}

func (r *runner) emailMessage(entry job, result report) []byte {
	var message bytes.Buffer
	parts := multipart.NewWriter(&message)
	subject := strings.Join(strings.Fields(asciiText(result.Title)), " ")
	fmt.Fprintf(&message, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <wakeplane-%s@wakeplane.invalid>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", r.config.GmailFrom, r.config.GmailTo, subject, entry.ID, parts.Boundary())
	for _, body := range []struct{ contentType, text string }{
		{"text/plain", renderEmailText(result)},
		{"text/html", renderEmailHTML(result)},
	} {
		header := textproto.MIMEHeader{"Content-Type": {body.contentType + "; charset=us-ascii"}, "Content-Transfer-Encoding": {"quoted-printable"}}
		part, _ := parts.CreatePart(header) // bytes.Buffer cannot fail writes.
		writer := quotedprintable.NewWriter(part)
		_, _ = io.WriteString(writer, body.text)
		_ = writer.Close()
	}
	_ = parts.Close()
	return message.Bytes()
}
