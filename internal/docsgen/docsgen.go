package docsgen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/justyn-clark/wakeplane/internal/cli"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

type Output struct {
	Path    string
	Content string
}

type Route struct {
	Method      string
	Path        string
	Description string
}

var routeDescriptions = map[string]string{
	"GET /healthz":                    "Liveness probe. Returns `{\"ok\":true}`.",
	"GET /readyz":                     "Readiness probe. Returns `{\"ok\":true,\"storage\":\"ok\"}` when the store is reachable.",
	"GET /v1/status":                  "Operational status including scheduler timing, worker counts, and run counts.",
	"GET /v1/metrics":                 "Prometheus text metrics for schedules, runs, leases, and executor outcomes.",
	"POST /v1/schedules":              "Create a schedule. Returns `201` with the full schedule.",
	"GET /v1/schedules":               "List schedules. Supports `enabled`, `limit`, and `cursor` query params.",
	"GET /v1/schedules/{id}":          "Get one schedule including computed `next_run_at`.",
	"PUT /v1/schedules/{id}":          "Replace a schedule. All fields required.",
	"PATCH /v1/schedules/{id}":        "Patch a schedule. Only provided fields change.",
	"DELETE /v1/schedules/{id}":       "Delete a schedule and its dependent runs, leases, receipts, and dead letters.",
	"POST /v1/schedules/{id}/pause":   "Pause a schedule by setting `enabled=false` and recording `paused_at`.",
	"POST /v1/schedules/{id}/resume":  "Resume a schedule by setting `enabled=true`, clearing `paused_at`, and recomputing `next_run_at`.",
	"POST /v1/schedules/{id}/trigger": "Create a manual run immediately. Requires `{\"reason\":\"...\"}`.",
	"GET /v1/schedules/{id}/runs":     "List runs for a specific schedule. Supports `status`, `limit`, and `cursor`.",
	"GET /v1/runs":                    "List runs across all schedules. Supports `schedule_id`, `status`, `limit`, and `cursor`.",
	"GET /v1/runs/{id}":               "Get one run including result fields.",
	"GET /v1/runs/{id}/receipts":      "List execution receipts for a run.",
}

func Generate(repoRoot string) ([]Output, error) {
	version, err := currentVersion(repoRoot)
	if err != nil {
		return nil, err
	}
	routes, err := Routes(repoRoot)
	if err != nil {
		return nil, err
	}
	outputs := []Output{
		{
			Path:    filepath.Join(repoRoot, "docs/public/cli.md"),
			Content: generateCLIReference(version),
		},
		{
			Path:    filepath.Join(repoRoot, "docs/public/api.md"),
			Content: generateAPIReference(version, routes),
		},
	}
	return outputs, nil
}

func Check(repoRoot string) error {
	outputs, err := Generate(repoRoot)
	if err != nil {
		return err
	}
	for _, output := range outputs {
		current, err := os.ReadFile(output.Path)
		if err != nil {
			return err
		}
		if string(current) != output.Content {
			return fmt.Errorf("%s is out of date; run go run ./tools/docsgen", output.Path)
		}
	}
	return nil
}

func Write(repoRoot string) error {
	outputs, err := Generate(repoRoot)
	if err != nil {
		return err
	}
	for _, output := range outputs {
		if err := os.WriteFile(output.Path, []byte(output.Content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func Routes(repoRoot string) ([]Route, error) {
	src, err := os.ReadFile(filepath.Join(repoRoot, "internal/api/http.go"))
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) ([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(src), -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no API routes found")
	}
	routes := make([]Route, 0, len(matches))
	for _, match := range matches {
		key := match[1] + " " + match[2]
		routes = append(routes, Route{
			Method:      match[1],
			Path:        match[2],
			Description: routeDescriptions[key],
		})
	}
	return routes, nil
}

func currentVersion(repoRoot string) (string, error) {
	src, err := os.ReadFile(filepath.Join(repoRoot, "cmd/wakeplane/main.go"))
	if err != nil {
		return "", err
	}
	re := regexp.MustCompile(`const version = "([^"]+)"`)
	match := re.FindStringSubmatch(string(src))
	if len(match) != 2 {
		return "", fmt.Errorf("could not determine current version")
	}
	return match[1], nil
}

func generateCLIReference(version string) string {
	sections := []struct {
		Title string
		Args  []string
	}{
		{Title: "Root Command", Args: nil},
		{Title: "`serve`", Args: []string{"serve"}},
		{Title: "`schedule`", Args: []string{"schedule"}},
		{Title: "`schedule create`", Args: []string{"schedule", "create"}},
		{Title: "`schedule list`", Args: []string{"schedule", "list"}},
		{Title: "`schedule get`", Args: []string{"schedule", "get"}},
		{Title: "`schedule pause`", Args: []string{"schedule", "pause"}},
		{Title: "`schedule resume`", Args: []string{"schedule", "resume"}},
		{Title: "`schedule delete`", Args: []string{"schedule", "delete"}},
		{Title: "`schedule trigger`", Args: []string{"schedule", "trigger"}},
		{Title: "`run`", Args: []string{"run"}},
		{Title: "`run list`", Args: []string{"run", "list"}},
		{Title: "`run get`", Args: []string{"run", "get"}},
	}

	var b strings.Builder
	b.WriteString("<!-- Code generated by go run ./tools/docsgen; DO NOT EDIT. -->\n")
	b.WriteString("# CLI Reference\n\n")
	b.WriteString("This page is generated from the real Cobra command tree in `internal/cli/root.go`.\n\n")
	b.WriteString("> **Status:** current public operator surface for Wakeplane `")
	b.WriteString(version)
	b.WriteString("`. If a command is not listed here, it is not shipped.\n\n")

	for _, section := range sections {
		help, err := commandHelp(version, section.Args...)
		if err != nil {
			panic(err)
		}
		b.WriteString("## ")
		b.WriteString(section.Title)
		b.WriteString("\n\n```text\n")
		b.WriteString(strings.TrimSpace(help))
		b.WriteString("\n```\n\n")
	}

	return b.String()
}

func generateAPIReference(version string, routes []Route) string {
	health, operational, schedules, runs := groupRoutes(routes)

	var b strings.Builder
	b.WriteString("<!-- Code generated by go run ./tools/docsgen; DO NOT EDIT. -->\n")
	b.WriteString("# API Reference\n\n")
	b.WriteString("Wakeplane exposes a JSON HTTP API for schedule and run management. The route tables on this page are generated from `internal/api/http.go` so the published surface stays aligned with the server.\n\n")
	b.WriteString("> **Operator warning:** Wakeplane currently has no auth or RBAC. Bind it to localhost, a trusted subnet, VPN, Tailscale, or a reverse-proxied private network. Do not expose it directly to the public internet. See [Security](security.md).\n\n")

	writeRouteTable(&b, "Health and readiness", health)
	writeRouteTable(&b, "Operational status and metrics", operational)
	writeRouteTable(&b, "Schedule management", schedules)
	writeRouteTable(&b, "Run inspection", runs)

	b.WriteString("## Error envelope\n\n")
	b.WriteString("All API errors return JSON with this shape:\n\n```json\n{\n  \"code\": \"string\",\n  \"error\": \"human-readable message\",\n  \"details\": []\n}\n```\n\n")
	b.WriteString("| HTTP Status | Code | When |\n|---|---|---|\n")
	b.WriteString("| 400 | `bad_request` | Malformed JSON, invalid query parameters, or invalid trigger reason |\n")
	b.WriteString("| 400 | `validation_failed` | Schedule create or patch validation failed |\n")
	b.WriteString("| 404 | `not_found` | Schedule or run ID does not exist |\n")
	b.WriteString("| 500 | `internal_error` | Unexpected server error |\n\n")
	b.WriteString("Go's default `404 method not allowed` and malformed transport-level responses do not use the JSON envelope.\n\n")

	b.WriteString("## Pagination and filtering\n\n")
	b.WriteString("List endpoints use cursor-based pagination with newest-first ordering (`created_at DESC, id DESC`).\n\n")
	b.WriteString("| Parameter | Applies to | Behavior |\n|---|---|---|\n")
	b.WriteString("| `limit` | schedule and run list endpoints | Default `50`. Invalid or non-positive values fall back to `50`. |\n")
	b.WriteString("| `cursor` | schedule and run list endpoints | Opaque cursor from a previous response. Invalid values return `400 bad_request`. |\n")
	b.WriteString("| `enabled=true|false` | `GET /v1/schedules` | Strict boolean filter. Any other value returns `400 bad_request`. |\n")
	b.WriteString("| `schedule_id=<id>` | `GET /v1/runs` | Filter runs to a single schedule. |\n")
	b.WriteString("| `status=<value>` | run list endpoints | Accepted values: `")
	b.WriteString(strings.Join(runStatuses(), "`, `"))
	b.WriteString("`. Invalid values return `400 bad_request`. |\n\n")

	b.WriteString("## Content types\n\n")
	b.WriteString("- Request bodies: `application/json`\n")
	b.WriteString("- JSON responses: `application/json`\n")
	b.WriteString("- Metrics response: `text/plain; version=0.0.4`\n\n")

	b.WriteString("## Status response shape\n\n```json\n{\n  \"service\": \"wakeplane\",\n  \"version\": \"")
	b.WriteString(version)
	b.WriteString("\",\n  \"started_at\": \"2026-03-25T12:00:00Z\",\n  \"database\": {\n    \"driver\": \"sqlite\",\n    \"path\": \"/var/lib/wakeplane/data.db\"\n  },\n  \"scheduler\": {\n    \"loop_interval_seconds\": 5,\n    \"last_tick_at\": \"2026-03-25T12:00:05Z\",\n    \"due_runs\": 0,\n    \"next_due_schedule_id\": \"sch_01...\",\n    \"next_due_run_at\": \"2026-03-25T12:05:00Z\"\n  },\n  \"workers\": {\n    \"active\": 0,\n    \"claimed_but_expired\": 0\n  },\n  \"runs\": {\n    \"running\": 0,\n    \"failed\": 0,\n    \"retry_queued\": 0,\n    \"dead_letter\": 0\n  }\n}\n```\n\n")

	b.WriteString("## Trigger response shape\n\n```json\n{\n  \"run_id\": \"run_01...\",\n  \"schedule_id\": \"sch_01...\",\n  \"occurrence_key\": \"manual:run_01...\",\n  \"status\": \"pending\",\n  \"created_at\": \"2026-03-25T12:00:00Z\"\n}\n```\n\n")

	b.WriteString("## Default policy values on create\n\n")
	b.WriteString("When schedule policy or retry fields are omitted, Wakeplane applies these defaults:\n\n```json\n{\n  \"policy\": {\n    \"overlap\": \"")
	b.WriteString(string(domain.DefaultPolicy().Overlap))
	b.WriteString("\",\n    \"misfire\": \"")
	b.WriteString(string(domain.DefaultPolicy().Misfire))
	b.WriteString("\",\n    \"timeout_seconds\": ")
	b.WriteString(fmt.Sprintf("%d", domain.DefaultPolicy().TimeoutSeconds))
	b.WriteString(",\n    \"max_concurrency\": ")
	b.WriteString(fmt.Sprintf("%d", domain.DefaultPolicy().MaxConcurrency))
	b.WriteString("\n  },\n  \"retry\": {\n    \"max_attempts\": ")
	b.WriteString(fmt.Sprintf("%d", domain.DefaultRetryPolicy().MaxAttempts))
	b.WriteString(",\n    \"strategy\": \"")
	b.WriteString(string(domain.DefaultRetryPolicy().Strategy))
	b.WriteString("\",\n    \"initial_delay_seconds\": ")
	b.WriteString(fmt.Sprintf("%d", domain.DefaultRetryPolicy().InitialDelaySeconds))
	b.WriteString(",\n    \"max_delay_seconds\": ")
	b.WriteString(fmt.Sprintf("%d", domain.DefaultRetryPolicy().MaxDelaySeconds))
	b.WriteString("\n  }\n}\n```\n")

	return b.String()
}

func commandHelp(version string, args ...string) (string, error) {
	root := cli.NewRootCmd(version)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)

	commandArgs := append([]string{}, args...)
	if len(commandArgs) == 0 {
		commandArgs = []string{"help"}
	} else {
		commandArgs = append(commandArgs, "--help")
	}
	root.SetArgs(commandArgs)
	if err := root.Execute(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func groupRoutes(routes []Route) (health []Route, operational []Route, schedules []Route, runs []Route) {
	for _, route := range routes {
		switch {
		case route.Path == "/healthz" || route.Path == "/readyz":
			health = append(health, route)
		case route.Path == "/v1/status" || route.Path == "/v1/metrics":
			operational = append(operational, route)
		case strings.HasPrefix(route.Path, "/v1/schedules"):
			schedules = append(schedules, route)
		case strings.HasPrefix(route.Path, "/v1/runs"):
			runs = append(runs, route)
		}
	}
	return health, operational, schedules, runs
}

func writeRouteTable(b *strings.Builder, title string, routes []Route) {
	b.WriteString("## ")
	b.WriteString(title)
	b.WriteString("\n\n| Method | Path | Description |\n|---|---|---|\n")
	for _, route := range routes {
		desc := route.Description
		if desc == "" {
			desc = "Document this route in `internal/docsgen/docsgen.go`."
		}
		b.WriteString("| `")
		b.WriteString(route.Method)
		b.WriteString("` | `")
		b.WriteString(route.Path)
		b.WriteString("` | ")
		b.WriteString(desc)
		b.WriteString(" |\n")
	}
	b.WriteString("\n")
}

func runStatuses() []string {
	statuses := []string{
		string(domain.RunPending),
		string(domain.RunClaimed),
		string(domain.RunRunning),
		string(domain.RunSucceeded),
		string(domain.RunFailed),
		string(domain.RunRetryScheduled),
		string(domain.RunDeadLettered),
		string(domain.RunCancelled),
		string(domain.RunSkipped),
	}
	sort.Strings(statuses)
	return statuses
}
