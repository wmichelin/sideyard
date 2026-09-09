package app

import (
	"errors"
	"sort"
	"time"
	_ "time/tzdata" // The scratch image has no operating-system zone database.
)

type eventTime struct {
	Date time.Time
	At   *time.Time
	Zone string
}

type eventTimeChoice struct{ Offset, Label string }
type ambiguousEventTime struct{ Choices []eventTimeChoice }

func (*ambiguousEventTime) Error() string {
	return "Clocks repeat this time on that date. Choose which occurrence you mean below."
}

func eventOffset(t time.Time) string {
	_, seconds := t.Zone()
	if seconds%60 != 0 {
		return t.Format("-07:00:00")
	}
	return t.Format("-07:00")
}

// A date-only observation has no invented midnight timestamp. When a local time
// is supplied, resolve it against real zone offsets and reject gaps/fold guesses.
func parseEventTime(date, clock, zone, offset string, now time.Time) (eventTime, error) {
	var result eventTime
	if zone == "" || zone == "Local" || len(zone) > 100 {
		return result, errors.New("Enter a timezone such as America/New_York or UTC.")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return result, errors.New("Enter a valid timezone such as America/New_York or UTC.")
	}
	day, err := time.Parse("2006-01-02", date)
	if err != nil || day.Year() < 1 || day.Format("2006-01-02") != date || date > now.In(loc).Format("2006-01-02") {
		return result, errors.New("Choose a valid event date no later than today in its timezone.")
	}
	result = eventTime{Date: day, Zone: zone}
	if clock == "" {
		return result, nil
	}
	wall, err := time.Parse("2006-01-02T15:04", date+"T"+clock)
	if err != nil || wall.Format("15:04") != clock {
		return result, errors.New("Enter an event time as hours and minutes, or leave it blank.")
	}
	// Sampling either side of the local day collects standard/daylight offsets,
	// including half-hour transitions and date-line changes. Verify each candidate
	// against the original wall time instead of trusting time.Date's fold choice.
	anchor := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), 0, 0, loc)
	offsets := map[int]bool{}
	for hours := -36; hours <= 36; hours += 6 {
		_, seconds := anchor.Add(time.Duration(hours) * time.Hour).Zone()
		offsets[seconds] = true
	}
	var candidates []time.Time
	for seconds := range offsets {
		candidate := wall.Add(-time.Duration(seconds) * time.Second).In(loc)
		if candidate.Format("2006-01-02T15:04") == date+"T"+clock {
			candidates = append(candidates, candidate)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Before(candidates[j]) })
	if len(candidates) == 0 {
		return result, errors.New("This local time does not exist because clocks changed. Choose a valid time or leave the time blank.")
	}
	if len(candidates) > 1 && offset == "" {
		choices := &ambiguousEventTime{}
		for _, candidate := range candidates {
			choices.Choices = append(choices.Choices, eventTimeChoice{Offset: eventOffset(candidate), Label: candidate.Format("15:04 MST") + " (UTC" + eventOffset(candidate) + ")"})
		}
		return result, choices
	}
	for _, candidate := range candidates {
		if offset != "" && offset != eventOffset(candidate) {
			continue
		}
		if candidate.After(now) {
			return result, errors.New("The event time cannot be in the future.")
		}
		utc := candidate.UTC()
		result.At = &utc
		return result, nil
	}
	return result, errors.New("That offset does not match this event time. Clear the offset or choose the correct occurrence.")
}
