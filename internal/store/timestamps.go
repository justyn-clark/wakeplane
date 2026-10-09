package store

import (
	"fmt"
	"time"
)

// timestampSQL produces a sortable UTC timestamp without changing stored data.
// Legacy RFC3339Nano strings trim fractional zeros and therefore do not sort
// chronologically as TEXT. Both supported SQL dialects provide these functions.
// The column is always a source-owned SQL identifier or expression, never input.
// Normalizing at read time preserves backups, receipt/audit bytes, and cursor
// compatibility. Raw timestamp indexes cannot directly satisfy these expressions.
func timestampSQL(column string) string {
	return fmt.Sprintf("(substr(%[1]s, 1, 19) || '.' || substr(CASE WHEN substr(%[1]s, 20, 1) = '.' THEN substr(%[1]s, 21, length(%[1]s) - 21) ELSE '' END || '000000000', 1, 9) || 'Z')", column)
}

func comparisonTimeString(instant time.Time) string {
	return instant.UTC().Format("2006-01-02T15:04:05.000000000Z")
}
