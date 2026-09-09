package app

import (
	"errors"
	"testing"
	"time"
)

func TestEventTimePrecisionAndTimezoneBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 9, 0, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, date, clock, zone, offset, want string
		valid                                 bool
	}{
		{"date only", "2026-09-08", "", "America/New_York", "", "", true},
		{"local evening", "2026-09-08", "20:00", "America/New_York", "", "2026-09-09T00:00:00Z", true},
		{"future local date", "2026-09-09", "", "America/New_York", "", "", false},
		{"future instant", "2026-09-08", "21:00", "America/New_York", "", "", false},
		{"spring gap", "2025-03-09", "02:30", "America/New_York", "", "", false},
		{"fold earlier", "2025-11-02", "01:30", "America/New_York", "-04:00", "2025-11-02T05:30:00Z", true},
		{"fold later", "2025-11-02", "01:30", "America/New_York", "-05:00", "2025-11-02T06:30:00Z", true},
		{"fold wrong offset", "2025-11-02", "01:30", "America/New_York", "-06:00", "", false},
		{"half hour fold earlier", "2025-04-06", "01:45", "Australia/Lord_Howe", "+11:00", "2025-04-05T14:45:00Z", true},
		{"half hour fold later", "2025-04-06", "01:45", "Australia/Lord_Howe", "+10:30", "2025-04-05T15:15:00Z", true},
		{"half hour gap", "2025-10-05", "02:15", "Australia/Lord_Howe", "", "", false},
		{"skipped date", "2011-12-30", "12:00", "Pacific/Apia", "", "", false},
		{"unknown zone", "2025-01-01", "12:00", "Wrong/Zone", "", "", false},
		{"host zone forbidden", "2025-01-01", "12:00", "Local", "", "", false},
		{"invalid date", "2025-02-29", "12:00", "UTC", "", "", false},
		{"year zero", "0000-01-01", "", "UTC", "", "", false},
		{"invalid time", "2025-01-01", "25:30", "UTC", "", "", false},
		{"UTC", "2025-01-01", "12:00", "UTC", "", "2025-01-01T12:00:00Z", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseEventTime(tc.date, tc.clock, tc.zone, tc.offset, now)
			if (err == nil) != tc.valid {
				t.Fatalf("event time: %+v %v", got, err)
			}
			if !tc.valid {
				return
			}
			if tc.want == "" {
				if got.At != nil {
					t.Fatal("date-only observation acquired an invented time")
				}
			} else if got.At == nil || got.At.Format(time.RFC3339) != tc.want {
				t.Fatalf("instant: %+v want %s", got, tc.want)
			}
		})
	}
	_, err := parseEventTime("2025-11-02", "01:30", "America/New_York", "", now)
	var ambiguous *ambiguousEventTime
	if !errors.As(err, &ambiguous) || len(ambiguous.Choices) != 2 || ambiguous.Choices[0].Offset != "-04:00" || ambiguous.Choices[1].Offset != "-05:00" {
		t.Fatalf("repeated time did not offer both choices: %+v %v", ambiguous, err)
	}
}
