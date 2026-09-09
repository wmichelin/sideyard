package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

type notificationHTTPFunc func(*http.Request) (*http.Response, error)

func (f notificationHTTPFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestInterestNotificationConfigRequiresCompleteSingleRecipientConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, key, from, to string
		wantError           bool
	}{
		{"disabled", "", "", "", false},
		{"enabled", "private-key", "Sideyard <alerts@getsideyard.com>", "owner@example.test", false},
		{"missing key", "", "alerts@getsideyard.com", "owner@example.test", true},
		{"missing sender", "private-key", "", "owner@example.test", true},
		{"missing recipient", "private-key", "alerts@getsideyard.com", "", true},
		{"invalid recipient", "private-key", "alerts@getsideyard.com", "invalid", true},
		{"multiple recipients", "private-key", "alerts@getsideyard.com", "one@example.test,two@example.test", true},
		{"injected sender", "private-key", "alerts@getsideyard.com\r\nBcc: other@example.test", "owner@example.test", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{"RESEND_API_KEY": tc.key, "INTEREST_NOTIFY_FROM": tc.from, "INTEREST_NOTIFY_TO": tc.to}
			_, err := loadInterestNotificationConfig(func(k string) string { return values[k] })
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, want error %v", err, tc.wantError)
			}
			if err != nil && strings.Contains(err.Error(), "private-key") {
				t.Fatal("configuration error exposed credential")
			}
		})
	}
}

func TestInterestEmailUsesFixedRecipientPlainTextAndStableIdempotency(t *testing.T) {
	n := interestNotification{ID: "signup-id", interestSignup: interestSignup{
		Name: "Visitor\r\nBcc: unwanted@example.test", Email: "visitor@example.test",
		Business: "A & B", Trade: "Electrical", Message: "<script>not HTML</script>",
	}}
	var keys []string
	for _, env := range []Environment{EnvironmentProduction, EnvironmentProduction, EnvironmentStaging} {
		s := resendInterestSender{config: InterestNotificationConfig{APIKey: "private-key", From: "Sideyard <alerts@getsideyard.com>", To: "owner@example.test"}, environment: env}
		s.client = notificationHTTPFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != "POST" || r.URL.String() != "https://api.resend.com/emails" || r.Header.Get("Authorization") != "Bearer private-key" || r.UserAgent() != "Sideyard/1.0" {
				t.Fatal("wrong delivery endpoint or authentication")
			}
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			var body struct {
				From, Subject, Text, HTML string
				To                        []string
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.To) != 1 || body.To[0] != "owner@example.test" || body.From != s.config.From {
				t.Fatal("visitor influenced notification recipients or sender")
			}
			if body.HTML != "" || !strings.Contains(body.Text, n.Message) || !strings.Contains(body.Text, n.Email) || !strings.Contains(body.Text, n.Trade) {
				t.Fatal("notification lost signup details or rendered them as HTML")
			}
			wantSubject := "New Sideyard signup"
			if env == EnvironmentStaging {
				wantSubject = "[STAGING] " + wantSubject
			}
			if body.Subject != wantSubject {
				t.Fatal("subject was not fixed or environment-aware")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"provider-id"}`))}, nil
		})
		id, err := s.Send(context.Background(), n)
		if err != nil || id != "provider-id" {
			t.Fatalf("send: id=%q err=%v", id, err)
		}
	}
	if keys[0] == "" || keys[0] != keys[1] || keys[0] == keys[2] {
		t.Fatal("retry idempotency was not stable and environment-isolated")
	}
}

func TestInterestEmailFailuresRemainFailuresWithoutExposingProviderData(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		err    error
	}{
		{"network", 0, "", errors.New("private-key visitor@example.test")},
		{"unauthorized", 401, `{"message":"private-key visitor@example.test"}`, nil},
		{"throttled", 429, `{"message":"rate limited"}`, nil},
		{"unavailable", 503, "unavailable", nil},
		{"redirect", 302, "", nil},
		{"malformed", 200, "not JSON", nil},
		{"missing acknowledgement", 200, `{}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := resendInterestSender{client: notificationHTTPFunc(func(*http.Request) (*http.Response, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			_, err := s.Send(context.Background(), interestNotification{})
			if err == nil || strings.Contains(err.Error(), "private-key") || strings.Contains(err.Error(), "visitor@example.test") {
				t.Fatalf("unsafe failure: %v", err)
			}
		})
	}
}

type interestSendFunc func(context.Context, interestNotification) (string, error)

func (f interestSendFunc) Send(ctx context.Context, n interestNotification) (string, error) {
	return f(ctx, n)
}

func createNotificationTestSignup(t *testing.T, db *DB) string {
	t.Helper()
	email := "notify-" + time.Now().UTC().Format("20060102150405.000000000") + "@example.test"
	store := databaseInterestRecorder{db: db}
	in := interestSignup{Name: "Notification test", Email: email, Trade: "Plumbing"}
	for i := 0; i < 2; i++ {
		if err := store.SaveInterest(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), database.Statement(db.Pool.Dialect(), `DELETE FROM interest_signups WHERE email=$1`, `DELETE FROM dbo.interest_signups WHERE email=@p1`), email)
	})
	var id string
	var count int
	if err := db.Pool.QueryRow(context.Background(), database.Statement(db.Pool.Dialect(), `SELECT min(s.id::text), count(*)
		FROM interest_signups s JOIN interest_notifications n ON n.signup_id=s.id WHERE s.email=$1`, `SELECT MIN(LOWER(CONVERT(char(36),s.id))),COUNT(*) FROM dbo.interest_signups s JOIN dbo.interest_notifications n ON n.signup_id=s.id WHERE s.email=@p1`), email).Scan(&id, &count); err != nil || count != 1 {
		t.Fatalf("signup must enqueue exactly once: count=%d err=%v", count, err)
	}
	return id
}

func TestInterestNotificationPersistsRetryThenAcknowledgesOnlySuccess(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testInterestDeliveryRetry(t, db)
}

func testInterestDeliveryRetry(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	id := createNotificationTestSignup(t, db)
	now := time.Now().Add(time.Second)
	calls := 0
	sender := interestSendFunc(func(_ context.Context, n interestNotification) (string, error) {
		calls++
		if n.ID != id || n.Trade != "Plumbing" {
			t.Fatal("queued notification did not preserve signup")
		}
		if calls == 1 {
			return "", errors.New("temporary delivery failure")
		}
		return "accepted-message", nil
	})
	if found, err := deliverInterestNotification(ctx, db, sender, now); !found || err == nil {
		t.Fatalf("first send: found=%v err=%v", found, err)
	}
	var attempts int
	var pending bool
	if err := db.Pool.QueryRow(ctx, database.Statement(db.Pool.Dialect(), `SELECT attempts,sent_at IS NULL FROM interest_notifications WHERE signup_id=$1`, `SELECT attempts,CAST(CASE WHEN sent_at IS NULL THEN 1 ELSE 0 END AS bit) FROM dbo.interest_notifications WHERE signup_id=@p1`), id).Scan(&attempts, &pending); err != nil || attempts != 1 || !pending {
		t.Fatalf("failure lost pending notification: attempts=%d pending=%v err=%v", attempts, pending, err)
	}
	if found, err := deliverInterestNotification(ctx, db, sender, now.Add(30*time.Second)); found || err != nil || calls != 1 {
		t.Fatal("retried before backoff elapsed")
	}
	if found, err := deliverInterestNotification(ctx, db, sender, now.Add(2*time.Minute)); !found || err != nil || calls != 2 {
		t.Fatalf("retry did not succeed: found=%v calls=%d err=%v", found, calls, err)
	}
	if found, err := deliverInterestNotification(ctx, db, sender, now.Add(24*time.Hour)); found || err != nil || calls != 2 {
		t.Fatal("successful notification sent again")
	}
	var messageID string
	if err := db.Pool.QueryRow(ctx, database.Statement(db.Pool.Dialect(), `SELECT provider_message_id FROM interest_notifications WHERE signup_id=$1`, `SELECT provider_message_id FROM dbo.interest_notifications WHERE signup_id=@p1`), id).Scan(&messageID); err != nil || messageID != "accepted-message" {
		t.Fatal("provider acknowledgement not persisted")
	}
}

func TestInterestNotificationConcurrentWorkersDoNotSendTheSameSignup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testInterestConcurrentWorkers(t, db)
}

func testInterestConcurrentWorkers(t *testing.T, db *DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	createNotificationTestSignup(t, db)
	now := time.Now().Add(time.Second)
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	result := make(chan error, 1)
	go func() {
		_, err := deliverInterestNotification(ctx, db, interestSendFunc(func(context.Context, interestNotification) (string, error) {
			close(started)
			select {
			case <-release:
				return "accepted", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}), now)
		result <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first worker did not start")
	}
	called := false
	found, err := deliverInterestNotification(ctx, db, interestSendFunc(func(context.Context, interestNotification) (string, error) {
		called = true
		return "unexpected", nil
	}), now)
	unblock()
	firstErr := <-result
	if found || called || err != nil || firstErr != nil {
		t.Fatalf("concurrent duplicate: found=%v called=%v err=%v first=%v", found, called, err, firstErr)
	}
}
