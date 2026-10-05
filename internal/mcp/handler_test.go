package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/justyn-clark/wakeplane/internal/app"
	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

func newTestService(t *testing.T) *app.Service {
	t.Helper()
	service, err := app.New(context.Background(), config.Config{
		DatabasePath: filepath.Join(t.TempDir(), "wakeplane.db"), Version: "mcp-test",
		SchedulerInterval: time.Hour, DispatcherInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	return service
}

func connectClient(t *testing.T, handler http.Handler, protocol string) (*sdk.ClientSession, context.Context) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	client := sdk.NewClient(&sdk.Implementation{Name: "wakeplane-interoperability-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{
		Endpoint: server.URL, HTTPClient: server.Client(), DisableStandaloneSSE: true, MaxRetries: -1,
	}, &sdk.ClientSessionOptions{ProtocolVersion: protocol})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	return session, ctx
}

func callTool(t *testing.T, ctx context.Context, session *sdk.ClientSession, name string, args any) *sdk.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func decodeTool[T any](t *testing.T, result *sdk.CallToolResult) T {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool failed: %+v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatal("missing legacy text fallback")
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode tool result %s: %v", data, err)
	}
	return value
}

func draft(name string) map[string]any {
	return map[string]any{
		"name": name, "timezone": "America/Los_Angeles",
		"schedule": map[string]any{"kind": "interval", "every_seconds": 3600},
		"target":   map[string]any{"kind": "workflow", "workflow_id": "sync.customers"},
	}
}

func TestOfficialClientNegotiationAndToolDiscovery(t *testing.T) {
	for _, protocol := range []string{"", "2025-11-25", "2025-06-18", "2025-03-26"} {
		t.Run(protocol, func(t *testing.T) {
			service := newTestService(t)
			session, ctx := connectClient(t, NewHandler(service, service.Version()), protocol)
			info := session.InitializeResult()
			if info.ServerInfo.Name != "wakeplane" || info.ServerInfo.Version != "mcp-test" {
				t.Fatalf("incorrect server identity: %+v", info.ServerInfo)
			}
			if protocol != "" && info.ProtocolVersion != protocol {
				t.Fatalf("negotiated %q, want %q", info.ProtocolVersion, protocol)
			}
			if info.Capabilities.Tools == nil || info.Capabilities.Tools.ListChanged {
				t.Fatalf("incorrect tool capabilities: %+v", info.Capabilities)
			}
			capabilityJSON, err := json.Marshal(info.Capabilities)
			if err != nil {
				t.Fatal(err)
			}
			var capabilityMap map[string]any
			if err := json.Unmarshal(capabilityJSON, &capabilityMap); err != nil {
				t.Fatal(err)
			}
			if _, ok := capabilityMap["tasks"]; ok {
				t.Fatal("must not advertise experimental Tasks")
			}
			if err := session.Ping(ctx, &sdk.PingParams{}); err != nil {
				t.Fatal(err)
			}
			list, err := session.ListTools(ctx, &sdk.ListToolsParams{})
			if err != nil {
				t.Fatal(err)
			}
			if len(list.Tools) != 12 {
				t.Fatalf("got %d tools, want 12", len(list.Tools))
			}
			tools := map[string]*sdk.Tool{}
			for _, tool := range list.Tools {
				if tool.InputSchema == nil || tool.Description == "" || tool.Annotations == nil {
					t.Fatalf("incomplete tool metadata: %+v", tool)
				}
				tools[tool.Name] = tool
			}
			if tools["wakeplane_get_run"] == nil || !tools["wakeplane_get_run"].Annotations.ReadOnlyHint {
				t.Fatal("run inspection must be read-only")
			}
			if tools["wakeplane_create_schedule"].Annotations.IdempotentHint || tools["wakeplane_trigger_schedule"].Annotations.IdempotentHint {
				t.Fatal("create/manual trigger must not promise replay safety")
			}
			if !tools["wakeplane_send_event"].Annotations.IdempotentHint {
				t.Fatal("event delivery should advertise deduplication")
			}
			if !*tools["wakeplane_update_schedule"].Annotations.DestructiveHint {
				t.Fatal("full replacement must advertise destructive mutation")
			}
			createSchema, err := json.Marshal(tools["wakeplane_create_schedule"].InputSchema)
			if err != nil || !strings.Contains(string(createSchema), `"http_job"`) || !strings.Contains(string(createSchema), `"poll_interval_seconds"`) {
				t.Fatalf("typed external job settings missing from discovery: %s %v", createSchema, err)
			}
			if tools["wakeplane_delete_schedule"] != nil {
				t.Fatal("unexpected destructive deletion tool")
			}
			status := decodeTool[domain.StatusResponse](t, callTool(t, ctx, session, "wakeplane_status", map[string]any{}))
			if status.Version != "mcp-test" {
				t.Fatalf("status: %+v", status)
			}
		})
	}
}

func TestOfficialClientCompletionIncludesOutcomeEvidence(t *testing.T) {
	service := newTestService(t)
	service.RegisterWorkflow("sync.customers", func(context.Context, map[string]any) (map[string]any, error) {
		return map[string]any{"summary": "Maintenance completed", "artifact": "report-2026-10-05"}, nil
	})
	session, ctx := connectClient(t, NewHandler(service), "2025-11-25")
	created := decodeTool[domain.Schedule](t, callTool(t, ctx, session, "wakeplane_create_schedule", draft("Complete developer use case")))
	runtimeCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- service.Run(runtimeCtx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	triggered := decodeTool[domain.Run](t, callTool(t, ctx, session, "wakeplane_trigger_schedule", map[string]any{"schedule_id": created.ID, "reason": "User authorized the maintenance run"}))
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		run := decodeTool[domain.Run](t, callTool(t, ctx, session, "wakeplane_get_run", map[string]any{"run_id": triggered.ID}))
		if run.Status == domain.RunSucceeded {
			if run.FinishedAt == nil || len(run.Receipts) == 0 || !strings.Contains(string(run.ResultJSON), "Maintenance completed") {
				t.Fatalf("success without final evidence: %+v", run)
			}
			return
		}
		if run.Status == domain.RunFailed || run.Status == domain.RunDeadLettered {
			t.Fatalf("workflow failed: %+v", run)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("workflow did not finish: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestOfficialClientCreatesTypedExternalJobDraft(t *testing.T) {
	service := newTestService(t)
	session, ctx := connectClient(t, NewHandler(service), "2025-11-25")
	input := draft("Track an external coding agent")
	input["target"] = map[string]any{"kind": "http", "method": "POST", "url": "https://runner.example/jobs", "http_job": map[string]any{"poll_interval_seconds": 5}}
	created := decodeTool[domain.Schedule](t, callTool(t, ctx, session, "wakeplane_create_schedule", input))
	if created.Target.HTTPJob == nil || created.Target.HTTPJob.PollIntervalSeconds != 5 || created.Enabled {
		t.Fatalf("typed external job draft was not preserved: %+v", created)
	}
}

type unavailableService struct{ Service }

func (unavailableService) Status(context.Context) (domain.StatusResponse, error) {
	return domain.StatusResponse{}, errors.New("database credential must-not-leak")
}

func TestInternalToolErrorsDoNotExposeBackendDetails(t *testing.T) {
	service := newTestService(t)
	session, ctx := connectClient(t, NewHandler(unavailableService{Service: service}), "2025-11-25")
	result := callTool(t, ctx, session, "wakeplane_status", map[string]any{})
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(string(data), "internal_error") || strings.Contains(string(data), "must-not-leak") {
		t.Fatalf("backend error was not safely reported: %s", data)
	}
}

func TestEventToolPreservesNumbersAndRejectsChangedNumericReplay(t *testing.T) {
	service := newTestService(t)
	session, ctx := connectClient(t, NewHandler(service), "2025-11-25")
	input := draft("Precise event payload")
	input["enabled"] = true
	schedule := decodeTool[domain.Schedule](t, callTool(t, ctx, session, "wakeplane_create_schedule", input))
	data := map[string]any{"record_id": json.Number("9007199254740993")}
	args := map[string]any{"schedule_id": schedule.ID, "event": map[string]any{"id": "precise-event", "source": "fixture", "data": data}}
	result := callTool(t, ctx, session, "wakeplane_send_event", args)
	if result.IsError {
		t.Fatalf("event creation: %+v", result.Content)
	}
	if len(result.Content) == 0 || !strings.Contains(result.Content[0].(*sdk.TextContent).Text, "9007199254740993") {
		t.Fatalf("MCP output rounded event data: %+v", result.Content)
	}
	runs, _, err := service.ListRuns(ctx, &schedule.ID, nil, nil, 50, "")
	if err != nil || len(runs) != 1 {
		t.Fatalf("stored runs: %+v %v", runs, err)
	}
	run, err := service.GetRun(ctx, runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := run.Event.Data["record_id"].(json.Number)
	if !ok || value.String() != "9007199254740993" {
		t.Fatalf("MCP input/storage rounded event data: %#v", run.Event.Data["record_id"])
	}
	data["record_id"] = json.Number("9007199254740992")
	if !callTool(t, ctx, session, "wakeplane_send_event", args).IsError {
		t.Fatal("different numeric payload accepted as identical replay")
	}
}

func TestOfficialClientScheduleAndDurableRunLifecycle(t *testing.T) {
	service := newTestService(t)
	session, ctx := connectClient(t, NewHandler(service), "2025-11-25")
	created := decodeTool[domain.Schedule](t, callTool(t, ctx, session, "wakeplane_create_schedule", draft("Assistant maintenance")))
	if created.Enabled || created.Timezone != "America/Los_Angeles" {
		t.Fatalf("unexpected default schedule: %+v", created)
	}
	stored, err := service.GetSchedule(ctx, created.ID)
	if err != nil || stored.ID != created.ID {
		t.Fatalf("schedule was not saved: %+v %v", stored, err)
	}
	if stored.Policy != domain.DefaultPolicy() || stored.Retry != domain.DefaultRetryPolicy() {
		t.Fatalf("service defaults were bypassed: %+v", stored)
	}
	inspected := decodeTool[domain.Schedule](t, callTool(t, ctx, session, "wakeplane_get_schedule", map[string]any{"schedule_id": created.ID}))
	if inspected.ID != stored.ID {
		t.Fatal("schedule inspection mismatch")
	}
	preview := decodeTool[struct {
		Times []time.Time `json:"times"`
	}](t, callTool(t, ctx, session, "wakeplane_preview_schedule", draft("Unsaved preview")))
	if len(preview.Times) != 5 {
		t.Fatalf("preview: %+v", preview)
	}
	items, _, err := service.ListSchedules(ctx, nil, 50, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("preview unexpectedly persisted work: %d %v", len(items), err)
	}

	replacement := draft("Revised maintenance")
	replacement["enabled"] = true
	updated := decodeTool[domain.Schedule](t, callTool(t, ctx, session, "wakeplane_update_schedule", map[string]any{"schedule_id": created.ID, "schedule": replacement}))
	if updated.ID != created.ID || updated.Name != "Revised maintenance" || !updated.Enabled {
		t.Fatalf("update: %+v", updated)
	}
	paused := decodeTool[domain.Schedule](t, callTool(t, ctx, session, "wakeplane_pause_schedule", map[string]any{"schedule_id": created.ID}))
	if paused.Enabled || paused.PausedAt == nil {
		t.Fatal("pause did not persist")
	}
	resumed := decodeTool[domain.Schedule](t, callTool(t, ctx, session, "wakeplane_resume_schedule", map[string]any{"schedule_id": created.ID}))
	if !resumed.Enabled || resumed.PausedAt != nil {
		t.Fatal("resume did not persist")
	}
	triggered := decodeTool[domain.Run](t, callTool(t, ctx, session, "wakeplane_trigger_schedule", map[string]any{"schedule_id": created.ID, "reason": "Authorized test"}))
	if triggered.Status != domain.RunPending || !strings.HasPrefix(triggered.OccurrenceKey, "manual:") {
		t.Fatalf("manual run: %+v", triggered)
	}
	run := decodeTool[domain.Run](t, callTool(t, ctx, session, "wakeplane_get_run", map[string]any{"run_id": triggered.ID}))
	if run.ID != triggered.ID || run.Status != domain.RunPending {
		t.Fatal("run must be durably inspectable before execution")
	}
	runs := decodeTool[domain.ListResponse[domain.RunSummary]](t, callTool(t, ctx, session, "wakeplane_list_runs", map[string]any{"schedule_id": created.ID, "status": "pending"}))
	if len(runs.Items) != 1 || runs.Items[0].ID != triggered.ID {
		t.Fatalf("run list: %+v", runs)
	}

	eventArgs := map[string]any{"schedule_id": created.ID, "event": map[string]any{"id": "evt-1", "source": "github", "data": map[string]any{"build": "ready"}}}
	type eventResult struct {
		Run     domain.Run `json:"run"`
		Created bool       `json:"created"`
	}
	first := decodeTool[eventResult](t, callTool(t, ctx, session, "wakeplane_send_event", eventArgs))
	again := decodeTool[eventResult](t, callTool(t, ctx, session, "wakeplane_send_event", eventArgs))
	if !first.Created || again.Created || first.Run.ID != again.Run.ID {
		t.Fatalf("event replay created duplicate work: %+v %+v", first, again)
	}
	eventArgs["event"].(map[string]any)["data"] = map[string]any{"build": "changed"}
	conflict := callTool(t, ctx, session, "wakeplane_send_event", eventArgs)
	if !conflict.IsError {
		t.Fatal("conflicting event replay should fail")
	}
}

func TestInvalidToolArgumentsCannotMutateSchedules(t *testing.T) {
	service := newTestService(t)
	session, ctx := connectClient(t, NewHandler(service), "2025-11-25")
	for name, change := range map[string]func(map[string]any){
		"missing timezone": func(in map[string]any) { delete(in, "timezone") },
		"empty timezone":   func(in map[string]any) { in["timezone"] = "" },
		"invalid timezone": func(in map[string]any) { in["timezone"] = "Mars/Olympus" },
		"unknown argument": func(in map[string]any) { in["run_now"] = true },
		"wrong type":       func(in map[string]any) { in["enabled"] = "true" },
		"invalid schedule": func(in map[string]any) { in["schedule"] = map[string]any{"kind": "interval", "every_seconds": -1} },
		"invalid policy": func(in map[string]any) {
			in["policy"] = map[string]any{"overlap": "bypass", "misfire": "skip", "timeout_seconds": 1, "max_concurrency": 1}
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := draft(name)
			change(input)
			result := callTool(t, ctx, session, "wakeplane_create_schedule", input)
			if !result.IsError {
				t.Fatalf("invalid input accepted: %+v", result)
			}
		})
	}
	for _, arguments := range []map[string]any{{"limit": -1}, {"limit": 101}, {"cursor": "not-a-cursor"}} {
		if !callTool(t, ctx, session, "wakeplane_list_schedules", arguments).IsError {
			t.Fatalf("invalid pagination accepted: %+v", arguments)
		}
	}
	if !callTool(t, ctx, session, "wakeplane_get_schedule", map[string]any{"schedule_id": ""}).IsError {
		t.Fatal("empty ID accepted")
	}
	if !callTool(t, ctx, session, "wakeplane_list_runs", map[string]any{"status": "invented"}).IsError {
		t.Fatal("invalid run status accepted")
	}
	if !callTool(t, ctx, session, "wakeplane_list_runs", map[string]any{"target_kind": "invented"}).IsError {
		t.Fatal("invalid target kind accepted")
	}
	_, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "not_a_wakeplane_tool", Arguments: map[string]any{}})
	if err == nil {
		t.Fatal("unknown tools require a protocol error")
	}
	items, _, err := service.ListSchedules(ctx, nil, 50, "")
	if err != nil || len(items) != 0 {
		t.Fatalf("invalid tool mutated schedules: %+v %v", items, err)
	}
}

func TestHTTPTransportBoundaries(t *testing.T) {
	service := newTestService(t)
	handler := NewHandler(service)
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	for _, test := range []struct {
		name, method, body, origin, version, contentType string
		want                                             int
	}{
		{"initialize", "POST", initialize, "", "", "application/json", 200},
		{"same origin", "POST", initialize, "http://example.com", "", "application/json", 200},
		{"cross origin", "POST", initialize, "http://attacker.test", "", "application/json", 403},
		{"null origin", "POST", initialize, "null", "", "application/json", 403},
		{"cross origin GET", "GET", "", "http://attacker.test", "", "", 403},
		{"origin path", "POST", initialize, "http://example.com/", "", "application/json", 403},
		{"origin credentials", "POST", initialize, "http://test@example.com", "", "application/json", 403},
		{"GET has no push stream", "GET", "", "", "", "", 405},
		{"DELETE has no session", "DELETE", "", "", "", "", 405},
		{"unsupported protocol", "POST", initialize, "", "2000-01-01", "application/json", 400},
		{"invalid JSON", "POST", `{"jsonrpc":`, "", "", "application/json", 400},
		{"invalid batch", "POST", `[]`, "", "", "application/json", 400},
		{"wrong content type", "POST", initialize, "", "", "text/plain", 415},
		{"oversized body", "POST", strings.Repeat(" ", maxRequestBytes+1), "", "", "application/json", 413},
		{"notification", "POST", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "", "2025-11-25", "application/json", 202},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "http://example.com/v1/mcp", bytes.NewBufferString(test.body))
			req.Header.Set("Accept", "application/json, text/event-stream")
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			if test.version != "" {
				req.Header.Set("MCP-Protocol-Version", test.version)
			}
			if test.contentType != "" {
				req.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != test.want {
				t.Fatalf("got HTTP %d, want %d; %s", response.Code, test.want, response.Body.String())
			}
			if response.Header().Get("MCP-Session-Id") != "" {
				t.Fatal("stateless endpoint emitted session state")
			}
			if response.Code == http.StatusAccepted && response.Body.Len() != 0 {
				t.Fatal("notifications must have an empty body")
			}
			if response.Code == http.StatusOK {
				if response.Header().Get("Content-Type") != "application/json" {
					t.Fatal("expected JSON response")
				}
				var rpc struct {
					JSONRPC string `json:"jsonrpc"`
					ID      int    `json:"id"`
					Result  any    `json:"result"`
				}
				if err := json.NewDecoder(response.Body).Decode(&rpc); err != nil || rpc.JSONRPC != "2.0" || rpc.ID != 1 || rpc.Result == nil {
					t.Fatalf("invalid RPC response: %+v %v", rpc, err)
				}
			}
		})
	}
	// Chunked bodies are bounded even when Content-Length is unavailable.
	req := httptest.NewRequest("POST", "http://example.com/v1/mcp", io.NopCloser(strings.NewReader(strings.Repeat(" ", maxRequestBytes+1))))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("unbounded chunked request: HTTP %d", response.Code)
	}
}
