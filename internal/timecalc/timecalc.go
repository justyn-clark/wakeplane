package timecalc

import (
	"fmt"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
	cronlib "github.com/robfig/cron/v3"
)

var parser = cronlib.NewParser(cronlib.Minute | cronlib.Hour | cronlib.Dom | cronlib.Month | cronlib.Dow)

func NextAfter(schedule domain.Schedule, after time.Time) (*time.Time, error) {
	loc, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return nil, err
	}
	switch schedule.Schedule.Kind {
	case domain.ScheduleKindCron:
		spec, err := parser.Parse(schedule.Schedule.Expr)
		if err != nil {
			return nil, err
		}
		base := after.In(loc)
		if schedule.StartAt != nil && schedule.StartAt.After(after) {
			base = schedule.StartAt.Add(-time.Nanosecond).In(loc)
		}
		next := spec.Next(base)
		if next.IsZero() {
			return nil, nil
		}
		nextUTC := next.UTC()
		if schedule.EndAt != nil && nextUTC.After(schedule.EndAt.UTC()) {
			return nil, nil
		}
		return &nextUTC, nil
	case domain.ScheduleKindInterval:
		if schedule.Schedule.EverySeconds <= 0 || int64(schedule.Schedule.EverySeconds) > int64((1<<63-1)/int64(time.Second)) {
			return nil, fmt.Errorf("interval must have a positive supported duration")
		}
		anchor := schedule.CreatedAt.UTC()
		if schedule.Schedule.AnchorAt != nil {
			anchor = schedule.Schedule.AnchorAt.UTC()
		} else if schedule.StartAt != nil {
			anchor = schedule.StartAt.UTC()
		}
		base := after.UTC()
		if schedule.StartAt != nil && schedule.StartAt.After(after) {
			base = schedule.StartAt.Add(-time.Nanosecond).UTC()
		}
		if base.Before(anchor) {
			next := anchor
			return bound(schedule, next)
		}
		// Use exact seconds plus the anchor's nanoseconds. Duration subtraction
		// saturates for distant anchors, and float division rounds near slots.
		intervalSeconds := int64(schedule.Schedule.EverySeconds)
		elapsedSeconds := base.Unix() - anchor.Unix()
		if base.Nanosecond() < anchor.Nanosecond() {
			elapsedSeconds--
		}
		steps := elapsedSeconds/intervalSeconds + 1
		next := time.Unix(anchor.Unix()+steps*intervalSeconds, int64(anchor.Nanosecond())).UTC()
		if next.Year() > 9999 {
			return nil, nil // RFC3339 JSON timestamps cannot represent later slots.
		}
		return bound(schedule, next)
	case domain.ScheduleKindOnce:
		if schedule.Schedule.At == nil {
			return nil, fmt.Errorf("once schedule missing at")
		}
		next := schedule.Schedule.At.UTC()
		if !after.UTC().Before(next) {
			return nil, nil
		}
		return bound(schedule, next)
	default:
		return nil, fmt.Errorf("unknown schedule kind %q", schedule.Schedule.Kind)
	}
}

func bound(schedule domain.Schedule, next time.Time) (*time.Time, error) {
	next = next.UTC()
	if schedule.StartAt != nil && next.Before(schedule.StartAt.UTC()) {
		return nil, nil
	}
	if schedule.EndAt != nil && next.After(schedule.EndAt.UTC()) {
		return nil, nil
	}
	return &next, nil
}
