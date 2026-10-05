package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/justyn-clark/wakeplane/internal/domain"
)

func TestSQLiteEventDeliveryInvariants(t *testing.T) { testEventDelivery(t, openTestStore(t)) }
func TestPostgresEventDeliveryInvariants(t *testing.T) {
	testEventDelivery(t, openPostgresTestStore(t))
}

func testEventDelivery(t *testing.T, st *Store) {
	ctx := context.Background()
	schedule := insertTestSchedule(t, st)
	now := time.Now().UTC()
	event := domain.TriggerEvent{ID: "delivery-1", Source: "github", Data: map[string]any{"action": "opened"}}
	var created atomic.Int64
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, fresh, err := st.TriggerEvent(ctx, schedule.ID, event, now)
			if err != nil {
				t.Errorf("TriggerEvent: %v", err)
				return
			}
			if fresh {
				created.Add(1)
			}
			ids <- run.ID
		}()
	}
	wg.Wait()
	close(ids)
	if created.Load() != 1 {
		t.Fatalf("created %d runs for one event", created.Load())
	}
	var runID string
	for id := range ids {
		if runID != "" && runID != id {
			t.Fatalf("replayed event returned a different run")
		}
		runID = id
	}
	run, err := st.GetRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Event == nil || run.Event.ID != event.ID {
		t.Fatalf("event did not survive durable storage: %+v", run.Event)
	}
	changed := event
	changed.Data = map[string]any{"action": "closed"}
	if _, _, err := st.TriggerEvent(ctx, schedule.ID, changed, now); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	schedule.Enabled = false
	if err := st.UpdateSchedule(ctx, schedule); err != nil {
		t.Fatal(err)
	}
	if got, fresh, err := st.TriggerEvent(ctx, schedule.ID, event, now); err != nil || fresh || got.ID != runID {
		t.Fatalf("existing replay after pause: %v", err)
	}
	newEvent := domain.TriggerEvent{ID: "delivery-2", Source: "github"}
	if _, _, err := st.TriggerEvent(ctx, schedule.ID, newEvent, now); !errors.Is(err, ErrSchedulePaused) {
		t.Fatalf("paused new event: %v", err)
	}
	run.Status = domain.RunSucceeded
	finished := now.Add(-48 * time.Hour)
	run.FinishedAt = &finished
	if err := st.FinishRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PruneTerminalRunsBefore(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.TriggerEvent(ctx, schedule.ID, event, now); !errors.Is(err, ErrEventHistoryPruned) {
		t.Fatalf("retention allowed redelivery: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("additive migrations cannot repeat: %v", err)
	}
}
