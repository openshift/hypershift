package scanner

import "time"

const (
	TagInfraID      = "hypershift.openshift.io/infra-id"
	TagClusterName  = "hypershift.openshift.io/cluster-name"
	TagSource       = "hypershift.openshift.io/source"
	TagClusterOwned = "kubernetes.io/cluster/%s"
)

// Category classifies an infrastructure group.
type Category string

const (
	CategoryConfirmedOrphan Category = "CONFIRMED ORPHAN"
	CategoryProbableOrphan  Category = "PROBABLE ORPHAN"
	CategoryDeveloperOwned  Category = "DEVELOPER-OWNED"
	CategoryActive          Category = "ACTIVE"
	CategoryUnknown         Category = "UNKNOWN ORIGIN"
)

// ResourceType identifies the AWS resource kind.
type ResourceType string

const (
	ResVPC             ResourceType = "VPC"
	ResSubnet          ResourceType = "Subnet"
	ResNATGateway      ResourceType = "NAT Gateway"
	ResElasticIP       ResourceType = "Elastic IP"
	ResInternetGateway ResourceType = "Internet Gateway"
	ResRouteTable      ResourceType = "Route Table"
	ResSecurityGroup   ResourceType = "Security Group"
	ResVPCEndpoint     ResourceType = "VPC Endpoint"
	ResDHCPOptions     ResourceType = "DHCP Options"
	ResEC2Instance     ResourceType = "EC2 Instance"
	ResEBSVolume       ResourceType = "EBS Volume"
	ResIAMRole         ResourceType = "IAM Role"
	ResInstanceProfile ResourceType = "Instance Profile"
	ResOIDCProvider    ResourceType = "OIDC Provider"
	ResRoute53Zone     ResourceType = "Route53 Zone"
	ResS3Prefix        ResourceType = "S3 OIDC Prefix"
	ResELB             ResourceType = "ELB"
	ResELBv2           ResourceType = "ELBv2"
	ResEC2KeyPair      ResourceType = "EC2 Key Pair"
	ResCapacityReservation ResourceType = "Capacity Reservation"
)

// Resource represents a single AWS resource.
type Resource struct {
	Type        ResourceType
	ID          string // e.g. vpc-0abc123, nat-0abc123, role name
	ARN         string // full ARN if available
	Name        string // Name tag or display name
	Region      string
	InfraID     string // extracted or matched infra ID
	ClusterName string // from tag if available
	CreatedAt   time.Time
	Tags        map[string]string
	MonthlyCost float64 // estimated monthly cost (0 if free)
	Details     string  // extra info (e.g. IP address, volume size)
}

// InfraGroup groups resources belonging to the same HyperShift infra ID.
type InfraGroup struct {
	InfraID     string
	ClusterName string
	Owner       string   // extracted from cluster-name prefix or tags
	Category    Category
	Resources   []Resource
	CreatedAt   time.Time // oldest resource creation time
	MonthlyCost float64   // sum of resource costs
	HasVPC      bool
	HasHC       bool // has HostedCluster on mgmt cluster
}

// Inventory is the top-level result of a scan.
type Inventory struct {
	AccountID string
	Regions   []string
	ScannedAt time.Time
	ScanMode  string                 // "tags", "deep", "detect"
	Groups    map[string]*InfraGroup // keyed by infra ID
	Ungrouped []Resource             // resources that couldn't be matched to an infra ID
}

// NewInventory creates a new Inventory.
func NewInventory(accountID string, regions []string, scanMode string) *Inventory {
	return &Inventory{
		AccountID: accountID,
		Regions:   regions,
		ScannedAt: time.Now(),
		ScanMode:  scanMode,
		Groups:    make(map[string]*InfraGroup),
	}
}

// AddResource adds a resource to the inventory, grouping by infra ID.
func (inv *Inventory) AddResource(r Resource) {
	if r.InfraID == "" {
		inv.Ungrouped = append(inv.Ungrouped, r)
		return
	}
	g, ok := inv.Groups[r.InfraID]
	if !ok {
		g = &InfraGroup{
			InfraID:     r.InfraID,
			ClusterName: r.ClusterName,
		}
		inv.Groups[r.InfraID] = g
	}
	g.Resources = append(g.Resources, r)
	g.MonthlyCost += r.MonthlyCost
	if r.ClusterName != "" && g.ClusterName == "" {
		g.ClusterName = r.ClusterName
	}
	if !r.CreatedAt.IsZero() && (g.CreatedAt.IsZero() || r.CreatedAt.Before(g.CreatedAt)) {
		g.CreatedAt = r.CreatedAt
	}
	if r.Type == ResVPC {
		g.HasVPC = true
	}
}

// OrphanedGroups returns groups categorized as orphaned.
func (inv *Inventory) OrphanedGroups() []*InfraGroup {
	var result []*InfraGroup
	for _, g := range inv.Groups {
		if g.Category == CategoryConfirmedOrphan || g.Category == CategoryProbableOrphan {
			result = append(result, g)
		}
	}
	return result
}

// ActiveGroups returns groups categorized as active.
func (inv *Inventory) ActiveGroups() []*InfraGroup {
	var result []*InfraGroup
	for _, g := range inv.Groups {
		if g.Category == CategoryActive {
			result = append(result, g)
		}
	}
	return result
}

// RoleSuffixes lists known IAM role component suffixes for HyperShift clusters.
var RoleSuffixes = []string{
	"cloud-controller",
	"node-pool",
	"control-plane-operator",
	"cloud-network-config-controller",
	"openshift-ingress",
	"openshift-image-registry",
	"aws-ebs-csi-driver-controller",
	"worker-role",
}

// Cost estimates per resource type (monthly).
const (
	CostNATGateway = 32.0 // ~$0.045/hr
	CostElasticIP  = 3.65 // ~$0.005/hr when not attached
)
