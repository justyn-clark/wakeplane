package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

var (
	ErrEventConflict      = errors.New("event data conflict")
	ErrSchedulePaused     = errors.New("schedule is paused")
	ErrEventHistoryPruned = errors.New("event history pruned")
)

// TriggerEvent atomically records an event and its run. Deduplication records
// survive run retention so an old delivery cannot silently execute again.
func (s *Store) TriggerEvent(ctx context.Context, scheduleID string, event domain.TriggerEvent, now time.Time) (domain.Run, bool, error) {
	keyData, _ := json.Marshal([]string{scheduleID, event.Source, event.ID})
	digest := sha256.Sum256(keyData)
	key := "event:" + hex.EncodeToString(digest[:])
	payload, err := json.Marshal(event)
	if err != nil {
		return domain.Run{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Run{}, false, err
	}
	defer tx.Rollback()
	var enabled int
	if err := s.txQueryRow(ctx, tx, `SELECT enabled FROM schedules WHERE id = ?`, scheduleID).Scan(&enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Run{}, false, ErrNotFound
		}
		return domain.Run{}, false, err
	}
	runID := domain.NewID("run")
	result, err := s.txExec(ctx, tx, `INSERT INTO trigger_events (occurrence_key, schedule_id, event_json, run_id, created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT (occurrence_key) DO NOTHING`, key, scheduleID, string(payload), runID, timeString(now))
	if err != nil {
		return domain.Run{}, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domain.Run{}, false, err
	}
	if inserted == 0 {
		var priorPayload string
		if err := s.txQueryRow(ctx, tx, `SELECT event_json, run_id FROM trigger_events WHERE occurrence_key = ?`, key).Scan(&priorPayload, &runID); err != nil {
			return domain.Run{}, false, err
		}
		if priorPayload != string(payload) {
			return domain.Run{}, false, ErrEventConflict
		}
		if err := tx.Commit(); err != nil {
			return domain.Run{}, false, err
		}
		run, err := s.GetRun(ctx, runID)
		if errors.Is(err, ErrNotFound) {
			return domain.Run{}, false, ErrEventHistoryPruned
		}
		return run, false, err
	}
	if enabled == 0 {
		return domain.Run{}, false, ErrSchedulePaused
	}
	run := domain.Run{ID: runID, ScheduleID: scheduleID, OccurrenceKey: key, NominalTime: now, DueTime: now, Status: domain.RunPending, Attempt: 1, CreatedAt: now, UpdatedAt: now, Event: &event}
	_, err = s.txExec(ctx, tx, `INSERT INTO schedule_runs (id, schedule_id, occurrence_key, nominal_time, due_time, status, attempt, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, run.ID, scheduleID, key, timeString(now), timeString(now), run.Status, 1, timeString(now), timeString(now))
	if err != nil {
		return domain.Run{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Run{}, false, err
	}
	return run, true, nil
}

func (s *Store) GetRunEvent(ctx context.Context, occurrenceKey string) (*domain.TriggerEvent, error) {
	var payload string
	err := s.queryRow(ctx, `SELECT event_json FROM trigger_events WHERE occurrence_key = ?`, occurrenceKey).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var event domain.TriggerEvent
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&event); err != nil {
		return nil, err
	}
	return &event, nil
}
