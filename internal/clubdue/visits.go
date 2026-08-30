package clubdue

import (
	"context"
	"time"

	"github.com/wmichelin/clubdue/internal/visits"
)

type VisitStatus = visits.VisitStatus

const (
	VisitDue       = visits.VisitDue
	VisitScheduled = visits.VisitScheduled
	VisitCompleted = visits.VisitCompleted
)

type VisitDueRow = visits.VisitDueRow
type VisitExportRow = visits.VisitExportRow

func (s *Store) visitService() *visits.Service {
	return visits.New(s.db.Pool, s.nowUTC)
}

func (s *Store) SyncVisitDues(ctx context.Context, shopID string) error {
	return s.visitService().SyncVisitDues(ctx, shopID)
}

func (s *Store) SyncAllVisitDues(ctx context.Context) error {
	return s.visitService().SyncAllVisitDues(ctx)
}

func (s *Store) ListVisitDues(ctx context.Context, shopID string, dueOnly bool) ([]VisitDueRow, error) {
	return s.visitService().ListVisitDues(ctx, shopID, dueOnly)
}

func (s *Store) CountVisitsOverdue(ctx context.Context, shopID string) (int, error) {
	return s.visitService().CountVisitsOverdue(ctx, shopID)
}

func (s *Store) BulkUpdateVisitStatus(ctx context.Context, shopID string, visitIDs []string, action string) error {
	return s.visitService().BulkUpdateVisitStatus(ctx, shopID, visitIDs, action)
}

func (s *Store) VisitExportRows(ctx context.Context, shopID string, dueOnly bool) ([]VisitExportRow, error) {
	return s.visitService().VisitExportRows(ctx, shopID, dueOnly)
}

func seasonWindowEnd(season string, year int) time.Time {
	return visits.SeasonWindowEnd(season, year)
}

func isVisitOverdue(season string, year int, status VisitStatus, now time.Time) bool {
	return visits.IsOverdue(season, year, status, now)
}

func writeVisitExportCSV(w interface {
	Write(p []byte) (n int, err error)
}, rows []VisitExportRow) error {
	return visits.WriteExportCSV(w, rows)
}

func visitStatusLabel(status VisitStatus) string {
	return visits.StatusLabel(status)
}
