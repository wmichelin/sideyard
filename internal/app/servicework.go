package app

import (
	"context"
	"time"

	"github.com/wmichelin/clubdue/internal/servicework"
)

type ServiceScheduleInput = servicework.ScheduleInput
type ServiceSchedule = servicework.Schedule
type ServiceOccurrence = servicework.Occurrence
type ServiceExportRow = servicework.ExportRow

func (s *Store) serviceWorkService() *servicework.Service {
	return servicework.New(s.db.Pool, s.nowUTC)
}

func (s *Store) CreateServiceSchedule(ctx context.Context, shopID string, input ServiceScheduleInput) (string, error) {
	return s.serviceWorkService().CreateSchedule(ctx, shopID, input)
}

func (s *Store) ListServiceSchedules(ctx context.Context, shopID, agreementID string) ([]ServiceSchedule, error) {
	return s.serviceWorkService().ListSchedulesForAgreement(ctx, shopID, agreementID)
}

func (s *Store) SetServiceScheduleActive(ctx context.Context, shopID, scheduleID string, active bool) error {
	return s.serviceWorkService().SetScheduleActive(ctx, shopID, scheduleID, active)
}

func (s *Store) ListServiceOccurrences(ctx context.Context, shopID string, dueOnly bool) ([]ServiceOccurrence, error) {
	return s.serviceWorkService().ListOccurrences(ctx, shopID, dueOnly)
}

func (s *Store) GenerateAllServiceOccurrences(ctx context.Context) error {
	return s.serviceWorkService().GenerateAllOccurrences(ctx)
}

func (s *Store) UpdateServiceOccurrence(ctx context.Context, shopID, occurrenceID, status, notes string) error {
	return s.serviceWorkService().UpdateOccurrenceStatus(ctx, shopID, occurrenceID, status, notes)
}

func (s *Store) UpdateServiceOccurrences(ctx context.Context, shopID string, occurrenceIDs []string, status, notes string) error {
	return s.serviceWorkService().UpdateOccurrenceStatuses(ctx, shopID, occurrenceIDs, status, notes)
}

func (s *Store) CountServiceOccurrencesOverdue(ctx context.Context, shopID string) (int, error) {
	return s.serviceWorkService().CountOverdue(ctx, shopID)
}

func (s *Store) ServiceOccurrenceExportRows(ctx context.Context, shopID string, dueOnly bool) ([]ServiceExportRow, error) {
	return s.serviceWorkService().ExportRows(ctx, shopID, dueOnly)
}

func validateServiceSchedule(input ServiceScheduleInput) error {
	return servicework.ValidateSchedule(input)
}

func serviceCadenceLabel(unit string, interval int) string {
	return servicework.CadenceLabel(unit, interval)
}

func serviceScheduleEndDate(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func projectedImportOccurrenceCount(rows []ImportRow, asOf time.Time) (int, error) {
	total := 0
	for _, row := range rows {
		if row.Error != "" || row.ServiceName == "" || row.Status != AgreementActive {
			continue
		}
		count, err := servicework.ProjectedOccurrenceCount(servicework.ScheduleInput{
			AgreementID:     row.ExternalID,
			ExternalID:      row.ServiceExternalID,
			ServiceName:     row.ServiceName,
			CadenceUnit:     row.CadenceUnit,
			CadenceInterval: row.CadenceInterval,
			StartsOn:        row.ServiceStartsOn,
			EndsOn:          row.ServiceEndsOn,
		}, asOf)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}
