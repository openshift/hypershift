package main

import "time"

type Verdict string

const (
	VerdictProtected Verdict = "PROTECTED"
	VerdictTooYoung  Verdict = "TOO_YOUNG"
	VerdictActive    Verdict = "ACTIVE"
	VerdictLeaked    Verdict = "LEAKED"
	VerdictUncertain Verdict = "UNCERTAIN"
)

type InfraSet struct {
	InfraID       string
	VPCs          []VPCInfo
	Verdict       Verdict
	VerdictReason string

	HostedZones   []ZoneInfo
	IAMRoles      []string
	OIDCProviders []string
	Instances     int
	Age           time.Duration

	// Prow correlation
	TestType  string // "node-pool", "create-cluster", "control-plane-upgrade", etc.
	Namespace string // "e2e-clusters-XXXXX-<cluster>" from service.ci TXT records
	ProwLink  string // URL to Prow deck search for this test type
}

type VPCInfo struct {
	VPCID     string
	Name      string
	CIDR      string
	State     string
	CreatedAt time.Time

	// Protection tags
	DoNotDelete    bool
	CICluster      string
	ExpirationDate time.Time

	// Provenance tags (PR #8909: hypershift.openshift.io/*)
	Source      string // "cli", "e2e", or ""
	ClusterName string
	ProwJobID   string

	Endpoints        int
	EndpointServices int
	ENIs             int
	NATGateways      int
	IGWs             int
	Subnets          int
	SecurityGroups   int
	RouteTables      int
	ELBs             int
	EIPs             int
}

type ZoneInfo struct {
	ZoneID  string
	Name    string
	Private bool
	Records int
}

type Config struct {
	Region         string
	MinAge         time.Duration
	ProtectedVPCs  []string
	ProtectedUsers []string
	AllowedBuckets []string
	DryRun         bool
	Delete         bool
	Confirm        bool
	Interactive    bool
	Limit          int
	Target         string
	OutputFormat   string
	OutputDir      string
}

func DefaultConfig() Config {
	return Config{
		Region: "us-east-1",
		MinAge: 24 * time.Hour,
		ProtectedVPCs: []string{
			"hypershift-ci-2-vpc",
			"hypershift-ci-3-vpc",
			"hypershift-ci-metrics-vpc",
		},
		ProtectedUsers: []string{
			"aabdelre", "agarcial", "ahmed", "alamela", "alesross",
			"bclement", "brcox", "celebdor", "cewong",
			"dario", "dari2o", "dmace",
			"glipceanu",
			"jiezhao", "jparrill",
			"mbhalodi", "mbrown", "meha", "mgencur", "mulham", "mraee",
			"rkshirsa",
			"sdminonne", "sjenning",
			"tsegura",
			"vismishr",
		},
		AllowedBuckets: []string{
			"hypershift-ci-oidc",
			"hypershift-ci-2-oidc",
			"hypershift-ci-3-oidc",
		},
		OutputFormat: "table",
		OutputDir:    "output",
	}
}
