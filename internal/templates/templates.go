// Package templates contains operator recipes, kept outside the core domain.
package templates

import "github.com/justyn-clark/wakeplane/internal/domain"

type Template struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	Description string                       `json:"description"`
	Request     domain.CreateScheduleRequest `json:"request"`
}

func List() []Template {
	base := func(name, cron, method string) domain.CreateScheduleRequest {
		return domain.CreateScheduleRequest{Name: name, Enabled: false, Timezone: "UTC", Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindCron, Expr: cron}, Target: domain.TargetSpec{Kind: domain.TargetKindHTTP, Method: method}, Policy: domain.DefaultPolicy(), Retry: domain.DefaultRetryPolicy()}
	}
	developer := base("Repository watch", "0 9 * * 1-5", "POST")
	developer.Target.Body = map[string]any{"task": "repository-watch", "repository": "", "notify_url": ""}
	developer.Target.URL = "http://127.0.0.1:8091/jobs"
	developer.Policy.TimeoutSeconds = 120
	developer.Target.HTTPJob = &domain.HTTPJobSpec{PollIntervalSeconds: 5, LookupURL: "http://127.0.0.1:8091/jobs/lookup"}
	weekly := base("Weekly reading summary", "0 9 * * 1", "POST")
	weekly.Target.Body = map[string]any{"task": "weekly-summary", "feeds": []string{}, "notify_url": ""}
	weekly.Target.URL = "http://127.0.0.1:8091/jobs"
	weekly.Policy.TimeoutSeconds = 180
	weekly.Target.HTTPJob = &domain.HTTPJobSpec{PollIntervalSeconds: 5, LookupURL: "http://127.0.0.1:8091/jobs/lookup"}
	check := base("Website check", "*/15 * * * *", "GET")
	backup := base("Nightly backup", "0 2 * * *", "GET")
	backup.Target = domain.TargetSpec{Kind: domain.TargetKindShell}
	return []Template{
		{ID: "developer-maintenance", Name: "Repository watch", Description: "Check repository activity and receive a report from your configured runner.", Request: developer},
		{ID: "weekly-summary", Name: "Weekly reading summary", Description: "Collect recent articles from your feeds and receive a weekly reading list.", Request: weekly},
		{ID: "http-check", Name: "Website check", Description: "Check an HTTP endpoint and keep a history of its availability.", Request: check},
		{ID: "shell-backup", Name: "Nightly backup", Description: "Run your own backup program. Choose the command and destination before enabling.", Request: backup},
	}
}
