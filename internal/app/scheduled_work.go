package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"
)

const (
	hostedDatabaseReadinessTimeout = 80 * time.Second
	hostedNotificationAttemptLimit = 20
	hostedNotificationAttemptTime  = 110 * time.Second
	hostedNotificationSendTimeout  = 10 * time.Second
)

var errInterestNotificationBatchRemaining = errors.New("interest notification batch limit reached")

type interestDrainResult struct {
	Accepted int
	Deferred int
}

type hostedWorkOperations struct {
	readiness   func(context.Context) error
	maintenance func(context.Context) error
	drain       func(context.Context) (interestDrainResult, error)
}

type boundedInterestSender struct {
	inner   interestNotificationSender
	timeout time.Duration
}

func (s boundedInterestSender) Send(ctx context.Context, notification interestNotification) (string, error) {
	send, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.inner.Send(send, notification)
}

func runScheduledWork(config Config) error {
	return runHostedWork(config, true)
}

func runInterestDelivery(config Config) error {
	return runHostedWork(config, false)
}

func runHostedWork(config Config, includeMaintenance bool) error {
	if config.Environment != EnvironmentStaging || config.DatabaseBackend != "azure_sql" || config.ScheduledWorkOwner != "host" {
		return errors.New("host-owned work currently requires the explicit staging Azure SQL runtime")
	}
	if !includeMaintenance && config.InterestNotifications == (InterestNotificationConfig{}) {
		return errors.New("host-owned work requires the complete interest notification configuration")
	}
	ctx, stop := databaseCommandContext()
	defer stop()
	db, err := openConfiguredDB(ctx, config)
	if err != nil {
		return err
	}
	defer db.Close()
	var sender interestNotificationSender
	if config.InterestNotifications != (InterestNotificationConfig{}) {
		sender = boundedInterestSender{inner: &resendInterestSender{
			client: &http.Client{Timeout: hostedNotificationSendTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
			config: config.InterestNotifications, environment: config.Environment,
		}, timeout: hostedNotificationSendTimeout}
	}
	err = executeHostedWork(ctx, config, db, sender, includeMaintenance)
	if errors.Is(err, errInterestNotificationBatchRemaining) {
		newInterestNotificationFileSignal(config.InterestWakeFile)()
		return processExitError{err: err, code: 75}
	}
	if errors.Is(err, errInterestNotificationDeferred) {
		newInterestNotificationFileSignal(config.InterestWakeFile)()
		return processExitError{err: err, code: 76}
	}
	return err
}

func executeHostedWork(ctx context.Context, config Config, db *DB, sender interestNotificationSender, includeMaintenance bool) error {
	runtime, ok := db.Pool.(interface{ CheckRuntime(context.Context) error })
	if !ok {
		return errors.New("host-owned work requires the checked Azure SQL runtime")
	}
	operations := hostedWorkOperations{
		readiness: func(readiness context.Context) error { return runtime.CheckRuntime(readiness) },
		maintenance: func(maintenance context.Context) error {
			return executeScheduledMaintenance(maintenance, config, db)
		},
	}
	if sender != nil {
		operations.drain = func(delivery context.Context) (interestDrainResult, error) {
			return drainDueInterest(delivery, databaseInterestQueue{db: db, sender: sender}, hostedNotificationAttemptLimit)
		}
	}
	result, err := executeHostedSequence(ctx, includeMaintenance, operations)
	log.Printf("host-owned work complete maintenance=%t notifications_accepted=%d notifications_deferred=%d", includeMaintenance, result.Accepted, result.Deferred)
	return err
}

func executeHostedSequence(ctx context.Context, includeMaintenance bool, operations hostedWorkOperations) (interestDrainResult, error) {
	readiness, cancel := context.WithTimeout(ctx, hostedDatabaseReadinessTimeout)
	err := operations.readiness(readiness)
	cancel()
	if err != nil {
		return interestDrainResult{}, errors.New("host-owned database readiness failed")
	}
	var failures []error
	if includeMaintenance {
		if err := operations.maintenance(ctx); err != nil {
			failures = append(failures, errors.New("scheduled maintenance failed"))
		}
	}
	if operations.drain == nil {
		log.Print("host-owned interest delivery unconfigured; durable alerts remain queued")
		return interestDrainResult{}, errors.Join(failures...)
	}
	result, err := operations.drain(ctx)
	if err != nil {
		failures = append(failures, err)
	}
	return result, errors.Join(failures...)
}

func drainDueInterest(ctx context.Context, queue interestNotificationQueue, limit int) (interestDrainResult, error) {
	var result interestDrainResult
	var providerDeferred bool
	for result.Accepted+result.Deferred < limit {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		attempt, cancel := context.WithTimeout(ctx, hostedNotificationAttemptTime)
		found, err := queue.Deliver(attempt, time.Now())
		cancel()
		if !found {
			if providerDeferred {
				return result, errInterestNotificationDeferred
			}
			return result, err
		}
		if err != nil {
			result.Deferred++
			if errors.Is(err, errInterestNotificationDeferred) {
				providerDeferred = true
				continue
			}
			return result, errors.New("interest notification delivery state is uncertain")
		}
		result.Accepted++
	}
	if providerDeferred {
		return result, errInterestNotificationDeferred
	}
	return result, errInterestNotificationBatchRemaining
}
