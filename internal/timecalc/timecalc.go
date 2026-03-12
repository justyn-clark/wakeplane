package timecalc

import (
	"fmt"
	"math"
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
		next := spec.Next(base)
		nextUTC := next.UTC()
		if schedule.EndAt != nil && nextUTC.After(schedule.EndAt.UTC()) {
			return nil, nil
		}
		return &nextUTC, nil
	case domain.ScheduleKindInterval:
		anchor := schedule.CreatedAt.UTC()
		if schedule.Schedule.AnchorAt != nil {
			anchor = schedule.Schedule.AnchorAt.UTC()
		} else if schedule.StartAt != nil {
			anchor = schedule.StartAt.UTC()
		}
		if after.UTC().Before(anchor) {
			next := anchor
			return bound(schedule, next)
		}
		interval := time.Duration(schedule.Schedule.EverySeconds) * time.Second
		elapsed := after.UTC().Sub(anchor)
		steps := int(math.Floor(float64(elapsed)/float64(interval))) + 1
		next := anchor.Add(time.Duration(steps) * interval)
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
		return &[]time.Time{schedule.StartAt.UTC()}[0], nil
	}
	if schedule.EndAt != nil && next.After(schedule.EndAt.UTC()) {
		return nil, nil
	}
	return &next, nil
}
