package controllers_api

import (
	"k8s.io/client-go/util/workqueue"
)

// Event represents a registry update event.
type Event int

const (
	EventAdd Event = iota
	EventUpdate
	EventDelete
)

const (
	UnknownEvent = "unknown"
)

// ObjectEnqueuer - an interface providing a limited access to a queue
// main use-case is to provide access to a queue from the components which don't own the queue, thus limiting the allowed operations.
type ObjectEnqueuer interface {
	// Enqueue - enqueue an item to a shared queue, honoring the rate-limiting mechanisms in place, if configured
	Enqueue(clusterOfEvent string, obj interface{}, event Event)
}

// QueueAccessor - an interface providing a full access to a queue
// main use case - queue access to the components that own the queue.
type QueueAccessor interface {
	ObjectEnqueuer

	// EnqueueNow - enqueue an item to a shared queue, bypassing the rate-limiting mechanisms in place, if configured
	EnqueueNow(clusterOfEvent string, obj interface{}, event Event)

	// GetQueue - get the actual queue object
	GetQueue() workqueue.RateLimitingInterface
}

type NamespaceFilter interface {
	IsNamespaceMopEnabledForObject(obj interface{}) bool
	Run(stopCh <-chan struct{}) bool
}

type MulticlusterController interface {
	Run(workerThreads int, stopCh <-chan struct{}) error

	// OnNewClusterAdded - multicluster controllers aren't responsible for tracking currently available clusters,
	// however they are informed of these whenever a new one is added.
	// OnNewClusterAdded is invoked every time a new cluster is discovered, *before* any controllers or informers started for it.
	// This allows controllers to wire necessary informers/handlers/indexers.
	// Reference to the cluster object must not be retained.
	OnNewClusterAdded(cluster Cluster) error

	// GetObjectEnqueuer - get an "enqueuer" for the given controller
	GetObjectEnqueuer() ObjectEnqueuer
}

// ProxyAccessController manages service discovery and SidecarConfig updates.
// It watches services across clusters and updates SidecarConfigs based on service events.
type ProxyAccessController interface {
	// Start initializes and starts the controller
	Start(stopCh <-chan struct{}) error
	// Stop shuts down the controller
	Stop()
	// OnNewClusterAdded registers event handlers for a newly added cluster
	OnNewClusterAdded(cluster Cluster) error
}
