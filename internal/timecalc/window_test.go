package timecalc

import (
	"github.com/justyn-clark/wakeplane/internal/domain"
	"testing"
	"time"
)

func TestCronRespectsStartWindowAndImpossibleDate(t *testing.T) {
	after := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	start := after.Add(48*time.Hour + 30*time.Minute)
	s := domain.Schedule{Timezone: "UTC", Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindCron, Expr: "0 * * * *"}, StartAt: &start}
	next, err := NextAfter(s, after)
	if err != nil || next == nil || !next.Equal(after.Add(49*time.Hour)) {
		t.Fatalf("cron start bound: %v %v", next, err)
	}
	s.Schedule.Expr = "0 0 30 2 *"
	if next, err := NextAfter(s, after); err != nil || next != nil {
		t.Fatalf("impossible cron produced phantom occurrence: %v %v", next, err)
	}
}

func TestIntervalWindowPreservesCadenceAndExactSubsecondBoundaries(t *testing.T) {
	anchor := time.Date(2026, 10, 5, 0, 0, 0, 123456789, time.UTC)
	start := anchor.Add(30 * time.Minute)
	schedule := domain.Schedule{Timezone: "UTC", Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindInterval, EverySeconds: 3600, AnchorAt: &anchor}, StartAt: &start}
	if next, err := NextAfter(schedule, anchor.Add(-time.Hour)); err != nil || next == nil || !next.Equal(anchor.Add(time.Hour)) {
		t.Fatalf("start window introduced off-cadence run: %v %v", next, err)
	}
	// The first cadence slot exactly on StartAt is eligible, including when
	// an old anchor makes float-based interval arithmetic round nanoseconds.
	start = anchor.Add(365 * 24 * time.Hour)
	if next, err := NextAfter(schedule, anchor.Add(-time.Hour)); err != nil || next == nil || !next.Equal(start) {
		t.Fatalf("exact cadence StartAt skipped: %v %v", next, err)
	}
	schedule.StartAt = nil
	if next, err := NextAfter(schedule, start.Add(-time.Nanosecond)); err != nil || next == nil || !next.Equal(start) {
		t.Fatalf("nanosecond boundary skipped: %v %v", next, err)
	}
}

func TestOnceOutsideActiveWindowHasNoOccurrence(t *testing.T) {
	at := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	start := at.Add(time.Hour)
	schedule := domain.Schedule{Timezone: "UTC", Schedule: domain.ScheduleSpec{Kind: domain.ScheduleKindOnce, At: &at}, StartAt: &start}
	if next, err := NextAfter(schedule, at.Add(-time.Hour)); err != nil || next != nil {
		t.Fatalf("one-time occurrence moved to StartAt: %v %v", next, err)
	}
	start = at
	if next, err := NextAfter(schedule, at.Add(-time.Hour)); err != nil || next == nil || !next.Equal(at) {
		t.Fatalf("one-time occurrence exactly on StartAt excluded: %v %v", next, err)
	}
}
