package app

import "testing"

func TestSQLServerMoveServiceVisitPreservesCadenceAndSummaries(t *testing.T) {
	testMoveServiceVisitPreservesCadenceAndSummaries(t, sqlServerTestDB(t))
}
func TestSQLServerMoveServiceVisitPauseResetsFutureWork(t *testing.T) {
	testMoveServiceVisitPauseResetsFutureWork(t, sqlServerTestDB(t))
}
func TestSQLServerCalendarCountsPagingAndTenantBoundaries(t *testing.T) {
	testCalendarCountsPagingAndTenantBoundaries(t, sqlServerTestDB(t))
}
