package v1alpha4

type NetworkInterface struct {
	// Subnet ID of the network interface
	Subnet string `json:"subnet,omitempty"`
}

type Subnet struct {
	Ipv4CidrBlock *string `json:"cidr"`
	Name          *string `json:"name"`
	ID            *string `json:"id"`
	Zone          *string `json:"zone"`
}

type APIEndpoint struct {
	Address *string `json:"address"`
	FIPID   *string `json:"floatingIPID"`
}
