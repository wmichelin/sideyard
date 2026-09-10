package servicework

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOccurrenceDatesUseStableWeeklyCadenceAndBoundedWindow(t *testing.T) {
	schedule := Schedule{
		CadenceUnit: "week", CadenceInterval: 1,
		StartsOn: time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC),
	}
	dates := occurrenceDates(
		schedule,
		time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC),
	)
	want := []time.Time{
		time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 17, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC),
	}
	if len(dates) != len(want) {
		t.Fatalf("weekly dates = %v, want %v", dates, want)
	}
	for i := range want {
		if !dates[i].Equal(want[i]) {
			t.Fatalf("weekly date %d = %s, want %s", i, dates[i], want[i])
		}
	}
}

func TestProjectedOccurrenceCountMatchesMaterializationWindow(t *testing.T) {
	asOf := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	count, err := ProjectedOccurrenceCount(ScheduleInput{
		AgreementID: "agreement", ServiceName: "Daily route", CadenceUnit: "day",
		CadenceInterval: 1, StartsOn: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if count != lookBehindDays+lookAheadDays+1 {
		t.Fatalf("daily projection = %d, want %d", count, lookBehindDays+lookAheadDays+1)
	}
}

func TestOutcomeNotesAreBoundedBeforeDatabaseWork(t *testing.T) {
	service := &Service{}
	err := service.UpdateOccurrenceStatuses(context.Background(), "shop", []string{"occurrence"}, "completed", strings.Repeat("x", 2001))
	if err == nil || !strings.Contains(err.Error(), "2000 bytes") {
		t.Fatalf("oversized outcome notes error = %v", err)
	}
}

func TestMonthlyCadenceClampsToCalendarMonthWithoutDrifting(t *testing.T) {
	schedule := Schedule{
		CadenceUnit: "month", CadenceInterval: 1,
		StartsOn: time.Date(2028, time.January, 31, 0, 0, 0, 0, time.UTC),
	}
	dates := occurrenceDates(
		schedule,
		time.Date(2028, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2028, time.April, 30, 0, 0, 0, 0, time.UTC),
	)
	wantDays := []int{31, 29, 31, 30}
	if len(dates) != len(wantDays) {
		t.Fatalf("monthly dates = %v, want %d dates", dates, len(wantDays))
	}
	for i, wantDay := range wantDays {
		if dates[i].Day() != wantDay {
			t.Fatalf("monthly date %d day = %d, want %d (%s)", i, dates[i].Day(), wantDay, dates[i])
		}
	}
}

func TestOccurrenceDatesRespectScheduleEnd(t *testing.T) {
	endsOn := time.Date(2026, time.September, 14, 0, 0, 0, 0, time.UTC)
	schedule := Schedule{
		CadenceUnit: "week", CadenceInterval: 1,
		StartsOn: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
		EndsOn:   &endsOn,
	}
	dates := occurrenceDates(schedule, schedule.StartsOn, schedule.StartsOn.AddDate(0, 2, 0))
	if len(dates) != 2 || dates[0].Day() != 1 || dates[1].Day() != 8 {
		t.Fatalf("end-bounded dates = %v", dates)
	}
}

func TestValidateScheduleRejectsAmbiguousOrUnsafeInputs(t *testing.T) {
	valid := ScheduleInput{
		AgreementID: "agreement", ServiceName: "Weekly mowing", CadenceUnit: "week",
		CadenceInterval: 1, StartsOn: time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := ValidateSchedule(valid); err != nil {
		t.Fatalf("valid schedule rejected: %v", err)
	}
	for _, mutate := range []func(*ScheduleInput){
		func(input *ScheduleInput) { input.ServiceName = "" },
		func(input *ScheduleInput) { input.CadenceUnit = "year" },
		func(input *ScheduleInput) { input.CadenceInterval = 0 },
		func(input *ScheduleInput) {
			end := input.StartsOn.AddDate(0, 0, -1)
			input.EndsOn = &end
		},
	} {
		input := valid
		mutate(&input)
		if err := ValidateSchedule(input); err == nil {
			t.Fatalf("invalid schedule accepted: %+v", input)
		}
	}
}

func TestCadenceAcrossCenturiesAndBoundedDateExtremes(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, unit := range []string{"day", "week"} {
		schedule := Schedule{CadenceUnit: unit, CadenceInterval: 1, StartsOn: anchor}
		index := 1000000
		if unit == "week" {
			index = 300000
		}
		target := occurrenceAt(schedule, index)
		if got := firstOccurrenceIndex(schedule, target); got != index {
			t.Fatalf("%s index %d want %d", unit, got, index)
		}
		dates := occurrenceDates(schedule, target, target)
		if len(dates) != 1 || !dates[0].Equal(target) {
			t.Fatalf("%s far cadence lost: %v", unit, dates)
		}
	}
	for _, date := range []time.Time{time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)} {
		first, last := planningWindow(date)
		if !validDate(first) || !validDate(last) || date.Before(first) || date.After(last) {
			t.Fatal("planning overflow", first, last)
		}
	}
	for _, text := range []string{"x\x00y", string([]byte{0xff})} {
		if err := (&Service{}).UpdateOccurrenceStatuses(context.Background(), "shop", []string{"visit"}, "skipped", text); err == nil {
			t.Fatal("invalid notes accepted")
		}
	}
	// Validation follows the stored UTC day, including offset crossings at year 1.
	bad := time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 3600))
	if err := (&Service{}).MoveVisit(context.Background(), "shop", "visit", bad, 0); err != ErrDateOutsideSchedule {
		t.Fatal(err)
	}
}
