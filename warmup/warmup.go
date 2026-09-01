// Package warmup contains Go kata exercises for interview prep.
// Read the implementation, understand it, then delete and rewrite from memory.
package warmup

import (
	"container/list"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// 1. Thread-Safe Counter
//   100 goroutines × 1000 increments = exactly 100,000
// ---------------------------------------------------------------------------

// Counter is a thread-safe monotonic counter.
type Counter struct {
	mu  sync.Mutex
	val int64
}

// Inc atomically increments the counter by one.
func (c *Counter) Inc() {
	c.mu.Lock()
	c.val++
	c.mu.Unlock()
}

// Val returns the current count.
func (c *Counter) Val() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.val
}

// AtomicCounter is an alternative using sync/atomic (no mutex).
// Faster for simple increment, but less flexible than mutex-guarded state.
type AtomicCounter struct {
	val atomic.Int64
}

func (c *AtomicCounter) Inc()       { c.val.Add(1) }
func (c *AtomicCounter) Val() int64 { return c.val.Load() }

// ---------------------------------------------------------------------------
// 2. Token-Bucket Rate Limiter
//   Allows max N requests per second. Concurrent-safe.
// ---------------------------------------------------------------------------

// RateLimiter implements the token-bucket algorithm.
//   - tokens refill at `rate` per second, capped at `burst`.
//   - Allow() consumes one token; returns true if a token was available.
type RateLimiter struct {
	rate   float64      // tokens per second
	burst  float64      // max tokens
	tokens atomic.Int64 // current tokens (stored as nanos to avoid float64 atomics — see fillBucket)

	mu       sync.Mutex
	lastFill time.Time
}

// NewRateLimiter creates a token-bucket limiter.
//
//	rate: tokens added per second (e.g. 10 → 10 req/s)
//	burst: max tokens that can accumulate
func NewRateLimiter(rate, burst float64) *RateLimiter {
	rl := &RateLimiter{rate: rate, burst: burst, lastFill: time.Now()}
	rl.tokens.Store(tokensAsNanos(burst)) // start full
	return rl
}

func tokensAsNanos(t float64) int64 { return int64(t * 1e9) }

// Allow returns true if a request can proceed right now.
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastFill).Seconds()
	rl.lastFill = now

	// Refill: add elapsed * rate, cap at burst.
	t := float64(rl.tokens.Load()) / 1e9
	t += elapsed * rl.rate
	if t > rl.burst {
		t = rl.burst
		rl.tokens.Store(tokensAsNanos(t))
	}

	if t < 1 {
		rl.tokens.Store(tokensAsNanos(t))
		return false
	}
	t--
	rl.tokens.Store(tokensAsNanos(t))
	return true
}

// ---------------------------------------------------------------------------
// 3. LRU Cache (generic, thread-safe)
//   Uses map + container/list for O(1) Get and Put.
// ---------------------------------------------------------------------------

// entry holds a key-value pair stored in the LRU list.
type entry[K comparable, V any] struct {
	key K
	val V
}

// LRU is a generic, thread-safe, fixed-size LRU cache.
type LRU[K comparable, V any] struct {
	mu      sync.Mutex
	cap     int
	items   map[K]*list.Element // key → list node
	recency *list.List          // front = most-recent, back = least-recent
}

// NewLRU creates an LRU cache that holds at most capacity items.
func NewLRU[K comparable, V any](capacity int) *LRU[K, V] {
	return &LRU[K, V]{
		cap:     capacity,
		items:   make(map[K]*list.Element),
		recency: list.New(),
	}
}

// Get returns the value and true if the key exists.
// Access moves the entry to the front (most-recent).
func (l *LRU[K, V]) Get(key K) (V, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	elem, ok := l.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	l.recency.MoveToFront(elem)
	return elem.Value.(*entry[K, V]).val, true
}

// Put inserts or updates the key. If the cache is full the least-recently-used
// entry is evicted.
func (l *LRU[K, V]) Put(key K, val V) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if elem, ok := l.items[key]; ok {
		elem.Value.(*entry[K, V]).val = val
		l.recency.MoveToFront(elem)
		return
	}

	if l.recency.Len() >= l.cap {
		oldest := l.recency.Back()
		if oldest != nil {
			l.recency.Remove(oldest)
			delete(l.items, oldest.Value.(*entry[K, V]).key)
		}
	}

	e := &entry[K, V]{key: key, val: val}
	elem := l.recency.PushFront(e)
	l.items[key] = elem
}

// Len returns the number of items currently in the cache.
func (l *LRU[K, V]) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recency.Len()
}

// ---------------------------------------------------------------------------
// 4. Fan-Out / Fan-In
//   N workers read from input, write results to a single output channel.
//   When input is exhausted, output channel is closed.
// ---------------------------------------------------------------------------

// FanOutFanIn distributes work from `in` across `workers` goroutines.
// Each worker calls `fn` and writes the result to the returned channel.
// When `in` is closed and all workers have finished, the output channel is closed.
func FanOutFanIn(in <-chan int, workers int, fn func(int) int) <-chan int {
	out := make(chan int)

	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for v := range in {
				out <- fn(v)
			}
		}()
	}

	// Close output when all workers are done.
	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

// Collect reads all values from a channel into a slice.
func Collect(ch <-chan int) []int {
	var result []int
	for v := range ch {
		result = append(result, v)
	}
	return result
}

// ---------------------------------------------------------------------------
// 5. Graceful Shutdown HTTP Server
//   - Listen on :8080 with a simple handler.
//   - On SIGTERM/SIGINT: stop accepting new requests, drain existing ones
//     with a 10-second deadline, then exit.
// ---------------------------------------------------------------------------

// RunGracefulServer starts an HTTP server and blocks until SIGINT/SIGTERM,
// then shuts down gracefully. Intended to be called from main.
//
// Example:
//
//	func main() {
//	    if err := warmup.RunGracefulServer(); err != nil {
//	        log.Fatal(err)
//	    }
//	}
func RunGracefulServer() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Start the server in a background goroutine.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for signal.
	<-ctx.Done()

	// Shutdown with a 10-second grace period.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	// Check if ListenAndServe returned an unexpected error.
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

// =============================================================================
// BONUS EXERCISES
// =============================================================================

// ---------------------------------------------------------------------------
// 6. Event Bus (Pub-Sub with Channels)
//   Multiple subscribers can subscribe to event types.
//   Publish is non-blocking (drop if subscriber buffer is full).
// ---------------------------------------------------------------------------

// Event is a generic event payload.
type Event struct {
	Type string
	Data any
}

// EventBus allows subscribers to receive events by type.
// Each subscriber gets its own buffered channel.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[string][]chan Event
}

// NewEventBus creates an EventBus.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[string][]chan Event),
	}
}

// Subscribe returns a channel that receives events of the given type.
// The caller should call Unsubscribe when done to avoid leaks.
func (eb *EventBus) Subscribe(eventType string, bufSize int) <-chan Event {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	ch := make(chan Event, bufSize)
	eb.subscribers[eventType] = append(eb.subscribers[eventType], ch)
	return ch
}

// Unsubscribe removes a subscriber channel.
func (eb *EventBus) Unsubscribe(eventType string, ch <-chan Event) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	subs := eb.subscribers[eventType]
	for i, sub := range subs {
		if sub == ch {
			eb.subscribers[eventType] = append(subs[:i], subs[i+1:]...)
			close(sub)
			return
		}
	}
}

// Publish sends an event to all subscribers of its type.
// Non-blocking: if a subscriber's buffer is full the event is dropped.
func (eb *EventBus) Publish(e Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for _, ch := range eb.subscribers[e.Type] {
		select {
		case ch <- e:
		default: // drop if buffer full
		}
	}
}

// ---------------------------------------------------------------------------
// 7. State Machine
//   Validates transitions between states. Enum-style with compile-time safety.
// ---------------------------------------------------------------------------

//go:generate stringer -type=MarketState
type MarketState int

const (
	StateOpen MarketState = iota
	StateLive
	StatePaused
	StateSettled
)

// StateMachine holds the current state and valid transition rules.
type StateMachine struct {
	mu    sync.Mutex
	state MarketState
	// validTransitions[from] = set of allowed 'to' states.
	transitions map[MarketState][]MarketState
}

// NewMarketStateMachine creates a state machine for prediction market lifecycle.
func NewMarketStateMachine() *StateMachine {
	return &StateMachine{
		state: StateOpen,
		transitions: map[MarketState][]MarketState{
			StateOpen:    {StateLive},
			StateLive:    {StatePaused, StateSettled},
			StatePaused:  {StateLive, StateSettled},
			StateSettled: {},
		},
	}
}

// Transition moves to toState if the transition is valid.
func (sm *StateMachine) Transition(to MarketState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for _, valid := range sm.transitions[sm.state] {
		if to == valid {
			sm.state = to
			return nil
		}
	}
	return fmt.Errorf("invalid transition: %v → %v", sm.state, to)
}

// State returns the current state.
func (sm *StateMachine) State() MarketState {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.state
}

// ---------------------------------------------------------------------------
// 8. Idempotent Transaction Processor
//   Same request ID → same result. Prevents double-execution.
// ---------------------------------------------------------------------------

// TxnResult holds the outcome of a processed transaction.
type TxnResult struct {
	Success bool
	Message string
}

// IdempotentProcessor ensures each transaction ID is executed at most once.
// Processed results are cached indefinitely (add TTL in production).
type IdempotentProcessor struct {
	mu       sync.Mutex
	results  map[string]TxnResult
	balances map[string]int64
}

// NewIdempotentProcessor creates a processor.
func NewIdempotentProcessor() *IdempotentProcessor {
	return &IdempotentProcessor{
		results:  make(map[string]TxnResult),
		balances: make(map[string]int64),
	}
}

// Transfer moves amount from one account to another.
// If txnID has been processed before, returns the cached result.
// Returns an error on insufficient funds — does NOT cache failed attempts
// (so retries with different amounts still work).
func (p *IdempotentProcessor) Transfer(txnID, from, to string, amount int64) (TxnResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if r, ok := p.results[txnID]; ok {
		return r, nil
	}

	if p.balances[from] < amount {
		return TxnResult{Success: false, Message: "insufficient funds"},
			fmt.Errorf("insufficient funds: %s has %d, needs %d", from, p.balances[from], amount)
	}

	p.balances[from] -= amount
	p.balances[to] += amount

	r := TxnResult{Success: true, Message: "transferred"}
	p.results[txnID] = r
	return r, nil
}

// Deposit adds funds to an account (not idempotent — uses unique txnID).
func (p *IdempotentProcessor) Deposit(txnID, account string, amount int64) (TxnResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if r, ok := p.results[txnID]; ok {
		return r, nil
	}

	p.balances[account] += amount
	r := TxnResult{Success: true, Message: "deposited"}
	p.results[txnID] = r
	return r, nil
}

// Balance returns the current balance of an account.
func (p *IdempotentProcessor) Balance(account string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.balances[account]
}

// ---------------------------------------------------------------------------
// 9. Debouncer
//   Call a function at most once per interval. Last call wins.
// ---------------------------------------------------------------------------

// Debouncer ensures a function is called at most once per `interval`.
// Rapid successive calls reset the timer. Useful for batching writes.
type Debouncer struct {
	mu       sync.Mutex
	interval time.Duration
	timer    *time.Timer
	fn       func()
}

// NewDebouncer creates a debouncer.
func NewDebouncer(interval time.Duration, fn func()) *Debouncer {
	return &Debouncer{
		interval: interval,
		fn:       fn,
	}
}

// Trigger schedules fn to run after interval. If Trigger is called again
// before interval elapses the previous timer is reset (last call wins).
func (d *Debouncer) Trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.interval, d.fn)
}

// ---------------------------------------------------------------------------
// 10. Semaphore (Weighted)
//   Limits concurrent access to a resource. Similar to errgroup's SetLimit.
// ---------------------------------------------------------------------------

// Semaphore limits concurrent operations to a fixed capacity.
// Acquire blocks until a slot is available.
type Semaphore struct {
	ch chan struct{}
}

// NewSemaphore creates a semaphore with the given capacity.
func NewSemaphore(n int) *Semaphore {
	return &Semaphore{ch: make(chan struct{}, n)}
}

// Acquire takes a slot. Blocks if none are available.
func (s *Semaphore) Acquire() {
	s.ch <- struct{}{}
}

// Release frees a slot.
func (s *Semaphore) Release() {
	<-s.ch
}

// AcquireCtx takes a slot or returns when ctx is done.
func (s *Semaphore) AcquireCtx(ctx context.Context) error {
	select {
	case s.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
