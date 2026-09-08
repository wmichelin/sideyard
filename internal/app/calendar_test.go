package app

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCalendarMonthLayoutAndNavigation(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		month                string
		days, padding, cells int
		next                 string
	}{
		{"2026-02", 28, 6, 35, "2026-03"},
		{"2028-02", 29, 1, 35, "2028-03"},
		{"2026-04", 30, 2, 35, "2026-05"},
		{"2026-08", 31, 5, 42, "2026-09"},
		{"2026-06", 30, 0, 35, "2026-07"},
		{"2026-12", 31, 1, 35, "2027-01"},
	} {
		t.Run(tc.month, func(t *testing.T) {
			view := calendarSelection(now, url.Values{"month": {tc.month}})
			if len(view.Days) != tc.cells {
				t.Fatalf("cells=%d want%d", len(view.Days), tc.cells)
			}
			count := 0
			for i, day := range view.Days {
				if day.Date == "" {
					continue
				}
				count++
				if day.Number != count || i != tc.padding+count-1 {
					t.Fatalf("misaligned/duplicate day %+v index%d", day, i)
				}
			}
			if count != tc.days || view.DayISO != tc.month+"-01" || !strings.Contains(view.NextURL, "month="+tc.next) {
				t.Fatalf("month navigation: %+v", view)
			}
		})
	}
}

func TestCalendarSelectionUsesUTCAndRejectsInvalidDates(t *testing.T) {
	now := time.Date(2026, 9, 7, 23, 30, 0, 0, time.FixedZone("west", -4*3600))
	for _, tc := range []struct {
		name  string
		query url.Values
		want  string
	}{
		{"default", url.Values{}, "2026-09-08"},
		{"selected", url.Values{"day": {"2026-09-14"}}, "2026-09-14"},
		{"different month", url.Values{"month": {"2027-01"}, "day": {"2026-09-14"}}, "2027-01-01"},
		{"invalid", url.Values{"month": {"not-a-month"}, "day": {"2026-02-30"}}, "2026-09-08"},
		{"zero year", url.Values{"month": {"0000-01"}, "day": {"0000-01-01"}}, "2026-09-08"},
		{"oversized year", url.Values{"month": {"10000-01"}, "day": {"10000-01-01"}}, "2026-09-08"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := calendarSelection(now, tc.query)
			if view.DayISO != tc.want {
				t.Fatalf("selected %s want%s", view.DayISO, tc.want)
			}
		})
	}
	view := calendarSelection(now, url.Values{"month": {"2027-01"}})
	if !view.BeyondHorizon {
		t.Fatal("future coverage claimed complete")
	}
	for _, day := range view.Days {
		if day.Date != "" && !day.BeyondHorizon {
			t.Fatal("future cell not marked incomplete")
		}
	}
	last := calendarSelection(now, url.Values{"month": {"9999-12"}, "day": {"9999-12-31"}})
	if last.NextURL != "" || last.DayISO != "9999-12-31" {
		t.Fatal("last supported year reset or overflowed")
	}
	first := calendarSelection(now, url.Values{"month": {"0001-01"}})
	if first.PreviousURL != "" {
		t.Fatal("first supported year overflowed")
	}
}
