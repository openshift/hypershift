package v1beta1

// Declare the types in this version as the Hub
func (*HostedCluster) Hub()      {}
func (*NodePool) Hub()           {}
func (*AWSEndpointService) Hub() {}
func (*HostedControlPlane) Hub() {}
