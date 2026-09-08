package app

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/wmichelin/clubdue/internal/servicework"
)

type calendarDay struct {
	Date, Label, URL               string
	Number, Count                  int
	Today, Selected, BeyondHorizon bool
}

type dashboardCalendar struct {
	Month, SelectedDay, End                                                    time.Time
	MonthLabel, DayLabel, DayISO, PreviousURL, NextURL, TodayURL, HorizonLabel string
	PreviousPageURL, NextPageURL                                               string
	Days                                                                       []calendarDay
	Visits                                                                     []servicework.CalendarVisit
	Total, MonthTotal, First, Last                                             int
	Overdue, BeyondHorizon                                                     bool
}

func calendarDate(raw string) (time.Time, bool) {
	date, err := time.Parse("2006-01-02", raw)
	return date, err == nil && date.Year() >= 1 && date.Year() <= 9999
}

func calendarURL(day time.Time, page int) string {
	values := url.Values{"month": {day.Format("2006-01")}, "day": {day.Format("2006-01-02")}}
	if page > 1 {
		values.Set("calendar_page", strconv.Itoa(page))
	}
	return "/dashboard?" + values.Encode()
}

func calendarSelection(now time.Time, query url.Values) dashboardCalendar {
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	selected, ok := calendarDate(query.Get("day"))
	if !ok {
		selected = today
	}
	month, err := time.Parse("2006-01", query.Get("month"))
	if err != nil || month.Year() < 1 || month.Year() > 9999 {
		month = time.Date(selected.Year(), selected.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	if selected.Year() != month.Year() || selected.Month() != month.Month() {
		selected = month
	}
	end := month.AddDate(0, 1, 0)
	horizon := today.AddDate(0, 0, 90)
	result := dashboardCalendar{Month: month, End: end, SelectedDay: selected, MonthLabel: month.Format("January 2006"),
		DayLabel: selected.Format("Monday, Jan 2, 2006"), DayISO: selected.Format("2006-01-02"), TodayURL: calendarURL(today, 1) + "#service-calendar",
		HorizonLabel: horizon.Format("Jan 2, 2006"), Overdue: selected.Before(today), BeyondHorizon: selected.After(horizon)}
	previous, next := month.AddDate(0, -1, 0), end
	if previous.Year() >= 1 {
		result.PreviousURL = calendarURL(previous, 1) + "#service-calendar"
	}
	if next.Year() <= 9999 {
		result.NextURL = calendarURL(next, 1) + "#service-calendar"
	}
	// Monday-first grid. Empty padding cells never imply planned work.
	padding := (int(month.Weekday()) + 6) % 7
	for i := 0; i < padding; i++ {
		result.Days = append(result.Days, calendarDay{})
	}
	for day := month; day.Before(end); day = day.AddDate(0, 0, 1) {
		result.Days = append(result.Days, calendarDay{Date: day.Format("2006-01-02"), Label: day.Format("Monday, January 2, 2006"), URL: calendarURL(day, 1) + "#calendar-agenda", Number: day.Day(), Today: day.Equal(today), Selected: day.Equal(selected), BeyondHorizon: day.After(horizon)})
	}
	for len(result.Days)%7 != 0 {
		result.Days = append(result.Days, calendarDay{})
	}
	return result
}

func (view *dashboardCalendar) populate(data servicework.Calendar) {
	for i := range view.Days {
		day := &view.Days[i]
		if day.Date == "" {
			continue
		}
		day.Count = data.Counts[day.Date]
		view.MonthTotal += day.Count
		day.Label += fmt.Sprintf(": %d job", day.Count)
		if day.Count != 1 {
			day.Label += "s"
		}
		if day.BeyondHorizon {
			day.Label += "; planning incomplete"
		}
	}
	view.Visits, view.Total = data.Visits, data.Total
	if data.Total > 0 {
		view.First = (data.Page-1)*servicework.CalendarPageSize + 1
		view.Last = view.First + len(data.Visits) - 1
	}
	if data.Page > 1 {
		view.PreviousPageURL = calendarURL(view.SelectedDay, data.Page-1) + "#calendar-agenda"
	}
	if view.Last < data.Total {
		view.NextPageURL = calendarURL(view.SelectedDay, data.Page+1) + "#calendar-agenda"
	}
}
