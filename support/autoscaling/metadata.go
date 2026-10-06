// Package autoscaling defines the metadata shared by the control plane and NodePool
// controllers to describe nodes that do not exist yet.
package autoscaling

// AWSSubnetTopologyAnnotation records discovered worker topology on AWSMachineTemplate
// metadata. It must not be included in the template spec or NodePool rollout inputs.
const AWSSubnetTopologyAnnotation = "hypershift.openshift.io/aws-subnet-topology"

// AWSSubnetTopology describes a subnet in the hosted cluster's AWS account and region.
// Zone names are account-specific; ZoneID identifies the physical availability zone.
type AWSSubnetTopology struct {
	Region string `json:"region"`
	Zone   string `json:"zone"`
	ZoneID string `json:"zoneID"`
}
