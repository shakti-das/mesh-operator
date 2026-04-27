# `eventhub`

`eventhub` is a small generic **fanout** utility for Go: one publisher, many subscribers.

It is designed for cases where:

- **Publish must not block** on slow subscribers (publishers enqueue only)
- **Events must not be dropped** *while subscribed* (ordered delivery)
- Subscribers consume events at their own pace via a **channel**

## What it provides

- **`Hub[T]`**: manages a set of named subscribers and publishes events of type `T` to all of them.
- **Per-subscriber forwarding goroutine**: each subscriber gets its own internal queue + goroutine that drains into the subscriber channel.

## Guarantees / semantics

- **Non-blocking publish**: `Hub.Publish(x)` does not block on subscriber consumption. It only enqueues.
- **Per-subscriber ordering**: each subscriber receives events **in the same order** `Publish` was called.
- **Named subscribers**:
  - `Hub.Add(name, ch)` registers/overwrites subscriber `name`
  - `Hub.Remove(name)` unregisters subscriber `name`
  - `Hub.Len()` returns current subscriber count
- **Channels are not closed by the hub**:
  - `Hub.Remove` and `Hub.Close` do **not** close subscriber channels.
  - Channel ownership (close responsibility) stays with the caller.
- **Stop semantics (important)**:
  - `Remove`/`Close` stop the internal forwarder even if it’s blocked sending.
  - Any events still queued for that subscriber may be **dropped on Remove/Close**.

## Tradeoffs (read this)

This hub provides **effectively-unbounded buffering per subscriber**.

If a subscriber stops consuming (or consumes much slower than publishers produce), the in-memory queue for that subscriber can grow without bound and lead to **increased memory usage** and potentially **OOM**.

This is an intentional tradeoff to satisfy:

- *don’t block publishers* + *don’t drop while subscribed*

If you need bounded memory, you must introduce backpressure (block) or a drop/coalesce strategy.

## Example

```go
package main

import (
  "fmt"
  "time"

  "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/common/pkg/eventhub"
)

func main() {
  h := eventhub.New[string]()

  ch := make(chan string, 10)
  h.Add("logger", ch)

  go func() {
    for msg := range ch {
      fmt.Println("received:", msg)
    }
  }()

  h.Publish("hello")
  h.Publish("world")

  time.Sleep(50 * time.Millisecond)
  h.Remove("logger") // stops delivery to this subscriber (does not close ch)
}
```

## Tests

Unit tests live in `hub_test.go` and include:

- publish burst without blocking
- ordering checks
- fanout to multiple subscribers
- replace/remove/close semantics

