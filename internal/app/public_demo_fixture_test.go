package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPublicDemoFixtureContract(t *testing.T) {
	for _, asOf := range []time.Time{
		time.Date(2032, time.February, 29, 9, 0, 0, 0, time.UTC),
		time.Date(2035, time.September, 2, 12, 0, 0, 0, time.UTC),
		time.Date(2036, time.January, 31, 18, 0, 0, 0, time.UTC),
	} {
		if err := publicDemoFixture.validate(asOf); err != nil {
			t.Fatalf("validate fixture at %s: %v", asOf.Format(time.DateOnly), err)
		}
	}
	for _, valid := range []string{"(614) 555-0100", "740-555-0199"} {
		if !isReservedPublicDemoPhone(valid) {
			t.Fatalf("reserved fictional number %q was rejected", valid)
		}
	}
	for _, invalid := range []string{"(614) 555-0099", "(614) 555-0200", "(614) 867-5309", "555-0100"} {
		if isReservedPublicDemoPhone(invalid) {
			t.Fatalf("non-reserved number %q was accepted", invalid)
		}
	}
}

func TestPublicDemoHydratesRealisticLandscapingBook(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2035, time.September, 2, 12, 0, 0, 0, time.UTC)
	store := &Store{db: db, clock: fixedClock{now: now}}
	result, err := store.SeedPublicDemo(ctx)
	if err != nil {
		t.Fatal(err)
	}

	expectedCounts := map[string]int{
		"plan_templates":    publicDemoPlanCount,
		"customers":         publicDemoCustomerCount,
		"properties":        publicDemoPropertyCount,
		"equipment":         publicDemoEquipmentCount,
		"agreements":        publicDemoAgreementCount,
		"service_schedules": publicDemoScheduleCount,
		"visit_dues":        publicDemoSeasonalVisitCount,
	}
	for table, want := range expectedCounts {
		if got := countShopRows(t, ctx, db, table, result.ShopID); got != want {
			t.Fatalf("public demo %s = %d, want %d", table, got, want)
		}
	}

	statusCounts := make(map[AgreementStatus]int)
	rows, err := db.Pool.Query(ctx, `
		SELECT status, count(*) FROM agreements WHERE shop_id = $1 GROUP BY status`, result.ShopID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var status AgreementStatus
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		statusCounts[status] = count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if statusCounts[AgreementActive] != 20 || statusCounts[AgreementPendingRenewal] != 2 ||
		statusCounts[AgreementPastDue] != 1 || statusCounts[AgreementLapsed] != 2 ||
		statusCounts[AgreementCancelled] != 1 {
		t.Fatalf("public demo agreement statuses = %+v", statusCounts)
	}

	var cityCount, customerOnlyCount, multiLocationCount, sentinelCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(DISTINCT city) FROM properties WHERE shop_id = $1`, result.ShopID).Scan(&cityCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM customers c
		WHERE c.shop_id = $1 AND NOT EXISTS (
			SELECT 1 FROM properties p WHERE p.shop_id = c.shop_id AND p.customer_id = c.id
		)`, result.ShopID).Scan(&customerOnlyCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT customer_id FROM properties WHERE shop_id = $1
			GROUP BY customer_id HAVING count(*) > 1
		) multi_location`, result.ShopID).Scan(&multiLocationCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM customers
		WHERE shop_id = $1 AND name IN (
			'Maya Thompson', 'Willowmere Commons HOA', 'Buckeye Key Property Management'
		)`, result.ShopID).Scan(&sentinelCount); err != nil {
		t.Fatal(err)
	}
	if cityCount < 4 || customerOnlyCount != 4 || multiLocationCount != 3 || sentinelCount != 3 {
		t.Fatalf("public demo customer depth cities=%d customer_only=%d multi_location=%d sentinels=%d",
			cityCount, customerOnlyCount, multiLocationCount, sentinelCount)
	}

	contacts, err := db.Pool.Query(ctx, `SELECT email, phone FROM customers WHERE shop_id = $1`, result.ShopID)
	if err != nil {
		t.Fatal(err)
	}
	emailOnly, phoneOnly := 0, 0
	for contacts.Next() {
		var email, phone *string
		if err := contacts.Scan(&email, &phone); err != nil {
			contacts.Close()
			t.Fatal(err)
		}
		if email != nil && !hasTestEmailSuffix(*email) {
			contacts.Close()
			t.Fatalf("public demo persisted non-reserved email %q", *email)
		}
		if phone != nil && !isReservedPublicDemoPhone(*phone) {
			contacts.Close()
			t.Fatalf("public demo persisted non-fictional phone %q", *phone)
		}
		if email != nil && phone == nil {
			emailOnly++
		}
		if email == nil && phone != nil {
			phoneOnly++
		}
	}
	contacts.Close()
	if err := contacts.Err(); err != nil {
		t.Fatal(err)
	}
	if emailOnly == 0 || phoneOnly == 0 {
		t.Fatalf("public demo contact gaps email_only=%d phone_only=%d", emailOnly, phoneOnly)
	}

	var activeSchedules, pausedSchedules, inactiveAgreementSchedules, activeCadences int
	if err := db.Pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE sc.active AND a.status = 'active'),
			count(*) FILTER (WHERE NOT sc.active),
			count(*) FILTER (WHERE sc.active AND a.status <> 'active'),
			count(DISTINCT (sc.cadence_unit, sc.cadence_interval))
				FILTER (WHERE sc.active AND a.status = 'active')
		FROM service_schedules sc
		JOIN agreements a ON a.id = sc.agreement_id AND a.shop_id = sc.shop_id
		WHERE sc.shop_id = $1`, result.ShopID).Scan(
		&activeSchedules, &pausedSchedules, &inactiveAgreementSchedules, &activeCadences,
	); err != nil {
		t.Fatal(err)
	}
	if activeSchedules != publicDemoActiveScheduleCount || pausedSchedules != 3 ||
		inactiveAgreementSchedules != 3 || activeCadences != 3 {
		t.Fatalf("public demo schedule mix active=%d paused=%d inactive=%d cadences=%d",
			activeSchedules, pausedSchedules, inactiveAgreementSchedules, activeCadences)
	}

	var totalOccurrences, dueOccurrences, overdueOccurrences, completedOccurrences, skippedOccurrences, upcomingOccurrences int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*),
			count(*) FILTER (WHERE o.status = 'due' AND sc.active AND a.status = 'active'),
			count(*) FILTER (WHERE o.status = 'due' AND sc.active AND a.status = 'active' AND o.due_on < $2::date),
			count(*) FILTER (WHERE o.status = 'completed'),
			count(*) FILTER (WHERE o.status = 'skipped'),
			count(*) FILTER (WHERE o.status = 'due' AND sc.active AND a.status = 'active' AND o.due_on >= $2::date)
		FROM service_occurrences o
		JOIN service_schedules sc ON sc.id = o.schedule_id AND sc.shop_id = o.shop_id
		JOIN agreements a ON a.id = sc.agreement_id AND a.shop_id = sc.shop_id
		WHERE o.shop_id = $1`, result.ShopID, now).Scan(
		&totalOccurrences, &dueOccurrences, &overdueOccurrences,
		&completedOccurrences, &skippedOccurrences, &upcomingOccurrences,
	); err != nil {
		t.Fatal(err)
	}
	if totalOccurrences <= 0 || totalOccurrences > publicDemoBaseOccurrenceCeiling ||
		dueOccurrences <= 0 || dueOccurrences > publicDemoDueOccurrenceCeiling || overdueOccurrences != 3 ||
		completedOccurrences == 0 || skippedOccurrences < 2 || upcomingOccurrences == 0 {
		t.Fatalf("public demo occurrence mix total=%d due=%d overdue=%d completed=%d skipped=%d upcoming=%d",
			totalOccurrences, dueOccurrences, overdueOccurrences, completedOccurrences, skippedOccurrences, upcomingOccurrences)
	}
	dueWork, err := store.ListServiceOccurrences(ctx, result.ShopID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dueWork) != dueOccurrences || len(dueWork) > publicDemoDueOccurrenceCeiling {
		t.Fatalf("public demo due-work page rows=%d, persisted=%d", len(dueWork), dueOccurrences)
	}
	allWork, err := store.ListServiceOccurrences(ctx, result.ShopID, false)
	if err != nil {
		t.Fatal(err)
	}
	visibleStates := make(map[string]bool)
	for _, work := range allWork {
		visibleStates[work.StatusLabel] = true
	}
	if !visibleStates["Paused"] || !visibleStates["Plan inactive"] ||
		!visibleStates["Completed"] || !visibleStates["Skipped"] {
		t.Fatalf("public demo all-work lifecycle states = %v", visibleStates)
	}

	var completedVisits, scheduledVisits, dueVisits, visitsUsed int
	if err := db.Pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE status = 'completed'),
			count(*) FILTER (WHERE status = 'scheduled'),
			count(*) FILTER (WHERE status = 'due')
		FROM visit_dues WHERE shop_id = $1`, result.ShopID).Scan(&completedVisits, &scheduledVisits, &dueVisits); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT sum(visits_used) FROM agreements WHERE shop_id = $1`, result.ShopID).Scan(&visitsUsed); err != nil {
		t.Fatal(err)
	}
	if completedVisits != 3 || scheduledVisits != 1 || dueVisits != 4 || visitsUsed != 3 {
		t.Fatalf("public demo seasonal mix completed=%d scheduled=%d due=%d used=%d",
			completedVisits, scheduledVisits, dueVisits, visitsUsed)
	}

	if got := countShopRows(t, ctx, db, "renewal_tasks", result.ShopID); got != 25 {
		t.Fatalf("public demo renewal tasks = %d, want 25", got)
	}
	renewalTasks, err := store.ListRenewalTasks(ctx, result.ShopID)
	if err != nil {
		t.Fatal(err)
	}
	contacted := 0
	renewalHorizons := make(map[int]bool)
	for _, task := range renewalTasks {
		contacted += boolInt(task.Status == "contacted")
		renewalHorizons[task.Days] = true
	}
	if len(renewalTasks) != publicDemoVisibleRenewalTaskCeiling || contacted != 1 ||
		!renewalHorizons[-6] || !renewalHorizons[14] || !renewalHorizons[30] ||
		!renewalHorizons[58] || !renewalHorizons[82] {
		t.Fatalf("public demo visible renewals=%d contacted=%d horizons=%v", len(renewalTasks), contacted, renewalHorizons)
	}

	stats, err := store.DashboardStats(ctx, result.ShopID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ActiveCount != publicDemoActiveAgreementCount || stats.RenewalsDueMonth != 1 ||
		stats.RenewalsDue90 != 4 || stats.LapsedMTD != 2 || stats.MRRCents != 471533 ||
		stats.VisitsOverdue != 4 {
		t.Fatalf("public demo dashboard stats = %+v", stats)
	}

	var mutationsUsed, importRowsUsed, importOccurrencesUsed, sampleOnlyCustomers int
	if err := db.Pool.QueryRow(ctx, `
		SELECT demo_mutations_used, demo_import_rows_used, demo_import_occurrences_used
		FROM shops WHERE id = $1`, result.ShopID).Scan(
		&mutationsUsed, &importRowsUsed, &importOccurrencesUsed,
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM customers WHERE shop_id = $1 AND name = 'Jordan Green'`,
		result.ShopID).Scan(&sampleOnlyCustomers); err != nil {
		t.Fatal(err)
	}
	if mutationsUsed != 0 || importRowsUsed != 0 || importOccurrencesUsed != 0 || sampleOnlyCustomers != 0 {
		t.Fatalf("public demo initial budgets/sample leaked mutations=%d rows=%d occurrences=%d sample=%d",
			mutationsUsed, importRowsUsed, importOccurrencesUsed, sampleOnlyCustomers)
	}
}

func TestPublicDemoSeasonalHistoryFollowsCalendar(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tests := []struct {
		name                    string
		now                     time.Time
		completed, scheduled    int
		completedSeason         string
		scheduledSeason         string
		expectedAgreementVisits int
	}{
		{"winter", time.Date(2035, time.January, 15, 12, 0, 0, 0, time.UTC), 0, 1, "", "spring", 0},
		{"spring", time.Date(2035, time.April, 15, 12, 0, 0, 0, time.UTC), 3, 1, "spring", "spring", 3},
		{"fall", time.Date(2035, time.September, 2, 12, 0, 0, 0, time.UTC), 3, 1, "spring", "fall", 3},
		{"late winter", time.Date(2035, time.December, 15, 12, 0, 0, 0, time.UTC), 3, 1, "fall", "fall", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &Store{db: db, clock: fixedClock{now: tc.now}}
			result, err := store.SeedPublicDemo(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var completed, scheduled, agreementVisits int
			if err := db.Pool.QueryRow(ctx, `
				SELECT
					count(*) FILTER (WHERE status = 'completed'),
					count(*) FILTER (WHERE status = 'scheduled')
				FROM visit_dues WHERE shop_id = $1`, result.ShopID).Scan(&completed, &scheduled); err != nil {
				t.Fatal(err)
			}
			if err := db.Pool.QueryRow(ctx, `
				SELECT sum(visits_used) FROM agreements WHERE shop_id = $1`, result.ShopID).Scan(&agreementVisits); err != nil {
				t.Fatal(err)
			}
			if completed != tc.completed || scheduled != tc.scheduled || agreementVisits != tc.expectedAgreementVisits {
				t.Fatalf("seasonal lifecycle completed=%d scheduled=%d visits_used=%d", completed, scheduled, agreementVisits)
			}
			if tc.completed > 0 {
				var wrongSeason, outOfWindow int
				if err := db.Pool.QueryRow(ctx, `
					SELECT
						count(*) FILTER (WHERE season <> $2),
						count(*) FILTER (
							WHERE completed_at < make_date(due_year, CASE season WHEN 'spring' THEN 3 ELSE 9 END, 1)
							   OR completed_at >= make_date(due_year, CASE season WHEN 'spring' THEN 6 ELSE 12 END, 1)
						)
					FROM visit_dues
					WHERE shop_id = $1 AND status = 'completed'`, result.ShopID, tc.completedSeason).Scan(&wrongSeason, &outOfWindow); err != nil {
					t.Fatal(err)
				}
				if wrongSeason != 0 || outOfWindow != 0 {
					t.Fatalf("completed seasonal history wrong_season=%d outside_window=%d", wrongSeason, outOfWindow)
				}
			}
			var scheduledSeason string
			var scheduledAt time.Time
			if err := db.Pool.QueryRow(ctx, `
				SELECT season, scheduled_at FROM visit_dues
				WHERE shop_id = $1 AND status = 'scheduled'`, result.ShopID).Scan(&scheduledSeason, &scheduledAt); err != nil {
				t.Fatal(err)
			}
			if scheduledSeason != tc.scheduledSeason || !scheduledAt.Equal(tc.now) {
				t.Fatalf("seasonal handoff season=%q timestamp=%s", scheduledSeason, scheduledAt)
			}
		})
	}
}

func TestPublicDemoConcurrentHydrationStaysWithinAdmissionBudget(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const concurrentStarts = 20
	store := &Store{db: db, clock: fixedClock{now: time.Date(2035, time.September, 2, 12, 0, 0, 0, time.UTC)}}
	type seedOutcome struct {
		result PublicDemoResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan seedOutcome, concurrentStarts)
	var group sync.WaitGroup
	for range concurrentStarts {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, err := store.SeedPublicDemo(ctx)
			outcomes <- seedOutcome{result: result, err: err}
		}()
	}
	startedAt := time.Now()
	close(start)
	group.Wait()
	close(outcomes)
	if elapsed := time.Since(startedAt); elapsed >= 10*time.Second {
		t.Fatalf("20 serialized public demo starts took %s", elapsed)
	}

	shopIDs := make(map[string]struct{}, concurrentStarts)
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("concurrent public demo start: %v", outcome.err)
		}
		if _, exists := shopIDs[outcome.result.ShopID]; exists {
			t.Fatalf("concurrent public demo reused shop %s", outcome.result.ShopID)
		}
		shopIDs[outcome.result.ShopID] = struct{}{}
		if got := countShopRows(t, ctx, db, "customers", outcome.result.ShopID); got != publicDemoCustomerCount {
			t.Fatalf("concurrent public demo customers = %d", got)
		}
	}
	if len(shopIDs) != concurrentStarts {
		t.Fatalf("concurrent public demo shops = %d, want %d", len(shopIDs), concurrentStarts)
	}
	for shopID := range shopIDs {
		if _, err := db.Pool.Exec(ctx, `DELETE FROM shops WHERE id = $1 AND kind = 'demo_sandbox'`, shopID); err != nil {
			t.Fatal(err)
		}
	}
}

func hasTestEmailSuffix(email string) bool {
	return strings.Count(email, "@") == 1 && strings.HasSuffix(strings.ToLower(email), ".test")
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
