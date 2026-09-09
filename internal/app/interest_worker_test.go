package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

type notificationQueueFuncs struct {
	deliver func(context.Context, time.Time) (bool, error)
	next    func(context.Context) (*time.Time, error)
}

func (q notificationQueueFuncs) Deliver(ctx context.Context, now time.Time) (bool, error) {
	return q.deliver(ctx, now)
}
func (q notificationQueueFuncs) Next(ctx context.Context) (*time.Time, error) { return q.next(ctx) }

func TestInterestWorkerSleepsWithoutPollingEmptyQueue(t *testing.T) {
	deliveries, lookups, waits := 0, 0, 0
	wake, _ := newInterestNotificationSignal()
	worker := interestNotificationWorker{
		wake: wake, now: time.Now,
		queue: notificationQueueFuncs{
			deliver: func(context.Context, time.Time) (bool, error) { deliveries++; return false, nil },
			next:    func(context.Context) (*time.Time, error) { lookups++; return nil, nil },
		},
		wait: func(ctx context.Context, ch <-chan struct{}, delay *time.Duration) bool {
			waits++
			if delay != nil {
				t.Fatalf("empty queue armed a polling timer: %s", *delay)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if waitForInterestNotification(cancelled, ch, delay) {
				t.Fatal("cancelled idle worker resumed")
			}
			return false
		},
	}
	worker.run(context.Background())
	if deliveries != 1 || lookups != 1 || waits != 1 {
		t.Fatalf("idle queries repeated: %d %d %d", deliveries, lookups, waits)
	}
}

func TestInterestWorkerPreservesWakeBetweenEmptyCheckAndWaiting(t *testing.T) {
	wake, notify := newInterestNotificationSignal()
	queued, delivered, lookups := false, 0, 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	worker := interestNotificationWorker{
		wake: wake, now: time.Now,
		queue: notificationQueueFuncs{
			deliver: func(context.Context, time.Time) (bool, error) {
				if queued {
					queued = false
					delivered++
					return true, nil
				}
				return false, nil
			},
			next: func(context.Context) (*time.Time, error) {
				lookups++
				if lookups == 1 {
					// The read saw no work; a transaction commits immediately after.
					queued = true
					notify()
					notify()
				}
				return nil, nil
			},
		},
		wait: func(ctx context.Context, ch <-chan struct{}, delay *time.Duration) bool {
			if delivered == 1 {
				return false
			}
			return waitForInterestNotification(ctx, ch, delay)
		},
	}
	worker.run(ctx)
	if delivered != 1 || lookups != 2 || ctx.Err() != nil {
		t.Fatalf("wake lost or duplicated: delivered=%d lookups=%d err=%v", delivered, lookups, ctx.Err())
	}
}

func TestInterestWorkerDrainsBurstBeyondBatchLimit(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	remaining, delivered, waits := 25, 0, 0
	worker := interestNotificationWorker{
		now: func() time.Time { return now },
		queue: notificationQueueFuncs{
			deliver: func(context.Context, time.Time) (bool, error) {
				if remaining == 0 {
					return false, nil
				}
				remaining--
				delivered++
				return true, nil
			},
			next: func(context.Context) (*time.Time, error) {
				if remaining == 0 {
					return nil, nil
				}
				return &now, nil
			},
		},
		wait: func(_ context.Context, _ <-chan struct{}, delay *time.Duration) bool {
			waits++
			if waits == 1 {
				if delivered != 20 || delay == nil || *delay != 15*time.Second {
					t.Fatal("batch remainder lost or spun without a delay")
				}
				now = now.Add(*delay)
				return true
			}
			if remaining != 0 || delay != nil {
				t.Fatal("drained queue kept polling")
			}
			return false
		},
	}
	worker.run(context.Background())
	if delivered != 25 || waits != 2 {
		t.Fatalf("burst not drained: %d %d", delivered, waits)
	}
}

func TestInterestWorkerRetainsKnownRetryAndBoundsLockContention(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		nextOffset, wantDelay time.Duration
	}{
		{"failed delivery", 5 * time.Minute, 5 * time.Minute},
		{"another worker owns due row", -time.Minute, 15 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			next := now.Add(tc.nextOffset)
			waits := 0
			worker := interestNotificationWorker{
				now: func() time.Time { return now },
				queue: notificationQueueFuncs{
					deliver: func(context.Context, time.Time) (bool, error) { return false, nil },
					next:    func(context.Context) (*time.Time, error) { return &next, nil },
				},
				wait: func(_ context.Context, _ <-chan struct{}, delay *time.Duration) bool {
					waits++
					if delay == nil || *delay != tc.wantDelay {
						t.Fatalf("retry delay %v", delay)
					}
					return false
				},
			}
			worker.run(context.Background())
			if waits != 1 {
				t.Fatal("contended row caused unbounded work")
			}
		})
	}
}

func TestInterestWorkerDoesNotTreatDatabaseOutageAsEmptyQueue(t *testing.T) {
	waits := 0
	worker := interestNotificationWorker{
		now: time.Now,
		queue: notificationQueueFuncs{
			deliver: func(context.Context, time.Time) (bool, error) { return false, errors.New("unavailable") },
			next:    func(context.Context) (*time.Time, error) { return nil, errors.New("unavailable") },
		},
		wait: func(_ context.Context, _ <-chan struct{}, delay *time.Duration) bool {
			waits++
			want := time.Minute * time.Duration(1<<(waits-1))
			if delay == nil || *delay != want {
				t.Fatalf("outage retry delay=%v want=%v", delay, want)
			}
			return waits < 3
		},
	}
	worker.run(context.Background())
	if waits != 3 {
		t.Fatal("unfinished work was abandoned")
	}
}

func TestInterestWakeInterruptsFutureRetry(t *testing.T) {
	wake, notify := newInterestNotificationSignal()
	notify()
	delay := time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !waitForInterestNotification(ctx, wake, &delay) || ctx.Err() != nil {
		t.Fatal("new signup waited for old retry timer")
	}
}
