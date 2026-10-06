package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// TriggerEvent is an external signal, not an execution target. Source and ID
// identify one logical event for this schedule across delivery retries.
type TriggerEvent struct {
	ID     string         `json:"id"`
	Source string         `json:"source"`
	Data   map[string]any `json:"data,omitempty"`
}

func ValidateTriggerEvent(event TriggerEvent) error {
	for name, value := range map[string]string{"id": event.ID, "source": event.Source} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return fmt.Errorf("event %s must be non-empty and at most 256 bytes", name)
		}
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return fmt.Errorf("event %s must not contain control characters", name)
		}
	}
	data, err := json.Marshal(event.Data)
	if err != nil {
		return fmt.Errorf("event data must be JSON: %w", err)
	}
	if len(data) > 65536 {
		return fmt.Errorf("event data must be at most 65536 bytes")
	}
	return nil
}
