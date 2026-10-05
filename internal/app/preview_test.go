package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestPreviewUsesTimezoneAndWindowWithoutWriting(t *testing.T) {
	ctx := context.Background()
	s, err := New(ctx, config.Config{DatabasePath: filepath.Join(t.TempDir(), "preview.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	start := time.Now().UTC().Add(7 * 24 * time.Hour)
	end := start.Add(3 * 24 * time.Hour)
	req := domain.CreateScheduleRequest{Name: "preview", Timezone: "America/Los_Angeles", Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindCron, Expr: "0 9 * * *"}, Target: domain.TargetSpec{Kind: domain.TargetKindShell, Command: "echo"}, StartAt: &start, EndAt: &end}
	runs, errs, err := s.PreviewSchedule(ctx, req)
	if err != nil || len(errs) > 0 {
		t.Fatalf("preview: %v %+v", err, errs)
	}
	if len(runs) != 3 {
		t.Fatalf("expected 3 slots within window, got %v", runs)
	}
	loc, _ := time.LoadLocation(req.Timezone)
	for _, run := range runs {
		if run.Before(start) || run.After(end) || run.In(loc).Hour() != 9 {
			t.Fatalf("out-of-window or wrong timezone slot: %s", run)
		}
	}
	items, _, err := s.ListSchedules(ctx, nil, 50, "")
	if err != nil || len(items) != 0 {
		t.Fatal("preview wrote a schedule")
	}
	req.Timezone = ""
	if _, errs, _ := s.PreviewSchedule(ctx, req); len(errs) == 0 {
		t.Fatal("empty timezone accepted")
	}
	req.Timezone = "UTC"
	at := time.Now().UTC().Add(time.Hour)
	req.Schedule = domain.ScheduleSpec{Kind: domain.ScheduleKindOnce, At: &at}
	req.StartAt, req.EndAt = nil, nil
	runs, errs, err = s.PreviewSchedule(ctx, req)
	if err != nil || len(errs) > 0 || len(runs) != 1 || !runs[0].Equal(at) {
		t.Fatalf("once preview: %v %+v %v", runs, errs, err)
	}
}
