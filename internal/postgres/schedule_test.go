package postgres_test

import (
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
)

func TestParseNextOccurrence(t *testing.T) {
	repo := postgres.NewScheduleRepo(nil)

	from := time.Date(2026, 10, 16, 8, 30, 0, 0, time.UTC)

	// Daily at 9:00 AM UTC
	next, err := repo.ParseNextOccurrence("0 9 * * *", "UTC", from)
	if err != nil {
		t.Fatalf("Unexpected error parsing valid cron: %v", err)
	}

	expected := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("Expected next run at %v, got %v", expected, next)
	}

	// Invalid timezone should fail
	_, err = repo.ParseNextOccurrence("0 9 * * *", "Invalid/Timezone_Name", from)
	if err == nil {
		t.Errorf("Expected error for invalid timezone, got nil")
	}

	// 6-field cron with seconds should fail in standard 5-field parser
	_, err = repo.ParseNextOccurrence("*/5 * * * * *", "UTC", from)
	if err == nil {
		t.Errorf("Expected error for 6-field cron expression, got nil")
	}

	// Malformed cron syntax
	_, err = repo.ParseNextOccurrence("not-a-cron-expression", "UTC", from)
	if err == nil {
		t.Errorf("Expected error for malformed cron, got nil")
	}
}
