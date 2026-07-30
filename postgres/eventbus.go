package postgres

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	ultra "github.com/aleksclark/ultralogical"
)

// EventChannel is the Postgres NOTIFY channel used to wake event
// subscribers. The payload is "<session_id>:<seq>" and is only a wakeup
// hint; subscribers always read forward from their last delivered seq.
const EventChannel = "session_events"

// EventBus implements ultra.EventBus on Postgres: a catch-up read from the
// store combined with LISTEN/NOTIFY wakeups and a periodic poll fallback, so
// delivery stays correct even when notifications are dropped.
type EventBus struct {
	store    ultra.Store
	pool     *pgxpool.Pool
	log      *slog.Logger
	pollTick time.Duration

	mu   sync.Mutex
	subs map[ultra.SessionID]map[chan struct{}]struct{}

	cancel context.CancelFunc
	done   chan struct{}
}

// NewEventBus creates an EventBus. pollTick defaults to 2s.
func NewEventBus(store ultra.Store, pool *pgxpool.Pool, log *slog.Logger, pollTick time.Duration) *EventBus {
	if pollTick <= 0 {
		pollTick = 2 * time.Second
	}
	return &EventBus{
		store:    store,
		pool:     pool,
		log:      log,
		pollTick: pollTick,
		subs:     map[ultra.SessionID]map[chan struct{}]struct{}{},
	}
}

// Start begins the LISTEN loop. Subscribers work without it (poll fallback),
// just with higher latency.
func (b *EventBus) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.done = make(chan struct{})
	go b.listenLoop(ctx)
}

// Stop terminates the LISTEN loop.
func (b *EventBus) Stop() {
	if b.cancel == nil {
		return
	}
	b.cancel()
	<-b.done
}

// Subscribe implements ultra.EventBus.
func (b *EventBus) Subscribe(ctx context.Context, org ultra.OrgID, session ultra.SessionID, fromSeq int64) (<-chan ultra.Event, error) {
	wake := make(chan struct{}, 1)
	b.addWaiter(session, wake)

	out := make(chan ultra.Event, 64)
	go func() {
		defer b.removeWaiter(session, wake)
		defer close(out)
		next := fromSeq
		ticker := time.NewTicker(b.pollTick)
		defer ticker.Stop()
		events := b.store.Org(org).Events()
		for {
			// Drain everything currently visible.
			for {
				batch, err := events.Range(ctx, session, next, 256)
				if err != nil || len(batch) == 0 {
					break
				}
				for _, e := range batch {
					select {
					case out <- e:
						next = e.Seq
					case <-ctx.Done():
						return
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-wake:
			case <-ticker.C:
			}
		}
	}()
	return out, nil
}

func (b *EventBus) addWaiter(session ultra.SessionID, ch chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs[session] == nil {
		b.subs[session] = map[chan struct{}]struct{}{}
	}
	b.subs[session][ch] = struct{}{}
}

func (b *EventBus) removeWaiter(session ultra.SessionID, ch chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs[session], ch)
	if len(b.subs[session]) == 0 {
		delete(b.subs, session)
	}
}

func (b *EventBus) wakeSession(session ultra.SessionID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[session] {
		select {
		case ch <- struct{}{}:
		default: // already has a pending wakeup
		}
	}
}

// listenLoop holds a dedicated connection with LISTEN and dispatches
// wakeups. On connection failure it retries with backoff; subscribers keep
// making progress via the poll tick in the meantime.
func (b *EventBus) listenLoop(ctx context.Context) {
	defer close(b.done)
	for {
		if ctx.Err() != nil {
			return
		}
		if err := b.listenOnce(ctx); err != nil && ctx.Err() == nil {
			b.log.Warn("eventbus: listener error, reconnecting", "error", err)
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (b *EventBus) listenOnce(ctx context.Context) error {
	conn, err := b.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+EventChannel); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		// Payload is "<session_id>:<seq>"; only the session id matters.
		if id, _, ok := strings.Cut(n.Payload, ":"); ok {
			b.wakeSession(ultra.SessionID(id))
		}
	}
}
