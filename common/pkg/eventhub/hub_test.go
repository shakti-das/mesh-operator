package eventhub

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

const unexpectedPanicFmt = "unexpected panic: %v"

func TestHubPublishMillionNonBlockingAndAllDelivered(t *testing.T) {
	t.Parallel()

	// Arrange
	const n = 1_000_000

	h := New[uint64]()
	out := make(chan uint64) // unbuffered: forwarder will block on send until we start draining
	h.Add("sub", out)

	published := make(chan struct{})

	// Act
	go func() {
		defer close(published)
		for i := 0; i < n; i++ {
			h.Publish(uint64(i))
		}
	}()

	// Assert (Publish should not block on subscriber consumption)
	select {
	case <-published:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatalf("Publish appeared to block: did not finish publishing %d events within timeout", n)
	}

	// Arrange (drain)
	var (
		count uint64
		sum   uint64
	)
	drained := make(chan struct{})

	// Act (drain all)
	go func() {
		defer close(drained)
		for i := 0; i < n; i++ {
			v := <-out
			count++
			sum += v
		}
	}()

	// Assert (all delivered)
	select {
	case <-drained:
		// ok
	case <-time.After(30 * time.Second):
		t.Fatalf("Timed out draining %d events from hub subscriber channel", n)
	}

	expectedCount := uint64(n)
	expectedSum := (expectedCount - 1) * expectedCount / 2
	if count != expectedCount {
		t.Fatalf("unexpected event count: got=%d want=%d", count, expectedCount)
	}
	if sum != expectedSum {
		t.Fatalf("unexpected sum: got=%d want=%d", sum, expectedSum)
	}

	// Arrange (Close shouldn’t deadlock)
	var wg sync.WaitGroup
	wg.Add(1)

	// Act
	go func() {
		defer wg.Done()
		h.Close()
	}()

	// Assert
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatalf("hub.Close() timed out")
	}
}

func TestHubPublishWithNoSubscribersDoesNotPanic(t *testing.T) {
	t.Parallel()

	// Arrange
	h := New[int]()

	// Act
	for i := 0; i < 1000; i++ {
		h.Publish(i)
	}

	// Assert
	// No panic is the assertion.
}

func TestHubAddReplaceRemoveAffectsDelivery(t *testing.T) {
	t.Parallel()

	// Arrange
	h := New[int]()
	out1 := make(chan int, 10)
	out2 := make(chan int, 10)

	h.Add("sub", out1)

	// Act
	h.Publish(1)

	// Assert
	select {
	case got := <-out1:
		if got != 1 {
			t.Fatalf("unexpected value on out1: got=%d want=1", got)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for first delivery")
	}

	// Arrange (replace)
	h.Add("sub", out2)

	// Act
	h.Publish(2)

	// Assert (new subscriber gets it)
	select {
	case got := <-out2:
		if got != 2 {
			t.Fatalf("unexpected value on out2: got=%d want=2", got)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for delivery to replacement subscriber")
	}

	// Assert (old subscriber does not get new events after replacement)
	select {
	case got := <-out1:
		t.Fatalf("unexpected delivery to old subscriber after replacement: got=%d", got)
	case <-time.After(50 * time.Millisecond):
		// ok
	}

	// Arrange (remove)
	h.Remove("sub")

	// Act
	h.Publish(3)

	// Assert
	select {
	case got := <-out2:
		t.Fatalf("unexpected delivery after Remove: got=%d", got)
	case <-time.After(50 * time.Millisecond):
		// ok
	}
}

func TestHubAddSameNameKeepsLenOne(t *testing.T) {
	t.Parallel()

	// Arrange
	h := New[int]()
	out1 := make(chan int, 1)
	out2 := make(chan int, 1)

	// Act
	h.Add("sub", out1)
	h.Add("sub", out2)

	// Assert
	if got, want := h.Len(), 1; got != want {
		t.Fatalf("unexpected len after replace Add: got=%d want=%d", got, want)
	}
}

func TestHubRemoveNonExistentDoesNotChangeLenOrPanic(t *testing.T) {
	t.Parallel()

	// Arrange
	h := New[int]()
	out := make(chan int, 1)
	h.Add("a", out)

	before := h.Len()

	// Act
	h.Remove("does-not-exist")

	// Assert
	after := h.Len()
	if before != after {
		t.Fatalf("len changed after removing non-existent subscriber: before=%d after=%d", before, after)
	}
}

func TestHubFanoutToMultipleSubscribersAllReceiveAll(t *testing.T) {
	t.Parallel()

	// Arrange
	const n = 10_000

	h := New[uint64]()
	out1 := make(chan uint64, 1024)
	out2 := make(chan uint64, 1024)
	h.Add("a", out1)
	h.Add("b", out2)

	// Act
	for i := 0; i < n; i++ {
		h.Publish(uint64(i))
	}

	// Assert
	drainAndCheck := func(ch <-chan uint64) (count uint64, sum uint64) {
		for i := 0; i < n; i++ {
			select {
			case v := <-ch:
				count++
				sum += v
			case <-time.After(5 * time.Second):
				t.Fatalf("timeout draining %d events", n)
			}
		}
		return count, sum
	}

	c1, s1 := drainAndCheck(out1)
	c2, s2 := drainAndCheck(out2)

	expectedCount := uint64(n)
	expectedSum := (expectedCount - 1) * expectedCount / 2
	if c1 != expectedCount || c2 != expectedCount {
		t.Fatalf("unexpected counts: out1=%d out2=%d want=%d", c1, c2, expectedCount)
	}
	if s1 != expectedSum || s2 != expectedSum {
		t.Fatalf("unexpected sums: out1=%d out2=%d want=%d", s1, s2, expectedSum)
	}
}

func TestHubOrderPreservedSingleSubscriber(t *testing.T) {
	t.Parallel()

	// Arrange
	const n = 100_000

	h := New[uint64]()
	out := make(chan uint64, 1024)
	h.Add("sub", out)

	// Act
	for i := 0; i < n; i++ {
		h.Publish(uint64(i))
	}

	// Assert
	for i := 0; i < n; i++ {
		select {
		case got := <-out:
			want := uint64(i)
			if got != want {
				t.Fatalf("out of order: got=%d want=%d at i=%d", got, want, i)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("timeout waiting for event %d/%d", i, n)
		}
	}
}

func TestHubRemoveStopsBlockedForwarderNoFurtherDelivery(t *testing.T) {
	t.Parallel()

	// Arrange
	h := New[int]()
	out := make(chan int) // unbuffered
	h.Add("sub", out)

	firstRead := make(chan struct{})
	go func() {
		_ = <-out
		close(firstRead)
	}()

	// Act
	h.Publish(1)
	<-firstRead // first send completed

	// Receiver is now gone; this publish will cause the forwarder goroutine to block on send.
	h.Publish(2)

	// Remove should stop the blocked forwarder.
	h.Remove("sub")

	// Assert (2 is NOT delivered even if we start receiving again)
	select {
	case got := <-out:
		t.Fatalf("unexpected delivery after Remove: got=%d", got)
	case <-time.After(150 * time.Millisecond):
		// ok
	}
}

func TestHubCloseRemovesSubscribersDoesNotCloseChannelsAndStopsDelivery(t *testing.T) {
	t.Parallel()

	// Arrange
	h := New[int]()
	out1 := make(chan int, 1)
	out2 := make(chan int, 1)
	h.Add("a", out1)
	h.Add("b", out2)

	// Act
	h.Close()

	// Assert (subscribers removed)
	if got, want := h.Len(), 0; got != want {
		t.Fatalf("unexpected len after Close: got=%d want=%d", got, want)
	}

	// Assert (channels are not closed by hub.Close): closing them ourselves should NOT panic.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("out1 was closed by hub.Close(), unexpected panic on close(out1): %v", r)
			}
		}()
		close(out1)
	}()
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("out2 was closed by hub.Close(), unexpected panic on close(out2): %v", r)
			}
		}()
		close(out2)
	}()

	// Assert (Publish after Close is safe)
	h.Publish(123)

	// Arrange (hub is reusable after Close)
	out3 := make(chan int, 1)

	// Act
	h.Add("c", out3)
	h.Publish(4)

	// Assert
	select {
	case got := <-out3:
		if got != 4 {
			t.Fatalf("unexpected value after reusing hub: got=%d want=4", got)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for delivery after reusing hub")
	}
}

func TestHubConcurrentRemoveAndCloseDoesNotPanic(t *testing.T) {
	t.Parallel()

	// Arrange
	const iterations = 200
	for i := 0; i < iterations; i++ {
		h := New[int]()
		out := make(chan int, 1)
		h.Add("sub", out)

		start := make(chan struct{})
		panicCh := make(chan any, 2)

		var wg sync.WaitGroup
		wg.Add(2)

		// Act
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCh <- r
				}
			}()
			<-start
			h.Remove("sub")
		}()
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCh <- r
				}
			}()
			<-start
			h.Close()
		}()
		close(start)
		wg.Wait()
		close(panicCh)

		// Assert
		for r := range panicCh {
			t.Fatalf(unexpectedPanicFmt, r)
		}
	}
}

func TestHubConcurrentOperationsDoesNotPanic(t *testing.T) {
	t.Parallel()

	// Arrange
	h := New[int]()
	panicCh := make(chan any, 1000)
	var wg sync.WaitGroup

	// Act
	const n = 200
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("sub-%d", i)

		wg.Add(3)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCh <- r
				}
			}()
			h.Add(name, make(chan int, 32))
		}()
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCh <- r
				}
			}()
			h.Remove(name)
		}()
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCh <- r
				}
			}()
			h.Publish(42)
		}()
	}
	wg.Wait()
	close(panicCh)

	// Assert
	for r := range panicCh {
		t.Fatalf(unexpectedPanicFmt, r)
	}
}

func TestHubMultipleConcurrentPublishersAllDelivered(t *testing.T) {
	t.Parallel()

	// Arrange
	const (
		publishers   = 10
		perPublisher = 1_000
		total        = publishers * perPublisher
	)

	h := New[int]()
	out := make(chan int, total)
	h.Add("sub", out)

	var wg sync.WaitGroup

	// Act
	for p := 0; p < publishers; p++ {
		wg.Add(1)
		base := p * perPublisher
		go func(base int) {
			defer wg.Done()
			for i := 0; i < perPublisher; i++ {
				h.Publish(base + i)
			}
		}(base)
	}
	wg.Wait()

	// Assert
	seen := make([]bool, total)
	deadline := time.After(5 * time.Second)
	for i := 0; i < total; i++ {
		select {
		case v := <-out:
			if v < 0 || v >= total {
				t.Fatalf("unexpected value: %d (expected 0..%d)", v, total-1)
			}
			seen[v] = true
		case <-deadline:
			t.Fatalf("timed out waiting to receive %d events", total)
		}
	}
	for i, ok := range seen {
		if !ok {
			t.Fatalf("missing event %d", i)
		}
	}
}

func TestHubZeroValuePublishDoesNotPanic(t *testing.T) {
	t.Parallel()

	// Arrange
	var h Hub[int]

	// Act
	h.Publish(1)

	// Assert
	// No panic is the assertion.
}

func TestHubZeroValueAddThenPublishDelivers(t *testing.T) {
	t.Parallel()

	// Arrange
	var h Hub[int]
	out := make(chan int, 1)

	// Act
	h.Add("sub", out)
	h.Publish(1)

	// Assert
	select {
	case got := <-out:
		if got != 1 {
			t.Fatalf("unexpected value: got=%d want=1", got)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for delivery")
	}
}

func TestHubAddNilChannelPanics(t *testing.T) {
	t.Parallel()

	// Arrange
	h := New[int]()

	// Act + Assert
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic when adding nil channel, got none")
		}
	}()
	h.Add("sub", nil)
}

func TestForwarderCompactionShrinksQueueBackingSlice(t *testing.T) {
	t.Parallel()

	// Arrange
	const n = 4096
	h := New[int]()
	out := make(chan int, n)
	h.Add("sub", out)

	// Act (publish then drain)
	for i := 0; i < n; i++ {
		h.Publish(i)
	}
	for i := 0; i < n; i++ {
		select {
		case <-out:
			// ok
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout draining %d events", n)
		}
	}

	// Assert (compaction ran at least once, shrinking queue length)
	f := h.subs["sub"]
	if f == nil {
		t.Fatalf("expected forwarder to exist")
	}
	f.mu.Lock()
	queueLen := len(f.queue)
	f.mu.Unlock()

	if queueLen > n/2 {
		t.Fatalf("expected queue backing slice to shrink to <= %d after compaction, got %d", n/2, queueLen)
	}
}

func TestHubSubscriberClosesChannelPublishDoesNotCrash(t *testing.T) {
	t.Parallel()

	// Arrange
	h := New[int]()
	out := make(chan int, 1)
	h.Add("sub", out)

	// Act (subscriber closes its channel; this is a misuse but should not crash the process)
	close(out)

	// Assert (Publish doesn't panic)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Publish panicked after subscriber closed channel: %v", r)
			}
		}()
		h.Publish(1)
	}()

	// Assert (subsequent publishes are also safe)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("second Publish panicked after subscriber closed channel: %v", r)
			}
		}()
		h.Publish(2)
	}()
}

func TestHubAddReplaceWhileOldForwarderBlockedDoesNotDeadlockAndStopsOld(t *testing.T) {
	t.Parallel()

	// Arrange
	h := New[int]()
	out1 := make(chan int) // unbuffered
	h.Add("sub", out1)

	// Ensure forwarder is active.
	firstRead := make(chan struct{})
	go func() {
		_ = <-out1
		close(firstRead)
	}()

	// Act (deliver first, then block on second send)
	h.Publish(1)
	select {
	case <-firstRead:
		// ok
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for first delivery")
	}
	h.Publish(2) // receiver is gone; forwarder will block on send

	// Act (replace while old forwarder is blocked)
	out2 := make(chan int, 2)
	added := make(chan struct{})
	go func() {
		defer close(added)
		h.Add("sub", out2)
	}()

	// Assert (replace does not deadlock)
	select {
	case <-added:
		// ok
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for Add to return while old forwarder is blocked")
	}

	// Act (publish after replacement)
	h.Publish(3)

	// Assert (new subscriber receives, old doesn't)
	select {
	case got := <-out2:
		if got != 3 {
			t.Fatalf("unexpected value on out2: got=%d want=3", got)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for delivery to replacement subscriber")
	}
	select {
	case got := <-out1:
		t.Fatalf("unexpected delivery to old subscriber after replacement: got=%d", got)
	case <-time.After(150 * time.Millisecond):
		// ok
	}
}

func TestHubHammerConcurrentAddRemoveClosePublishDoesNotPanicOrDeadlock(t *testing.T) {
	t.Parallel()

	// Arrange
	h := New[int]()
	panicCh := make(chan any, 1000)
	start := make(chan struct{})

	var wg sync.WaitGroup

	// Act
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCh <- r
				}
			}()
			<-start

			name := fmt.Sprintf("sub-hammer-%d", g)
			for i := 0; i < 200; i++ {
				switch i % 4 {
				case 0:
					h.Add(name, make(chan int, 8))
				case 1:
					h.Publish(i)
				case 2:
					h.Remove(name)
				case 3:
					h.Close()
				}
			}
		}(g)
	}
	close(start)

	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()

	// Assert
	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for concurrent hammer test to finish")
	}
	close(panicCh)
	for r := range panicCh {
		t.Fatalf(unexpectedPanicFmt, r)
	}
}
