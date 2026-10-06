package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/justyn-clark/wakeplane/internal/store"
)

func (s *Service) TriggerEvent(ctx context.Context, scheduleID string, event domain.TriggerEvent) (domain.Run, bool, error) {
	event.ID = strings.TrimSpace(event.ID)
	event.Source = strings.TrimSpace(event.Source)
	if err := domain.ValidateTriggerEvent(event); err != nil {
		return domain.Run{}, false, domain.NewBadRequestError(err.Error())
	}
	run, created, err := s.store.TriggerEvent(ctx, scheduleID, event, time.Now().UTC())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return domain.Run{}, false, domain.NewNotFoundError("schedule", scheduleID)
	case errors.Is(err, store.ErrEventConflict):
		return domain.Run{}, false, domain.NewConflictError("event ID was already used with different data")
	case errors.Is(err, store.ErrSchedulePaused):
		return domain.Run{}, false, domain.NewConflictError("schedule is paused; resume before delivering events")
	case errors.Is(err, store.ErrEventHistoryPruned):
		return domain.Run{}, false, &domain.APIError{Status: 410, Code: "history_pruned", Message: "event already processed; its run history has been pruned"}
	default:
		return run, created, err
	}
}
