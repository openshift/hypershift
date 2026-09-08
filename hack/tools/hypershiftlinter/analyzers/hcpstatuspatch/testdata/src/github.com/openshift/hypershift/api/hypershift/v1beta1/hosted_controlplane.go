package v1beta1

type HostedControlPlane struct {
	Status Status
}

type Status struct {
	Ready bool
}
