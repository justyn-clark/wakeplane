package timecalc

import (
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestNextAfterCronInTimezone(t *testing.T) {
	createdAt := time.Date(2026, 3, 11, 20, 0, 0, 0, time.UTC)
	schedule := domain.Schedule{
		Timezone:  "America/Los_Angeles",
		CreatedAt: createdAt,
		Schedule: domain.ScheduleSpec{
			Kind: domain.ScheduleKindCron,
			Expr: "0 2 * * *",
		},
	}
	after := time.Date(2026, 3, 11, 9, 0, 0, 0, time.UTC)
	next, err := NextAfter(schedule, after)
	if err != nil {
		t.Fatalf("NextAfter returned error: %v", err)
	}
	if next == nil {
		t.Fatal("expected next run")
	}
	expected := time.Date(2026, 3, 12, 9, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected %s, got %s", expected, next.UTC())
	}
}

func TestNextAfterInterval(t *testing.T) {
	anchor := time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC)
	schedule := domain.Schedule{
		Timezone:  "UTC",
		CreatedAt: anchor,
		Schedule: domain.ScheduleSpec{
			Kind:         domain.ScheduleKindInterval,
			EverySeconds: 3600,
			AnchorAt:     &anchor,
		},
	}
	after := anchor.Add(90 * time.Minute)
	next, err := NextAfter(schedule, after)
	if err != nil {
		t.Fatalf("NextAfter returned error: %v", err)
	}
	expected := anchor.Add(2 * time.Hour)
	if next == nil || !next.Equal(expected) {
		t.Fatalf("expected %s, got %v", expected, next)
	}
}
