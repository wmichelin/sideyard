package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/wmichelin/clubdue/internal/database"
	"html/template"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSQLServerVisitNotesPreserveWorkStateAndPrivacy(t *testing.T) {
	testVisitNotesPreserveWorkStateAndPrivacy(t, sqlServerTestDB(t))
}

func TestSQLServerVisitNotesExactRetriesFailuresAndHistoryWindow(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)}}
	shop := createTestShop(t, ctx, db, "visit-note-retries")
	_, _, work := recurringFixture(t, db, s, shop, 5, false)
	seasons, err := s.ListVisitDues(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	for i, target := range []struct{ kind, id, column string }{{"recurring", work[0].ID, "occurrence_id"}, {"seasonal", seasons[0].ID, "seasonal_visit_id"}} {
		input := internalNoteInput{Key: fmt.Sprintf("%032x", i), Body: " \u2060 ", Date: "2033-11-06", Time: "01:30", Zone: "America/New_York", Offset: "-05:00"}
		author := strings.Repeat("作者", 3000) + " "
		if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_test_visit_note ON dbo.visit_notes AFTER INSERT AS BEGIN THROW 51000,'synthetic failure',1; END`); err != nil {
			t.Fatal(err)
		}
		if err := s.AddVisitNote(ctx, shop, target.kind, target.id, author, input); err == nil {
			t.Fatal("failed insertion saved")
		}
		got, err := s.ListVisitNotes(ctx, shop, target.kind, target.id)
		if err != nil || len(got) != 0 {
			t.Fatal("failed insertion persisted", err)
		}
		if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_test_visit_note`); err != nil {
			t.Fatal(err)
		}
		if err := s.AddVisitNote(ctx, shop, target.kind, target.id, author, input); err != nil {
			t.Fatal(err)
		}
		if err := s.AddVisitNote(ctx, shop, target.kind, strings.ToUpper(target.id), author, input); err != nil {
			t.Fatal("identical retry", err)
		}
		got, err = s.ListVisitNotes(ctx, shop, target.kind, target.id)
		if err != nil || len(got) != 1 || got[0].Body != "\u2060" || got[0].RecordedBy != author || got[0].Event.At == nil || got[0].Event.At.Format(time.RFC3339) != "2033-11-06T06:30:00Z" {
			t.Fatal("exact text/fold lost", err)
		}
		for _, change := range []func(*internalNoteInput){func(n *internalNoteInput) { n.Date = "2033-11-05"; n.Offset = "" }, func(n *internalNoteInput) { n.Zone = "US/Eastern" }, func(n *internalNoteInput) { n.Offset = "-04:00" }, func(n *internalNoteInput) { n.Time = "" }, func(n *internalNoteInput) { n.Body = "x" }} {
			next := input
			change(&next)
			if err := s.AddVisitNote(ctx, shop, target.kind, target.id, author, next); !errors.Is(err, errVisitNoteConflict) {
				t.Fatal("changed retry accepted", err)
			}
		}
		for _, changedAuthor := range []string{strings.TrimSpace(author), "AUTHOR", author + "\u2060"} {
			if err := s.AddVisitNote(ctx, shop, target.kind, target.id, changedAuthor, input); !errors.Is(err, errVisitNoteConflict) {
				t.Fatal("author silently normalized", err)
			}
		}
		// Equal event/recording timestamps force canonical UUID ordering BEFORE TOP.
		for n := 1; n <= 101; n++ {
			uuid := fmt.Sprintf("%08x-0000-0000-0000-%012x", n, 1000-n)
			if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.visit_notes(id,shop_id,`+target.column+`,submission_key,body,occurred_on,event_timezone,recorded_by,recorded_at) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),CONVERT(uniqueidentifier,@p3),@p4,@p5,'2034-01-01',N'UTC',N'Owner','2034-01-01')`, uuid, shop, target.id, fmt.Sprintf("%032x", 1000+i*1000+n), fmt.Sprint(n)); err != nil {
				t.Fatal(err)
			}
		}
		got, err = s.ListVisitNotes(ctx, shop, target.kind, target.id)
		if err != nil || len(got) != 100 || got[0].Body != "101" || got[99].Body != "2" {
			t.Fatal("history window order", err)
		}
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.visit_notes WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND `+target.column+`=CONVERT(uniqueidentifier,@p2)`, shop, target.id).Scan(&count); err != nil || count != 102 {
			t.Fatal("older history removed", err)
		}
		// IDs for the second kind must be distinct across this single notes table.
		if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.visit_notes WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
			t.Fatal(err)
		}
	}
	// One global submission key cannot attach to two kinds even concurrently.
	input := internalNoteInput{Key: strings.Repeat("f", 32), Body: "Both kinds", Date: "2034-01-01", Zone: "UTC"}
	gate := make(chan struct{})
	done := make(chan error, 2)
	for _, target := range []struct{ kind, id string }{{"recurring", work[0].ID}, {"seasonal", seasons[0].ID}} {
		go func(kind, id string) { <-gate; done <- s.AddVisitNote(ctx, shop, kind, id, "Owner", input) }(target.kind, target.id)
	}
	close(gate)
	wins, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-done
		if err == nil {
			wins++
		} else if errors.Is(err, errVisitNoteConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("shared key attached twice")
	}
}

type unavailableHistoryPool struct {
	database.Pool
	err error
}

func (p unavailableHistoryPool) Query(ctx context.Context, q string, args ...any) (database.Rows, error) {
	if strings.Contains(q, "FROM visit_notes") || strings.Contains(q, "FROM dbo.visit_notes") || strings.Contains(q, "FROM asset_notes") || strings.Contains(q, "FROM dbo.asset_notes") {
		return nil, p.err
	}
	return p.Pool.Query(ctx, q, args...)
}
func (p unavailableHistoryPool) Exec(context.Context, string, ...any) (database.Result, error) {
	return nil, p.err
}
func (p unavailableHistoryPool) Begin(context.Context) (database.Tx, error) { return nil, p.err }
func TestSQLServerVisitHistoryUnavailableRetainsDraftAndSubmissionKey(t *testing.T) {
	testVisitHistoryUnavailableRetainsDraftAndSubmissionKey(t, sqlServerTestDB(t))
}
func TestVisitHistoryUnavailableRetainsDraftAndSubmissionKey(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testVisitHistoryUnavailableRetainsDraftAndSubmissionKey(t, db)
}
func testVisitHistoryUnavailableRetainsDraftAndSubmissionKey(t *testing.T, db *DB) {
	ctx := context.Background()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)}}
	shop := createTestShop(t, ctx, db, "note-fallback-"+time.Now().Format("150405.000000000"))
	_, _, work := recurringFixture(t, db, s, shop, 5, false)
	seasons, err := s.ListVisitDues(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("private history dependency failure")
	server := &Server{store: &Store{db: &DB{Pool: unavailableHistoryPool{Pool: db.Pool, err: failure}}, clock: s.clock}, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	for _, target := range []struct{ kind, id string }{{"recurring", work[0].ID}, {"seasonal", seasons[0].ID}} {
		key := strings.Repeat("a", 32)
		form := url.Values{"note_key": {key}, "note_body": {"Keep <private> draft"}, "occurred_on": {"2034-09-09"}, "event_timezone": {"UTC"}}
		r := httptest.NewRequest("POST", "/test/"+target.id+"/history", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", target.id)
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: "owner", Email: "Owner"}))
		w := httptest.NewRecorder()
		server.handleInternalHistory(w, r, target.kind)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "Could not confirm the save") || !strings.Contains(w.Body.String(), "Keep &lt;private&gt; draft") || !strings.Contains(w.Body.String(), `value="`+key+`"`) || !strings.Contains(w.Body.String(), "Note history is temporarily unavailable") || strings.Contains(w.Body.String(), "No service notes yet") || strings.Contains(w.Body.String(), failure.Error()) || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("lost safe retry %d %s", w.Code, w.Body.String())
		}
	}
}

func TestSQLServerVisitNotesFollowPauseCleanupAndSurvivingWork(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)}}
	shop := createTestShop(t, ctx, db, "visit-note-pause")
	a, id, rows := recurringFixture(t, db, s, shop, 5, false)
	if err := s.MoveServiceVisit(ctx, shop, rows[1].ID, s.nowUTC().AddDate(0, 0, 1), 0); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveServiceVisit(ctx, shop, rows[3].ID, s.nowUTC().AddDate(0, 0, -1), 0); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateServiceOccurrence(ctx, shop, rows[4].ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateServiceOccurrence(ctx, shop, rows[5].ID, "skipped", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err := s.AddVisitNote(ctx, shop, "recurring", rows[i].ID, "Owner", internalNoteInput{Key: fmt.Sprintf("%032x", i), Body: "Keep with visit", Date: "2034-09-09", Zone: "UTC"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_note_pause ON dbo.service_occurrences AFTER DELETE AS BEGIN THROW 51000,'test deletion failure',1; END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetServiceScheduleActive(ctx, shop, id, false); err == nil {
		t.Fatal("failed pause saved")
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.visit_notes WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&count); err != nil || count != 6 {
		t.Fatal("rolled back pause lost history", err)
	}
	if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_note_pause`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetServiceScheduleActive(ctx, shop, id, false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		notes, err := s.ListVisitNotes(ctx, shop, "recurring", rows[i].ID)
		want := 1
		if i == 1 || i == 2 || i == 3 {
			want = 0
		}
		if err != nil || len(notes) != want {
			t.Fatal("pause note lifetime", i, len(notes), err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET status=N'cancelled' WHERE id=CONVERT(uniqueidentifier,@p1)`, a); err != nil {
		t.Fatal(err)
	}
	input := internalNoteInput{Key: strings.Repeat("d", 32), Body: "Paused inactive observation", Date: "2034-09-09", Zone: "UTC"}
	if err := s.AddVisitNote(ctx, shop, "recurring", rows[0].ID, "Owner", input); err != nil {
		t.Fatal("surviving paused/inactive note rejected", err)
	}
	input.Key = strings.Repeat("e", 32)
	if err := s.AddVisitNote(ctx, shop, "recurring", rows[1].ID, "Owner", input); !errors.Is(err, errVisitNoteConflict) {
		t.Fatal("deleted visit accepted note", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET status=N'active' WHERE id=CONVERT(uniqueidentifier,@p1)`, a); err != nil {
		t.Fatal(err)
	}
	if err := s.SetServiceScheduleActive(ctx, shop, id, true); err != nil {
		t.Fatal(err)
	}
	planned, err := s.ListServiceOccurrences(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range planned {
		if v.ID == rows[1].ID || v.ID == rows[2].ID || v.ID == rows[3].ID {
			t.Fatal("reset visit identity reused")
		}
		if v.DueOn.Equal(rows[2].OriginalDueOn) {
			notes, err := s.ListVisitNotes(ctx, shop, "recurring", v.ID)
			if err != nil || len(notes) != 0 {
				t.Fatal("replacement inherited history", err)
			}
		}
	}
}

func TestSQLServerVisitNotesDeleteAndIdentityRaces(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)}}
	for _, kind := range []string{"recurring", "seasonal"} {
		for _, change := range []string{"visit", "agreement", "shop", "identity"} {
			for _, noteFirst := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/note-first-%v", kind, change, noteFirst), func(t *testing.T) {
					shop := createTestShop(t, ctx, db, fmt.Sprintf("visit-note-race-%s-%s-%v", kind, change, noteFirst))
					agreement, _, work := recurringFixture(t, db, s, shop, 5, false)
					id, table, column := work[0].ID, "service_occurrences", "occurrence_id"
					if kind == "seasonal" {
						rows, err := s.ListVisitDues(ctx, shop, false)
						if err != nil {
							t.Fatal(err)
						}
						id, table, column = rows[0].ID, "visit_dues", "seasonal_visit_id"
					}
					query := `DELETE FROM dbo.` + table + ` WHERE id=CONVERT(uniqueidentifier,@p1)`
					arg := id
					switch change {
					case "agreement":
						query = `DELETE FROM dbo.agreements WHERE id=CONVERT(uniqueidentifier,@p1)`
						arg = agreement
					case "shop":
						query = `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`
						arg = shop
					case "identity":
						query = `UPDATE dbo.` + table + ` SET id=NEWID() WHERE id=CONVERT(uniqueidentifier,@p1)`
					}
					input := internalNoteInput{Key: strings.Repeat("a", 32), Body: "Concurrent note", Date: "2034-09-09", Zone: "UTC"}
					tx, err := db.Pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(ctx)
					var blocker int
					if err := tx.QueryRow(ctx, `SELECT @@SPID`).Scan(&blocker); err != nil {
						t.Fatal(err)
					}
					done := make(chan error, 1)
					if noteFirst {
						if _, err := tx.Exec(ctx, `INSERT INTO dbo.visit_notes(shop_id,`+column+`,submission_key,body,occurred_on,event_timezone,recorded_by) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),@p3,N'Concurrent note','2034-09-09',N'UTC',N'Owner')`, shop, id, input.Key); err != nil {
							t.Fatal(err)
						}
						go func() { _, err := db.Pool.Exec(ctx, query, arg); done <- err }()
					} else {
						if _, err := tx.Exec(ctx, query, arg); err != nil {
							t.Fatal(err)
						}
						go func() { done <- s.AddVisitNote(ctx, shop, kind, id, "Owner", input) }()
					}
					waitForSeasonalBlockedSessions(t, ctx, db, blocker, 1)
					if err := tx.Commit(ctx); err != nil {
						t.Fatal(err)
					}
					err = <-done
					if noteFirst && change != "identity" {
						if err != nil {
							t.Fatal("cascade failed", err)
						}
					} else if err == nil {
						t.Fatal("deleted/reidentified parent accepted history")
					}
					var orphans int
					if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.visit_notes n WHERE (n.occurrence_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dbo.service_occurrences o WHERE o.shop_id=n.shop_id AND o.id=n.occurrence_id)) OR (n.seasonal_visit_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dbo.visit_dues v WHERE v.shop_id=n.shop_id AND v.id=n.seasonal_visit_id))`).Scan(&orphans); err != nil || orphans != 0 {
						t.Fatal("orphaned history", err)
					}
				})
			}
		}
	}
}

func TestSQLServerVisitNotesDirectOwnershipAndReparenting(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)}}
	shop := createTestShop(t, ctx, db, "visit-note-parent")
	a, _, work := recurringFixture(t, db, s, shop, 5, false)
	b, schedule, _ := recurringFixture(t, db, s, shop, 5, false)
	other := createTestShop(t, ctx, db, "visit-note-parent-other")
	foreignAgreement, foreignSchedule, _ := recurringFixture(t, db, s, other, 5, false)
	seasons, err := s.ListVisitDues(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	seasonal := ""
	for _, v := range seasons {
		if v.AgreementID == a {
			seasonal = v.ID
		}
	}
	// Remove conflicting destination cadence/season keys before moving identities.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.service_occurrences WHERE schedule_id=CONVERT(uniqueidentifier,@p1); DELETE FROM dbo.visit_dues WHERE agreement_id=CONVERT(uniqueidentifier,@p2)`, schedule, b); err != nil {
		t.Fatal(err)
	}
	for i, target := range []struct{ kind, id, table, column, parentColumn, destination, foreign string }{{"recurring", work[0].ID, "service_occurrences", "occurrence_id", "schedule_id", schedule, foreignSchedule}, {"seasonal", seasonal, "visit_dues", "seasonal_visit_id", "agreement_id", b, foreignAgreement}} {
		input := internalNoteInput{Key: fmt.Sprintf("%032x", i), Body: "History follows visit", Date: "2034-09-09", Zone: "UTC"}
		if err := s.AddVisitNote(ctx, shop, target.kind, target.id, "Owner", input); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, `UPDATE dbo.`+target.table+` SET shop_id=CONVERT(uniqueidentifier,@p2),`+target.parentColumn+`=CONVERT(uniqueidentifier,@p3) WHERE id=CONVERT(uniqueidentifier,@p1)`, target.id, other, target.foreign); err == nil {
			t.Fatal("referenced visit moved tenant")
		}
		if _, err := db.Pool.Exec(ctx, `UPDATE dbo.`+target.table+` SET `+target.parentColumn+`=CONVERT(uniqueidentifier,@p2),id=id,shop_id=shop_id WHERE id=CONVERT(uniqueidentifier,@p1)`, target.id, target.destination); err != nil {
			t.Fatal("same tenant parent move rejected", err)
		}
		if notes, err := s.ListVisitNotes(ctx, shop, target.kind, target.id); err != nil || len(notes) != 1 {
			t.Fatal("reparent lost history", err)
		}
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.visit_notes(shop_id,`+target.column+`,submission_key,body,occurred_on,event_timezone,recorded_by) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p3),REPLICATE(N'c',32),N'Valid','2034-01-01',N'UTC',N'Owner'),(CONVERT(uniqueidentifier,@p2),CONVERT(uniqueidentifier,@p3),REPLICATE(N'd',32),N'Foreign','2034-01-01',N'UTC',N'Owner')`, shop, other, target.id); err == nil {
			t.Fatal("mixed ownership batch accepted")
		}
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.visit_notes WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&count); err != nil || count != 2 {
		t.Fatal("mixed batch partially committed", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.visit_notes SET shop_id=CONVERT(uniqueidentifier,@p1)`, other); err == nil {
		t.Fatal("direct notes moved tenant")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.visit_notes SET occurrence_id=NULL,seasonal_visit_id=NEWID() WHERE occurrence_id IS NOT NULL`); err == nil {
		t.Fatal("direct note orphan accepted")
	}
}
