package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

type InterestNotificationConfig struct {
	APIKey, From, To string
}

func loadInterestNotificationConfig(getenv func(string) string) (InterestNotificationConfig, error) {
	c := InterestNotificationConfig{
		APIKey: strings.TrimSpace(getenv("RESEND_API_KEY")),
		From:   strings.TrimSpace(getenv("INTEREST_NOTIFY_FROM")),
		To:     strings.TrimSpace(getenv("INTEREST_NOTIFY_TO")),
	}
	if c == (InterestNotificationConfig{}) {
		return c, nil
	}
	if c.APIKey == "" || c.From == "" || c.To == "" {
		return c, errors.New("RESEND_API_KEY, INTEREST_NOTIFY_FROM, and INTEREST_NOTIFY_TO must be configured together")
	}
	for _, address := range []string{c.From, c.To} {
		if strings.ContainsAny(address, "\r\n") {
			return c, errors.New("interest notification addresses must be single email addresses")
		}
		if _, err := mail.ParseAddress(address); err != nil {
			return c, errors.New("interest notification addresses must be single email addresses")
		}
	}
	return c, nil
}

type interestNotification struct {
	ID string
	interestSignup
}

type interestNotificationSender interface {
	Send(context.Context, interestNotification) (string, error)
}

var errInterestNotificationDeferred = errors.New("interest notification delivery was deferred by the provider")

type notificationHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type resendInterestSender struct {
	client      notificationHTTPClient
	config      InterestNotificationConfig
	environment Environment
}

func (s *resendInterestSender) Send(ctx context.Context, n interestNotification) (string, error) {
	subject := "New Sideyard signup"
	if s.environment != EnvironmentProduction {
		subject = "[" + strings.ToUpper(string(s.environment)) + "] " + subject
	}
	// Send only to the configured owner. Submitted addresses never become recipients.
	// Plain text keeps visitor-provided content out of HTML and mail headers.
	body, err := json.Marshal(struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
	}{s.config.From, []string{s.config.To}, subject,
		fmt.Sprintf("Someone registered interest in Sideyard.\n\nName: %s\nEmail: %s\nBusiness: %s\nTrade: %s\n\nWhat would make their workday easier:\n%s\n\nSignup reference: %s\n", n.Name, n.Email, n.Business, n.Trade, n.Message, n.ID)})
	if err != nil {
		return "", errors.New("encode notification")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return "", errors.New("create notification request")
	}
	req.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Sideyard/1.0")
	req.Header.Set("Idempotency-Key", "sideyard-interest/"+string(s.environment)+"/"+n.ID)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", errors.New("notification service request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("notification service returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil || result.ID == "" {
		return "", errors.New("notification service returned no message ID")
	}
	return result.ID, nil
}

// The row lock spans the bounded send so current, candidate, and retained app
// containers cannot deliver the same queued signup concurrently. A crash rolls
// back the transaction; the stable provider idempotency key protects its retry.
func deliverInterestNotification(ctx context.Context, db *DB, sender interestNotificationSender, now time.Time) (bool, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return false, errors.New("open notification transaction")
	}
	defer tx.Rollback(ctx)
	var n interestNotification
	var attempts int
	err = tx.QueryRow(ctx, database.Statement(tx.Dialect(), `SELECT s.id::text, s.name, s.email, s.business, s.trade, s.message, n.attempts
		FROM interest_notifications n JOIN interest_signups s ON s.id=n.signup_id
		WHERE n.sent_at IS NULL AND n.next_attempt_at <= $1
		ORDER BY n.next_attempt_at, n.signup_id FOR UPDATE OF n SKIP LOCKED LIMIT 1`,
		`SELECT TOP (1) LOWER(CONVERT(char(36),s.id)),s.name,s.email,s.business,s.trade,s.message,n.attempts
		FROM dbo.interest_notifications n WITH (UPDLOCK,READPAST,READCOMMITTEDLOCK)
		JOIN dbo.interest_signups s ON s.id=n.signup_id
		WHERE n.sent_at IS NULL AND n.next_attempt_at<=@p1
		ORDER BY n.next_attempt_at,n.signup_id`), now).
		Scan(&n.ID, &n.Name, &n.Email, &n.Business, &n.Trade, &n.Message, &attempts)
	if errors.Is(err, database.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("read queued notification")
	}
	messageID, sendErr := sender.Send(ctx, n)
	if sendErr == nil {
		_, err = tx.Exec(ctx, database.Statement(tx.Dialect(), `UPDATE interest_notifications SET sent_at=$2,
			provider_message_id=$3, attempts=attempts+1 WHERE signup_id=$1`,
			`UPDATE dbo.interest_notifications SET sent_at=@p2,provider_message_id=@p3,attempts=attempts+1 WHERE signup_id=@p1`), n.ID, now, messageID)
	} else {
		_, err = tx.Exec(ctx, database.Statement(tx.Dialect(), `UPDATE interest_notifications SET attempts=attempts+1,
			next_attempt_at=$2 WHERE signup_id=$1`,
			`UPDATE dbo.interest_notifications SET attempts=attempts+1,next_attempt_at=@p2 WHERE signup_id=@p1`), n.ID, now.Add(interestNotificationBackoff(attempts)))
	}
	if err != nil {
		return true, errors.New("update notification delivery state")
	}
	if err := tx.Commit(ctx); err != nil {
		return true, errors.New("commit notification delivery state")
	}
	if sendErr != nil {
		return true, errInterestNotificationDeferred
	}
	return true, nil
}

func interestNotificationBackoff(attempts int) time.Duration {
	if attempts > 6 {
		attempts = 6
	}
	delay := time.Minute * time.Duration(1<<attempts)
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

// The channel coalesces bursts. Durable rows, startup recovery and the existing
// daily maintenance wake provide recovery if a process exits during handoff.
func newInterestNotificationSignal() (<-chan struct{}, func()) {
	wake := make(chan struct{}, 1)
	return wake, func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// The host watches this file and owns the short-lived delivery process. A
// missed filesystem wake is harmless because the durable queue is reconciled
// after boot, deployment and in the daily database window.
func newInterestNotificationFileSignal(path string) func() {
	return func() {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err == nil {
			err = file.Close()
		}
		if err != nil {
			log.Print("interest notification host wake failed; durable work remains queued")
		}
	}
}

type interestNotificationQueue interface {
	Deliver(context.Context, time.Time) (bool, error)
	Next(context.Context) (*time.Time, error)
}

type databaseInterestQueue struct {
	db     *DB
	sender interestNotificationSender
}

func (q databaseInterestQueue) Deliver(ctx context.Context, now time.Time) (bool, error) {
	return deliverInterestNotification(ctx, q.db, q.sender, now)
}
func (q databaseInterestQueue) Next(ctx context.Context) (*time.Time, error) {
	var next *time.Time
	err := q.db.Pool.QueryRow(ctx, `SELECT MIN(next_attempt_at) FROM interest_notifications WHERE sent_at IS NULL`).Scan(&next)
	return next, err
}

type interestNotificationWorker struct {
	queue interestNotificationQueue
	wake  <-chan struct{}
	now   func() time.Time
	wait  func(context.Context, <-chan struct{}, *time.Duration) bool
}

// The caller stops the worker after draining HTTP requests, so a final signup
// can commit and signal even after the process begins graceful shutdown.
func startInterestNotificationWorker(ctx context.Context, worker interestNotificationWorker) func() {
	workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() { defer close(done); worker.run(workerCtx) }()
	return func() { cancel(); <-done }
}

func (worker interestNotificationWorker) run(ctx context.Context) {
	consecutiveFailures := 0
	for {
		for i := 0; i < 20 && ctx.Err() == nil; i++ {
			attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			found, err := worker.queue.Deliver(attemptCtx, worker.now())
			cancel()
			if err != nil {
				// Keep recipient details, submitted content, and provider secrets out of logs.
				log.Print("interest notification delivery deferred; queued for retry")
				break
			}
			if !found {
				break
			}
			log.Print("interest notification accepted by email service")
		}
		if ctx.Err() != nil {
			return
		}
		lookup, cancel := context.WithTimeout(ctx, 15*time.Second)
		next, err := worker.queue.Next(lookup)
		cancel()
		var delay *time.Duration
		if err != nil {
			// A database outage is known unfinished work. Retry with a bounded
			// backoff; never interpret an unsuccessful lookup as an empty queue.
			backoff := interestNotificationBackoff(consecutiveFailures)
			if consecutiveFailures < 6 {
				consecutiveFailures++
			}
			delay = &backoff
		} else {
			consecutiveFailures = 0
			if next != nil {
				until := next.Sub(worker.now())
				// Another instance may hold a due row. Do not spin on that lock;
				// polling is bounded and occurs only while work remains queued.
				if until < 15*time.Second {
					until = 15 * time.Second
				}
				delay = &until
			}
		}
		// A nil delay means no timer and no database polling while idle.
		if !worker.wait(ctx, worker.wake, delay) {
			return
		}
	}
}

func waitForInterestNotification(ctx context.Context, wake <-chan struct{}, delay *time.Duration) bool {
	var timer *time.Timer
	var timeout <-chan time.Time
	if delay != nil {
		timer = time.NewTimer(*delay)
		timeout = timer.C
		defer timer.Stop()
	}
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return true
	case <-timeout:
		return true
	}
}
