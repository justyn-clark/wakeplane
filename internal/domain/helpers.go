package domain

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

func NewID(prefix string) string {
	return fmt.Sprintf("%s_%s", prefix, ulid.MustNew(ulid.Timestamp(time.Now().UTC()), ulid.Monotonic(rand.Reader, 0)).String())
}

func OccurrenceKey(scheduleID string, nominal time.Time) string {
	return fmt.Sprintf("%s:%s", scheduleID, nominal.UTC().Format(time.RFC3339))
}

func EncodeCursor(createdAt time.Time, id string) string {
	payload, _ := json.Marshal(map[string]string{
		"created_at": createdAt.UTC().Format(time.RFC3339Nano),
		"id":         id,
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	payload := map[string]string{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return time.Time{}, "", err
	}
	t, err := time.Parse(time.RFC3339Nano, payload["created_at"])
	if err != nil {
		return time.Time{}, "", err
	}
	return t.UTC(), payload["id"], nil
}

func MustJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func IsActiveStatus(status RunStatus) bool {
	return status == RunClaimed || status == RunRunning
}
