package app

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"reflect"
)

func TestVisitNotesPreserveWorkStateAndPrivacy(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	now := time.Date(2034, 2, 5, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, fmt.Sprintf("visit-history-%d", time.Now().UnixNano()))
	other := createTestShop(t, ctx, db, fmt.Sprintf("visit-history-other-%d", time.Now().UnixNano()))
	customer := createTestCustomer(t, ctx, db, shop, "Visit history customer")
	agreement, err := s.CreateAgreement(ctx, shop, CreateAgreementInput{CustomerID: customer, Status: AgreementActive, AnniversaryDate: now.AddDate(1, 0, 0), VisitsEntitled: 5})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateServiceSchedule(ctx, shop, ServiceScheduleInput{AgreementID: agreement, ServiceName: "Weekly service", CadenceUnit: "week", CadenceInterval: 1, StartsOn: now.AddDate(0, 0, -14)})
	if err != nil {
		t.Fatal(err)
	}
	recurring, err := s.ListServiceOccurrences(ctx, shop, false)
	if err != nil || len(recurring) < 2 {
		t.Fatal("recurring fixtures", err)
	}
	seasonal, err := s.ListVisitDues(ctx, shop, false)
	if err != nil || len(seasonal) < 2 {
		t.Fatal("seasonal fixtures", err)
	}
	beforeRecurring, err := s.ServiceOccurrenceExportRows(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	beforeSeasonal, err := s.VisitExportRows(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	type workState struct {
		Used, Recurring, Seasonal int
		Statuses                  string
	}
	state := func() workState {
		t.Helper()
		var v workState
		err := db.Pool.QueryRow(ctx, `SELECT visits_used,
   (SELECT count(*) FROM service_occurrences WHERE shop_id=$1),
   (SELECT count(*) FROM visit_dues WHERE shop_id=$1),
   (SELECT string_agg(id::text||':'||status,',' ORDER BY id) FROM (
    SELECT id,status FROM service_occurrences WHERE shop_id=$1 UNION ALL SELECT id,status FROM visit_dues WHERE shop_id=$1) work)
   FROM agreements WHERE shop_id=$1 AND id=$2`, shop, agreement).Scan(&v.Used, &v.Recurring, &v.Seasonal, &v.Statuses)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := state()
	targets := []struct{ kind, id, otherID, path string }{
		{"recurring", recurring[0].ID, recurring[1].ID, "/service-occurrences/"},
		{"seasonal", seasonal[0].ID, seasonal[1].ID, "/visits/"},
	}
	for i, target := range targets {
		t.Run(target.kind, func(t *testing.T) {
			input := internalNoteInput{Key: fmt.Sprintf("%032x", i+1), Body: "Private inspection " + target.kind, Date: "2034-02-04", Time: "09:15", Zone: "America/New_York"}
			if _, err := s.GetVisitHistoryTarget(ctx, other, target.kind, target.id); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal("foreign lookup", err)
			}
			if err := s.AddVisitNote(ctx, other, target.kind, target.id, "foreign", input); !errors.Is(err, errVisitNoteConflict) {
				t.Fatal("foreign write", err)
			}
			results := make(chan error, 2)
			for j := 0; j < 2; j++ {
				go func() {
					results <- s.AddVisitNote(ctx, shop, target.kind, target.id, "first-owner@example.test", input)
				}()
			}
			for j := 0; j < 2; j++ {
				if err := <-results; err != nil {
					t.Fatal(err)
				}
			}
			notes, err := s.ListVisitNotes(ctx, shop, target.kind, target.id)
			if err != nil || len(notes) != 1 || notes[0].Event.At == nil || notes[0].Event.At.Format(time.RFC3339) != "2034-02-04T14:15:00Z" {
				t.Fatalf("timed note: %+v %v", notes, err)
			}
			original := notes[0]
			for _, change := range []func(*internalNoteInput){func(n *internalNoteInput) { n.Body = "Changed" }, func(n *internalNoteInput) { n.Time = "" }} {
				changed := input
				change(&changed)
				if err := s.AddVisitNote(ctx, shop, target.kind, target.id, "first-owner@example.test", changed); !errors.Is(err, errVisitNoteConflict) {
					t.Fatal("changed retry", err)
				}
			}
			if err := s.AddVisitNote(ctx, shop, target.kind, target.otherID, "first-owner@example.test", input); !errors.Is(err, errVisitNoteConflict) {
				t.Fatal("form moved visit", err)
			}
			input.Key = fmt.Sprintf("%032x", i+10)
			input.Body = "Private correction " + target.kind
			input.Time = ""
			if err := s.AddVisitNote(ctx, shop, target.kind, target.id, "second-owner@example.test", input); err != nil {
				t.Fatal(err)
			}
			notes, err = s.ListVisitNotes(ctx, shop, target.kind, target.id)
			if err != nil || len(notes) != 2 {
				t.Fatal("correction count", err)
			}
			if !reflect.DeepEqual(original, notes[1]) {
				t.Fatal("original changed")
			}
			if notes[0].Event.At != nil || notes[0].RecordedBy != "second-owner@example.test" {
				t.Fatal("correction precision/author")
			}
			for _, scope := range []struct{ shop, id string }{{other, target.id}, {shop, target.otherID}} {
				got, err := s.ListVisitNotes(ctx, scope.shop, target.kind, scope.id)
				if err != nil || len(got) != 0 {
					t.Fatal("history leaked", err)
				}
			}
		})
	}
	if !reflect.DeepEqual(before, state()) {
		t.Fatal("notes changed work state")
	}
	afterRecurring, err := s.ServiceOccurrenceExportRows(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	afterSeasonal, err := s.VisitExportRows(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeRecurring, afterRecurring) {
		t.Fatal("private recurring notes entered export")
	}
	if !reflect.DeepEqual(beforeSeasonal, afterSeasonal) {
		t.Fatal("private seasonal notes entered export")
	}
	// Existing scheduling/completion remains authoritative, with unchanged note IDs.
	if err := s.MoveServiceVisit(ctx, shop, recurring[0].ID, recurring[0].DueOn.AddDate(0, 0, 1), 0); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateServiceOccurrences(ctx, shop, []string{recurring[0].ID}, "completed", "Legacy export note"); err != nil {
		t.Fatal(err)
	}
	if err := s.BulkUpdateVisitStatus(ctx, shop, []string{seasonal[0].ID}, "completed"); err != nil {
		t.Fatal(err)
	}
	completed := state()
	for i, target := range targets {
		input := internalNoteInput{Key: fmt.Sprintf("%032x", i+20), Body: "Private after completion", Date: "2034-02-05", Zone: "UTC"}
		if err := s.AddVisitNote(ctx, shop, target.kind, target.id, "owner", input); err != nil {
			t.Fatal(err)
		}
		got, err := s.ListVisitNotes(ctx, shop, target.kind, target.id)
		if err != nil || len(got) != 3 {
			t.Fatal("history lost after work outcome", err)
		}
	}
	if !reflect.DeepEqual(completed, state()) {
		t.Fatal("post-completion note changed state")
	}
	if completed.Used != 1 {
		t.Fatal("unexpected entitlement count", completed.Used)
	}
	for _, args := range [][]any{{other, recurring[0].ID, nil}, {shop, recurring[0].ID, seasonal[0].ID}} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO visit_notes(shop_id,occurrence_id,seasonal_visit_id,submission_key,body,occurred_on,event_timezone,recorded_by) VALUES ($1,$2,$3,repeat('f',32),'Invalid','2034-02-04','UTC','owner')`, args...); err == nil {
			t.Fatal("database accepted foreign or multiple parents")
		}
	}
	server := &Server{store: s, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	for _, target := range targets {
		request := func(method, tenant, role string, form url.Values) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, target.path+target.id+"/history", strings.NewReader(form.Encode()))
			r.SetPathValue("id", target.id)
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r = r.WithContext(contextWithUser(ctx, &User{ShopID: tenant, Role: role, Email: "owner@example.test"}))
			w := httptest.NewRecorder()
			server.handleInternalHistory(w, r, target.kind)
			return w
		}
		for _, method := range []string{"GET", "POST"} {
			if w := request(method, shop, "staff", nil); w.Code != 403 {
				t.Fatal("staff accessed visit notes", w.Code)
			}
			if w := request(method, other, "owner", nil); w.Code != 404 {
				t.Fatal("foreign handler", w.Code)
			}
		}
		form := url.Values{"note_key": {strings.Repeat("d", 32)}, "note_body": {"Keep <script>private</script>"}, "occurred_on": {"2033-11-06"}, "event_time": {"01:30"}, "event_timezone": {"America/New_York"}}
		w := request("POST", shop, "owner", form)
		if w.Code != 422 || !strings.Contains(w.Body.String(), "Keep &lt;script&gt;private&lt;/script&gt;") || !strings.Contains(w.Body.String(), "-04:00") || !strings.Contains(w.Body.String(), "-05:00") {
			t.Fatalf("invalid time form %d %s", w.Code, w.Body.String())
		}
		w = request("GET", shop, "owner", nil)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Body.String(), "Back to service work") || !strings.Contains(w.Body.String(), "Private after completion") {
			t.Fatal("history display", w.Code)
		}
	}
}
