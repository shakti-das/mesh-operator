package eventhub

import "sync"

const (
	forwarderInitialQueueCapacity = 16

	// forwarderCompactMinHead is the minimum number of already-drained items
	// required before we consider compacting the queue backing array.
	forwarderCompactMinHead = 1024

	// forwarderCompactHeadToLenRatio is the ratio used to decide whether enough
	// of the slice has been drained to justify compaction.
	// Condition: head * ratio >= len(queue).
	forwarderCompactHeadToLenRatio = 2
)

// Hub is a fanout publisher for events of type T.
//
// Properties:
// - Publish is non-blocking with respect to subscribers (it only enqueues).
// - Events are delivered in-order per subscriber in the same order Publish was called.
// - Subscribers are identified by name; adding the same name replaces the previous subscriber.
//
// IMPORTANT: This provides effectively-unbounded buffering per subscriber. If a subscriber
// stops consuming, memory usage can grow without bound.
type Hub[T any] struct {
	mu   sync.Mutex
	subs map[string]*forwarder[T]
}

func New[T any]() *Hub[T] {
	return &Hub[T]{subs: make(map[string]*forwarder[T])}
}

// Len returns the current number of subscribers.
func (h *Hub[T]) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// Add registers a subscriber channel under name. If a subscriber with the same
// name already exists, it will be replaced and the previous forwarder will stop.
//
// Note: this does NOT close the subscriber channel.
func (h *Hub[T]) Add(name string, out chan<- T) {
	if out == nil {
		panic("eventhub: Add called with nil channel")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.subs == nil {
		h.subs = make(map[string]*forwarder[T])
	}

	if old := h.subs[name]; old != nil {
		old.Close()
	}
	h.subs[name] = newForwarder(name, out)
}

// Remove unregisters name and stops its forwarder.
// Note: this does NOT close the subscriber channel.
func (h *Hub[T]) Remove(name string) {
	h.mu.Lock()
	f := h.subs[name]
	delete(h.subs, name)
	h.mu.Unlock()

	if f != nil {
		f.Close()
	}
}

// Publish enqueues x for all current subscribers.
// Publish never blocks on subscriber consumption.
func (h *Hub[T]) Publish(x T) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Preserve publish order across concurrent publishers by holding the hub lock
	// while enqueuing. Enqueue is fast and non-blocking.
	for _, f := range h.subs {
		f.Enqueue(x)
	}
}

// Close stops all forwarders and removes all subscribers.
// Note: this does NOT close the subscriber channels.
func (h *Hub[T]) Close() {
	h.mu.Lock()
	subs := h.subs
	h.subs = make(map[string]*forwarder[T])
	h.mu.Unlock()

	for _, f := range subs {
		f.Close()
	}
}

// forwarder provides effectively-unbounded, non-blocking enqueue for publishers
// while delivering events in-order to a subscriber channel.
type forwarder[T any] struct {
	name string
	out  chan<- T
	done chan struct{}
	once sync.Once

	mu     sync.Mutex
	cond   *sync.Cond
	queue  []T
	head   int
	closed bool
}

func newForwarder[T any](name string, out chan<- T) *forwarder[T] {
	if out == nil {
		panic("eventhub: newForwarder called with nil channel")
	}

	f := &forwarder[T]{
		name:  name,
		out:   out,
		done:  make(chan struct{}),
		queue: make([]T, 0, forwarderInitialQueueCapacity),
	}
	f.cond = sync.NewCond(&f.mu)
	go f.run()
	return f
}

func (f *forwarder[T]) Enqueue(x T) {
	select {
	case <-f.done:
		return
	default:
	}

	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.queue = append(f.queue, x)
	f.cond.Signal()
	f.mu.Unlock()
}

func (f *forwarder[T]) Close() {
	f.mu.Lock()
	f.closed = true
	f.cond.Broadcast()
	f.mu.Unlock()

	// Unblock any forwarder goroutine stuck in a send.
	// This intentionally allows dropping remaining queued events on Close.
	f.once.Do(func() { close(f.done) })
}

func (f *forwarder[T]) run() {
	for {
		f.mu.Lock()
		for !f.closed && f.head >= len(f.queue) {
			f.cond.Wait()
		}
		if f.closed {
			f.mu.Unlock()
			return
		}
		x := f.queue[f.head]
		f.head++

		// Periodically compact to avoid retaining a large underlying array.
		if f.head > forwarderCompactMinHead && f.head*forwarderCompactHeadToLenRatio >= len(f.queue) {
			remaining := len(f.queue) - f.head
			newQueue := make([]T, remaining, remaining)
			copy(newQueue, f.queue[f.head:])
			f.queue = newQueue
			f.head = 0
		}
		f.mu.Unlock()

		// Preserve ordering. This may block if the subscriber is slow, but it
		// never blocks Publish/Enqueue.
		var panicked bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()

			select {
			case f.out <- x:
			case <-f.done:
				return
			}
		}()
		if panicked {
			// If the subscriber channel has been closed, a send will panic
			// ("send on closed channel"). Do not crash the process; stop this
			// forwarder and prevent future enqueues from accumulating.
			f.Close()
			return
		}
	}
}
