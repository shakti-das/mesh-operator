package secretdiscovery

type fakeDynamicDiscovery struct {
	primaryCluster DynamicCluster
	remoteClusters []DynamicCluster
}

func NewFakeDynamicDiscovery(primaryCluster DynamicCluster, remoteClusters []DynamicCluster) Discovery {
	return &fakeDynamicDiscovery{
		primaryCluster: primaryCluster,
		remoteClusters: remoteClusters,
	}
}

func (f *fakeDynamicDiscovery) GetClusters() []DynamicCluster {
	return append([]DynamicCluster{f.primaryCluster}, f.remoteClusters...)
}

func (f *fakeDynamicDiscovery) GetPrimaryCluster() DynamicCluster {
	return f.primaryCluster
}

func (f *fakeDynamicDiscovery) GetRemoteClusters() []DynamicCluster {
	return f.remoteClusters
}

func NewFakeCluster(name string, isPrimary bool, client Client) DynamicCluster {
	return &clusterImpl{
		name:      name,
		isPrimary: isPrimary,
		client:    client,
	}
}
