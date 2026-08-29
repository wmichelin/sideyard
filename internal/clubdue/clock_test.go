package clubdue

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

func TestClockKeepsRenewalWindowDeterministicAtMidnight(t *testing.T) {
	beforeMidnight := time.Date(2030, time.January, 1, 23, 59, 59, 0, time.UTC)
	atMidnight := beforeMidnight.Add(time.Second)
	cases := []struct {
		name        string
		now         time.Time
		anniversary time.Time
		days        int
		status      AgreementStatus
		want        bool
	}{
		{
			name:        "tomorrow is excluded before the renewal window starts",
			now:         beforeMidnight,
			anniversary: atMidnight,
			days:        0,
			status:      AgreementActive,
			want:        false,
		},
		{
			name:        "renewal is included at midnight",
			now:         atMidnight,
			anniversary: atMidnight,
			days:        0,
			status:      AgreementActive,
			want:        true,
		},
		{
			name:        "renewal at the configured window end is included",
			now:         atMidnight,
			anniversary: atMidnight.AddDate(0, 0, 30),
			days:        30,
			status:      AgreementActive,
			want:        true,
		},
		{
			name:        "past renewal is excluded",
			now:         atMidnight,
			anniversary: beforeMidnight,
			days:        30,
			status:      AgreementActive,
			want:        false,
		},
		{
			name:        "inactive agreement is excluded",
			now:         atMidnight,
			anniversary: atMidnight,
			days:        30,
			status:      AgreementLapsed,
			want:        false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := &Server{clock: fixedClock{now: tc.now}}
			agreement := Agreement{Status: tc.status, AnniversaryDate: tc.anniversary}
			if got := agreementIsWithinRenewalWindow(agreement, server.nowUTC(), tc.days); got != tc.want {
				t.Fatalf("agreementIsWithinRenewalWindow() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestClockControlsRenewalLinkSessionAndVisitBoundaries(t *testing.T) {
	now := time.Date(2030, time.November, 30, 23, 59, 59, 0, time.UTC)
	clock := fixedClock{now: now}
	server := &Server{clock: clock}
	auth := newAuthWithClock(nil, "test-session-secret", false, clock)
	store := &Store{clock: clock}

	if got, want := server.renewalLinkExpiry(), now.Add(renewalLinkTTL); !got.Equal(want) {
		t.Fatalf("renewal link expiry = %s, want %s", got, want)
	}
	if got, want := auth.sessionExpiry(), now.Add(sessionTTL); !got.Equal(want) {
		t.Fatalf("session expiry = %s, want %s", got, want)
	}

	for _, tc := range []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "fall due visit is not overdue at window close", now: seasonWindowEnd("fall", 2030), want: false},
		{name: "fall due visit becomes overdue after window close", now: seasonWindowEnd("fall", 2030).Add(time.Second), want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store.clock = fixedClock{now: tc.now}
			if got := isVisitOverdue("fall", 2030, VisitDue, store.nowUTC()); got != tc.want {
				t.Fatalf("isVisitOverdue() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestInjectedClockControlsPersistedRenewalVisitAndSessionOutcomes(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2030, time.November, 30, 23, 59, 59, 0, time.UTC)
	clock := &fixedClock{now: now}
	store := &Store{db: db, clock: clock}
	server := &Server{clock: clock}
	auth := newAuthWithClock(db, "test-session-secret", false, clock)
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	shopID := createTestShop(t, ctx, db, "clock-"+stamp)
	customerID := createTestCustomer(t, ctx, db, shopID, "Clock Customer")
	planID := createTestPlan(t, ctx, db, shopID, "Clock Plan")
	agreementID, err := store.CreateAgreement(ctx, shopID, CreateAgreementInput{
		CustomerID: customerID, PlanTemplateID: planID, Status: AgreementActive,
		AnniversaryDate: now.AddDate(0, 0, 30), VisitsEntitled: 2, PriceCents: 19900,
	})
	if err != nil {
		t.Fatalf("create agreement: %v", err)
	}

	if err := store.GenerateRenewalTasks(ctx, shopID, now); err != nil {
		t.Fatalf("generate renewal tasks: %v", err)
	}
	queue, err := store.RenewalQueue(ctx, shopID)
	if err != nil {
		t.Fatalf("load renewal queue at fixed date: %v", err)
	}
	if got := renewalBucketItemCount(queue); got != 3 {
		t.Fatalf("renewal queue memberships at fixed date = %d, want 3", got)
	}
	stats, err := store.DashboardStats(ctx, shopID)
	if err != nil {
		t.Fatalf("load dashboard stats at fixed date: %v", err)
	}
	if stats.RenewalsDue90 != 1 {
		t.Fatalf("renewals due in 90 days = %d, want 1", stats.RenewalsDue90)
	}
	tasks, err := store.ListRenewalTasks(ctx, shopID)
	if err != nil {
		t.Fatalf("list renewal tasks at fixed date: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("expected fixed clock to expose due renewal work")
	}
	clock.now = now.AddDate(0, 0, -90)
	tasks, err = store.ListRenewalTasks(ctx, shopID)
	if err != nil {
		t.Fatalf("list renewal tasks before milestones: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("renewal tasks before milestones = %d, want 0", len(tasks))
	}
	queue, err = store.RenewalQueue(ctx, shopID)
	if err != nil {
		t.Fatalf("load renewal queue before fixed window: %v", err)
	}
	if got := renewalBucketItemCount(queue); got != 0 {
		t.Fatalf("renewal queue before fixed window = %d, want 0", got)
	}
	clock.now = now

	if err := store.SyncVisitDues(ctx, shopID); err != nil {
		t.Fatalf("sync visit dues: %v", err)
	}
	visits, err := store.ListVisitDues(ctx, shopID, true)
	if err != nil {
		t.Fatalf("list visit dues at window close: %v", err)
	}
	if !visitIsOverdue(visits, "fall", false) {
		t.Fatal("fall visit was not available and on time at the fixed window close")
	}
	clock.now = now.Add(time.Second)
	visits, err = store.ListVisitDues(ctx, shopID, true)
	if err != nil {
		t.Fatalf("list visit dues after window close: %v", err)
	}
	if !visitIsOverdue(visits, "fall", true) {
		t.Fatal("fall visit did not become overdue after the fixed window close")
	}
	clock.now = now

	if err := store.CreateRenewalLink(ctx, shopID, agreementID, "clock-renewal-link", server.renewalLinkExpiry()); err != nil {
		t.Fatalf("create renewal link: %v", err)
	}
	if _, err := store.GetHomeownerRenewal(ctx, "clock-renewal-link"); err != nil {
		t.Fatalf("renewal link before fixed expiry: %v", err)
	}
	clock.now = server.renewalLinkExpiry()
	if _, err := store.GetHomeownerRenewal(ctx, "clock-renewal-link"); err == nil {
		t.Fatal("renewal link was accepted at its fixed expiry")
	}
	clock.now = now

	email := "clock-owner-" + stamp + "@example.test"
	createTestUser(t, ctx, db, shopID, email)
	var userID string
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&userID); err != nil {
		t.Fatalf("find test user: %v", err)
	}
	token, err := auth.createSession(ctx, userID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	if _, err := auth.userFromRequest(ctx, request); err != nil {
		t.Fatalf("session before fixed expiry: %v", err)
	}
	clock.now = auth.sessionExpiry()
	if _, err := auth.userFromRequest(ctx, request); err == nil {
		t.Fatal("session was accepted at its fixed expiry")
	}
}

func visitIsOverdue(visits []VisitDueRow, season string, want bool) bool {
	for _, visit := range visits {
		if visit.Season == season {
			return visit.IsOverdue == want
		}
	}
	return false
}

func renewalBucketItemCount(buckets []RenewalBucket) int {
	count := 0
	for _, bucket := range buckets {
		count += len(bucket.Items)
	}
	return count
}
