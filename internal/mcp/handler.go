// Package mcp exposes Wakeplane's application service through MCP tools.
// It does not execute targets: every mutation follows the same service path
// as the REST API, including validation and durable run recording.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

const (
	maxRequestBytes  = 1 << 20
	operationTimeout = 30 * time.Second
)

// Service is the application boundary used by assistant tools.
type Service interface {
	CreateSchedule(context.Context, domain.CreateScheduleRequest) (domain.Schedule, []domain.ValidationError, error)
	ReplaceSchedule(context.Context, string, domain.UpdateScheduleRequest) (domain.Schedule, []domain.ValidationError, error)
	ListSchedules(context.Context, *bool, int, string) ([]domain.ScheduleSummary, *string, error)
	GetSchedule(context.Context, string) (domain.Schedule, error)
	PauseSchedule(context.Context, string) (domain.Schedule, error)
	ResumeSchedule(context.Context, string) (domain.Schedule, error)
	TriggerSchedule(context.Context, string, string) (domain.Run, error)
	ListRuns(context.Context, *string, *domain.RunStatus, *domain.TargetKind, int, string) ([]domain.RunSummary, *string, error)
	GetRun(context.Context, string) (domain.Run, error)
	Status(context.Context) (domain.StatusResponse, error)
}

type previewService interface {
	PreviewSchedule(context.Context, domain.CreateScheduleRequest) ([]time.Time, []domain.ValidationError, error)
}

type eventService interface {
	TriggerEvent(context.Context, string, domain.TriggerEvent) (domain.Run, bool, error)
}

// NewHandler returns a stateless Streamable HTTP MCP endpoint. Mount this inside
// the API's authentication and request audit middleware, not as a public bypass.
// The optional version identifies the running Wakeplane build to clients.
// This endpoint supports request/response tools, without server push or MCP Tasks.
func NewHandler(service Service, version ...string) http.Handler {
	buildVersion := "dev"
	if len(version) > 0 && version[0] != "" {
		buildVersion = version[0]
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "wakeplane", Version: buildVersion}, &sdk.ServerOptions{
		Instructions: "Wakeplane schedules typed work and records durable runs. Require user authorization before creating, replacing, enabling, or triggering work. Require an explicit IANA timezone. Treat run status pending or running as unfinished. Inspect runs for final receipts. New schedules are disabled by default. Create and manual trigger are not idempotent: never blindly replay them after an uncertain response.",
		Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}},
		// Advertise the revisions covered by real-client interoperability tests.
		// Newer SDK clients can negotiate down through discover/initialize.
		SupportedProtocolVersions: []string{"2025-11-25", "2025-06-18", "2025-03-26"},
	})
	registerTools(server, service)
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: maxRequestBytes,
		PropagateRequestCancellation: true,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate Origin for every HTTP method, including GET, which standard
		// cross-origin middleware commonly exempts as a safe method.
		if !validOrigin(r) {
			http.Error(w, "Forbidden: MCP requires a same-origin request", http.StatusForbidden)
			return
		}
		transport.ServeHTTP(w, r)
	})
}

func validOrigin(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true // Native MCP clients normally do not send Origin.
	}
	if len(origins) != 1 || origins[0] == "" {
		return false
	}
	origin, err := url.Parse(origins[0])
	if err != nil || origin.User != nil || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return origin.Scheme == scheme && strings.EqualFold(origin.Host, r.Host)
}

func addTool[In any](server *sdk.Server, name, title, description string, readOnly, destructive, openWorld bool, handler func(context.Context, In) (any, error), idempotent ...bool) {
	isIdempotent := len(idempotent) > 0 && idempotent[0]
	sdk.AddTool(server, &sdk.Tool{
		Name: name, Title: title, Description: description,
		Annotations: &sdk.ToolAnnotations{
			ReadOnlyHint: readOnly, DestructiveHint: &destructive,
			OpenWorldHint: &openWorld, IdempotentHint: isIdempotent,
		},
	}, func(ctx context.Context, request *sdk.CallToolRequest, input In) (*sdk.CallToolResult, any, error) {
		ctx, cancel := context.WithTimeout(ctx, operationTimeout)
		defer cancel()
		// The SDK validates the typed schema before reaching this handler. Read
		// the original arguments again with exact JSON numbers: its default map
		// decoding rounds large numeric event payloads and can hide conflicts.
		if request.Params.Arguments != nil {
			decoder := json.NewDecoder(bytes.NewReader(request.Params.Arguments))
			decoder.UseNumber()
			if err := decoder.Decode(&input); err != nil {
				return nil, nil, domain.NewBadRequestError("invalid tool arguments")
			}
		}
		output, err := handler(ctx, input)
		if err == nil {
			return nil, output, nil
		}
		payload := domain.ErrorResponse{Code: "internal_error", Error: "Wakeplane could not complete the operation"}
		var apiErr *domain.APIError
		if errors.As(err, &apiErr) {
			payload = domain.ErrorResponse{Code: apiErr.Code, Error: apiErr.Message, Details: apiErr.Details}
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			payload = domain.ErrorResponse{Code: "timeout", Error: "The operation was cancelled or timed out; inspect current state before retrying a mutation"}
		}
		return &sdk.CallToolResult{IsError: true}, payload, nil
	})
}
