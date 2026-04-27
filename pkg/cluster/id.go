package cluster

// ID is the unique identifier for a k8s cluster.
type ID string

func (id ID) String() string {
	return string(id)
}
