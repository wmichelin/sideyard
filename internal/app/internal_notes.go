package app

import (
	"errors"
	"strings"
	"time"
)

type internalNoteInput struct{ Key, Body, Date, Time, Zone, Offset string }
type historyTarget struct{ ID, Name, CustomerID, BackURL string }
type internalNote struct {
	Body, RecordedBy string
	Event            eventTime
	RecordedAt       time.Time
}

func (n internalNote) EventLabel() string {
	if n.Event.At == nil {
		return n.Event.Date.Format("Jan 2, 2006") + " · Date only · " + n.Event.Zone
	}
	loc, err := time.LoadLocation(n.Event.Zone)
	if err != nil {
		return n.Event.At.UTC().Format(time.RFC3339)
	}
	at := n.Event.At.In(loc)
	return at.Format("Jan 2, 2006 15:04 MST") + " (UTC" + eventOffset(at) + ")"
}

var errInternalNoteInput = errors.New("Enter a note up to 2,000 bytes. Reload the page if this form is out of date.")

func validateInternalNote(input internalNoteInput, now time.Time) (eventTime, error) {
	if !validStoredText(input.Body, input.Date, input.Time, input.Zone, input.Offset) {
		return eventTime{}, errInternalNoteInput
	}
	body := strings.TrimSpace(input.Body)
	if !noteSubmissionKey.MatchString(input.Key) || body == "" || len(body) > 2000 || strings.ContainsRune(body, '\x00') {
		return eventTime{}, errInternalNoteInput
	}
	return parseEventTime(input.Date, input.Time, input.Zone, input.Offset, now)
}
