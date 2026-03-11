package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/justyn-clark/timekeeper/internal/planner"
)

type Loop struct {
	planner  *planner.Planner
	logger   *slog.Logger
	interval time.Duration
}

func New(pl *planner.Planner, logger *slog.Logger, interval time.Duration) *Loop {
	return &Loop{planner: pl, logger: logger, interval: interval}
}

func (l *Loop) Run(ctx context.Context) error {
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	for {
		if err := l.planner.Tick(ctx); err != nil {
			l.logger.Error("planner loop tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
