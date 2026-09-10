package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type scriptedInterestQueue struct {
	results []struct {
		found bool
		err   error
	}
	calls int
}

func (q *scriptedInterestQueue) Deliver(ctx context.Context, _ time.Time) (bool, error) {
	if _, ok := ctx.Deadline(); !ok {
		return false, errors.New("delivery attempt was unbounded")
	}
	if q.calls >= len(q.results) {
		q.calls++
		return false, nil
	}
	result := q.results[q.calls]
	q.calls++
	return result.found, result.err
}

func (*scriptedInterestQueue) Next(context.Context) (*time.Time, error) {
	panic("one-shot drain must not poll for future work")
}

func TestHostedInterestDrainIsBoundedAndLeavesDeferredWork(t *testing.T) {
	queue := &scriptedInterestQueue{}
	for range hostedNotificationAttemptLimit + 5 {
		queue.results = append(queue.results, struct {
			found bool
			err   error
		}{found: true})
	}
	result, err := drainDueInterest(context.Background(), queue, hostedNotificationAttemptLimit)
	if !errors.Is(err, errInterestNotificationBatchRemaining) || result.Accepted != hostedNotificationAttemptLimit || result.Deferred != 0 || queue.calls != hostedNotificationAttemptLimit {
		t.Fatalf("bounded drain = %+v calls=%d err=%v", result, queue.calls, err)
	}
	if ExitCode(processExitError{err: err, code: 75}) != 75 || ExitCode(errors.New("ordinary")) != 1 {
		t.Fatal("operator command exit status mapping changed")
	}

	deferred := errInterestNotificationDeferred
	queue = &scriptedInterestQueue{results: []struct {
		found bool
		err   error
	}{{found: true}, {found: true, err: deferred}, {found: true}}}
	result, err = drainDueInterest(context.Background(), queue, hostedNotificationAttemptLimit)
	if !errors.Is(err, errInterestNotificationDeferred) || result.Accepted != 2 || result.Deferred != 1 || queue.calls != 4 {
		t.Fatalf("deferred drain = %+v calls=%d err=%v", result, queue.calls, err)
	}

	uncertain := errors.New("synthetic commit failure")
	queue = &scriptedInterestQueue{results: []struct {
		found bool
		err   error
	}{{found: true, err: uncertain}, {found: true}}}
	result, err = drainDueInterest(context.Background(), queue, hostedNotificationAttemptLimit)
	if err == nil || errors.Is(err, errInterestNotificationDeferred) || result.Accepted != 0 || result.Deferred != 1 || queue.calls != 1 {
		t.Fatalf("uncertain drain = %+v calls=%d err=%v", result, queue.calls, err)
	}
}

func TestHostedInterestDrainStopsOnCancellationAndDoesNotPollEmptyQueue(t *testing.T) {
	queue := &scriptedInterestQueue{}
	result, err := drainDueInterest(context.Background(), queue, hostedNotificationAttemptLimit)
	if err != nil || result != (interestDrainResult{}) || queue.calls != 1 {
		t.Fatalf("empty drain = %+v calls=%d err=%v", result, queue.calls, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	queue = &scriptedInterestQueue{}
	if _, err := drainDueInterest(cancelled, queue, hostedNotificationAttemptLimit); !errors.Is(err, context.Canceled) || queue.calls != 0 {
		t.Fatalf("cancelled drain calls=%d err=%v", queue.calls, err)
	}
}

func TestHostedSenderGetsItsOwnTenSecondBudget(t *testing.T) {
	var remaining time.Duration
	sender := boundedInterestSender{timeout: hostedNotificationSendTimeout, inner: interestSendFunc(func(ctx context.Context, _ interestNotification) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("send had no deadline")
		}
		remaining = time.Until(deadline)
		return "accepted", nil
	})}
	outer, cancel := context.WithTimeout(context.Background(), hostedNotificationAttemptTime)
	defer cancel()
	if _, err := sender.Send(outer, interestNotification{}); err != nil {
		t.Fatal(err)
	}
	if remaining < 9*time.Second || remaining > hostedNotificationSendTimeout {
		t.Fatalf("provider budget = %v", remaining)
	}
}

func TestHostWakeFileIsCreatedAfterCommitSignal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "interest.wake")
	newInterestNotificationFileSignal(path)()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() != 0 {
		t.Fatalf("wake file = %+v err=%v", info, err)
	}
}

func TestHostedCommandsRejectUnsafeOrUnconfiguredTargetsBeforeDatabaseAccess(t *testing.T) {
	mail := InterestNotificationConfig{APIKey: "private", From: "alerts@example.test", To: "owner@example.test"}
	for _, config := range []Config{
		{Environment: EnvironmentProduction, DatabaseBackend: "azure_sql", ScheduledWorkOwner: "host", InterestNotifications: mail},
		{Environment: EnvironmentStaging, DatabaseBackend: "postgres", ScheduledWorkOwner: "host", InterestNotifications: mail},
		{Environment: EnvironmentStaging, DatabaseBackend: "azure_sql", ScheduledWorkOwner: "embedded", InterestNotifications: mail},
		{Environment: EnvironmentStaging, DatabaseBackend: "azure_sql", ScheduledWorkOwner: "host"},
	} {
		if err := runHostedWork(config, false); err == nil {
			t.Fatal("unsafe hosted-work target was accepted")
		}
	}
}

func TestScheduledMaintenanceAllowsUnconfiguredInterestDelivery(t *testing.T) {
	config := Config{Environment: EnvironmentStaging, DatabaseBackend: "azure_sql", ScheduledWorkOwner: "host"}
	// Reaching database construction proves the command did not couple
	// maintenance eligibility to optional email configuration.
	if err := runHostedWork(config, true); err == nil || err.Error() == "host-owned work requires the complete interest notification configuration" {
		t.Fatalf("scheduled maintenance mail coupling = %v", err)
	}
}

func TestHostedSequenceOrdersReadinessAndKeepsMaintenanceAndDeliveryIndependent(t *testing.T) {
	var order []string
	operations := hostedWorkOperations{
		readiness: func(ctx context.Context) error {
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > hostedDatabaseReadinessTimeout {
				t.Fatal("readiness did not receive its bounded cold-start context")
			}
			order = append(order, "readiness")
			return nil
		},
		maintenance: func(context.Context) error {
			order = append(order, "maintenance")
			return errors.New("synthetic maintenance failure")
		},
		drain: func(context.Context) (interestDrainResult, error) {
			order = append(order, "delivery")
			return interestDrainResult{Accepted: 1}, nil
		},
	}
	result, err := executeHostedSequence(context.Background(), true, operations)
	if err == nil || result.Accepted != 1 || len(order) != 3 || order[0] != "readiness" || order[1] != "maintenance" || order[2] != "delivery" {
		t.Fatalf("hosted sequence order=%v result=%+v err=%v", order, result, err)
	}

	order = nil
	operations.readiness = func(context.Context) error { order = append(order, "readiness"); return errors.New("unavailable") }
	if _, err := executeHostedSequence(context.Background(), true, operations); err == nil || len(order) != 1 {
		t.Fatalf("failed readiness ran database work: order=%v err=%v", order, err)
	}

	order = nil
	operations.readiness = func(context.Context) error { order = append(order, "readiness"); return nil }
	operations.maintenance = func(context.Context) error { order = append(order, "maintenance"); return nil }
	operations.drain = nil
	if _, err := executeHostedSequence(context.Background(), true, operations); err != nil || len(order) != 2 {
		t.Fatalf("mail-free maintenance order=%v err=%v", order, err)
	}
}
