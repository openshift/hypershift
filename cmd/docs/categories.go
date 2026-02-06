package docs

// CategoryOrder defines the display order of categories
var CategoryOrder = []string{
	"Required",
	"Cluster Identity",
	"Release Configuration",
	"AWS Infrastructure",
	"Networking",
	"Proxy Configuration",
	"Node Pool Configuration",
	"Security & Encryption",
	"AWS Credentials & IAM",
	"Control Plane Configuration",
	"OLM Configuration",
	"Output & Execution",
	"Developer-Only",
	"Deprecated",
}

// FlagCategories maps flag names to their categories.
// Note: Required flags are auto-detected via cobra annotations (MarkFlagRequired),
// so they don't need to be listed here.
var FlagCategories = map[string]string{
	// Cluster Identity
	"namespace":          "Cluster Identity",
	"base-domain":        "Cluster Identity",
	"base-domain-prefix": "Cluster Identity",
	"infra-id":           "Cluster Identity",
	"annotations":        "Cluster Identity",
	"labels":             "Cluster Identity",

	// Release Configuration
	"release-image":                "Release Configuration",
	"release-stream":               "Release Configuration",
	"arch":                         "Release Configuration",
	"feature-set":                  "Release Configuration",
	"disable-cluster-capabilities": "Release Configuration",
	"enable-cluster-capabilities":  "Release Configuration",

	// AWS Infrastructure
	"region":                           "AWS Infrastructure",
	"zones":                            "AWS Infrastructure",
	"vpc-cidr":                         "AWS Infrastructure",
	"additional-tags":                  "AWS Infrastructure",
	"public-only":                      "AWS Infrastructure",
	"private-zones-in-cluster-account": "AWS Infrastructure",

	// Networking
	"network-type":          "Networking",
	"service-cidr":          "Networking",
	"cluster-cidr":          "Networking",
	"machine-cidr":          "Networking",
	"default-dual":          "Networking",
	"endpoint-access":       "Networking",
	"external-dns-domain":   "Networking",
	"kas-dns-name":          "Networking",
	"disable-multi-network": "Networking",
	"allocate-node-cidrs":   "Networking",

	// Proxy Configuration
	"enable-proxy":                    "Proxy Configuration",
	"enable-secure-proxy":             "Proxy Configuration",
	"proxy-vpc-endpoint-service-name": "Proxy Configuration",

	// Node Pool Configuration
	"node-pool-replicas":         "Node Pool Configuration",
	"instance-type":              "Node Pool Configuration",
	"root-volume-type":           "Node Pool Configuration",
	"root-volume-size":           "Node Pool Configuration",
	"root-volume-iops":           "Node Pool Configuration",
	"root-volume-kms-key":        "Node Pool Configuration",
	"auto-repair":                "Node Pool Configuration",
	"auto-node":                  "Node Pool Configuration",
	"node-upgrade-type":          "Node Pool Configuration",
	"node-drain-timeout":         "Node Pool Configuration",
	"node-volume-detach-timeout": "Node Pool Configuration",

	// Security & Encryption
	"fips":                             "Security & Encryption",
	"ssh-key":                          "Security & Encryption",
	"generate-ssh":                     "Security & Encryption",
	"kms-key-arn":                      "Security & Encryption",
	"additional-trust-bundle":          "Security & Encryption",
	"image-content-sources":            "Security & Encryption",
	"oidc-issuer-url":                  "Security & Encryption",
	"sa-token-issuer-private-key-path": "Security & Encryption",

	// AWS Credentials & IAM
	"role-arn":                  "AWS Credentials & IAM",
	"sts-creds":                 "AWS Credentials & IAM",
	"secret-creds":              "AWS Credentials & IAM",
	"use-rosa-managed-policies": "AWS Credentials & IAM",
	"shared-role":               "AWS Credentials & IAM",

	// Control Plane Configuration
	"control-plane-availability-policy": "Control Plane Configuration",
	"infra-availability-policy":         "Control Plane Configuration",
	"node-selector":                     "Control Plane Configuration",
	"pods-labels":                       "Control Plane Configuration",
	"toleration":                        "Control Plane Configuration",
	"etcd-storage-class":                "Control Plane Configuration",
	"etcd-storage-size":                 "Control Plane Configuration",

	// OLM Configuration
	"olm-catalog-placement":       "OLM Configuration",
	"olm-disable-default-sources": "OLM Configuration",

	// Output & Execution
	"render":           "Output & Execution",
	"render-into":      "Output & Execution",
	"render-sensitive": "Output & Execution",
	"wait":             "Output & Execution",
	"timeout":          "Output & Execution",
	"pausedUntil":      "Output & Execution",
	"version-check":    "Output & Execution",

	// Developer-Only
	"control-plane-operator-image": "Developer-Only",
	"infra-json":                   "Developer-Only",
	"iam-json":                     "Developer-Only",
	"single-nat-gateway":           "Developer-Only",
	"aws-creds":                    "Developer-Only",
	"vpc-owner-aws-creds":          "Developer-Only",

	// Deprecated
	"multi-arch": "Deprecated",
}

// GetCategory returns the category for a flag, defaulting to "Other" if not found
func GetCategory(flagName string) string {
	if cat, ok := FlagCategories[flagName]; ok {
		return cat
	}
	return "Other"
}
