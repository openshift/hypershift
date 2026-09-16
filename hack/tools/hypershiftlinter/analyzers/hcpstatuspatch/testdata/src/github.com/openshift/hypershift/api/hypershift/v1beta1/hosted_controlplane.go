package v1beta1

type HostedCluster struct {
	Status Status
}

type HostedControlPlane struct {
	Status Status
}

type Status struct {
	Ready bool
}
