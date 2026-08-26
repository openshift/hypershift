//go:build e2ev2

package lifecycle

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterSpec describes a single cluster to create for lifecycle tests.
type ClusterSpec struct {
	Variant      string   `json:"variant"`
	ExtraArgs    []string `json:"extraArgs,omitempty"`
	ReleaseImage string   `json:"releaseImage,omitempty"` // override (empty = use default)
}

// TestGroup describes one logical group of e2e tests to execute.
type TestGroup struct {
	Name        string `json:"name"`
	Variant     string `json:"variant"`
	LabelFilter string `json:"labelFilter"`
	Skip        string `json:"skip,omitempty"`
}

// JUnitFile returns the deterministic JUnit XML filename for this
// test group, derived from the group name. It panics if the name
// contains path separators or traversal sequences; callers must
// validate the matrix before use.
func (g TestGroup) JUnitFile() string {
	if err := validateGroupName(g.Name); err != nil {
		panic(err.Error())
	}
	return fmt.Sprintf("junit_%s.xml", g.Name)
}

// validateGroupName checks that name is safe for use as a path
// component in JUnit filenames (no separators or traversal sequences).
func validateGroupName(name string) error {
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return fmt.Errorf("invalid path component in test group name: %q", name)
	}
	return nil
}

// SequentialGroup runs its Steps one after another within a single
// goroutine. If any step fails, subsequent steps are skipped.
type SequentialGroup struct {
	Name  string      `json:"name"`
	Steps []TestGroup `json:"steps"`
}

// TestMatrix defines the full set of test groups for a platform.
// Parallel groups all run concurrently. Each SequentialGroup also
// runs concurrently with everything else, but its internal Steps
// run one after another.
type TestMatrix struct {
	Parallel   []TestGroup       `json:"parallel,omitempty"`
	Sequential []SequentialGroup `json:"sequential,omitempty"`
}

// Validate checks that all group names within the matrix are unique
// and safe for use as JUnit filename components.
func (m TestMatrix) Validate() error {
	seen := make(map[string]bool)
	var errs []error
	check := func(name string) {
		if err := validateGroupName(name); err != nil {
			errs = append(errs, err)
		}
		if seen[name] {
			errs = append(errs, fmt.Errorf("duplicate test group name: %q", name))
		}
		seen[name] = true
	}
	for _, g := range m.Parallel {
		check(g.Name)
	}
	for _, sg := range m.Sequential {
		for _, step := range sg.Steps {
			check(step.Name)
		}
	}
	return errors.Join(errs...)
}

// Variants returns the unique cluster variants referenced by the
// matrix. No ordering is guaranteed.
func (m TestMatrix) Variants() []string {
	seen := make(map[string]bool)
	for _, g := range m.Parallel {
		seen[g.Variant] = true
	}
	for _, sg := range m.Sequential {
		for _, step := range sg.Steps {
			seen[step.Variant] = true
		}
	}
	variants := make([]string, 0, len(seen))
	for v := range seen {
		variants = append(variants, v)
	}
	return variants
}

// ResolveVariants validates that every variant referenced by the test
// matrix exists in the manifest and returns a map from variant to
// ClusterEntry. Returns an error listing all missing variants.
func (m TestMatrix) ResolveVariants(manifest *ClusterManifest) (map[string]ClusterEntry, error) {
	resolved := make(map[string]ClusterEntry)
	var missing []string

	resolve := func(variant string) {
		if _, ok := resolved[variant]; ok {
			return
		}
		entry, err := manifest.LookupCluster(variant)
		if err != nil {
			missing = append(missing, variant)
			return
		}
		resolved[variant] = entry
	}

	for _, g := range m.Parallel {
		resolve(g.Variant)
	}
	for _, sg := range m.Sequential {
		for _, step := range sg.Steps {
			resolve(step.Variant)
		}
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("manifest missing variants: %v", missing)
	}
	return resolved, nil
}

// PlatformConfig provides all platform-specific configuration for
// the v2 lifecycle binaries. Adding a new platform means implementing
// this interface — the cmd binaries should not need modification.
type PlatformConfig interface {
	// Name returns the CLI subcommand name (e.g., "azure", "aws").
	Name() string

	// DefaultBaseDomain returns the platform's default base domain.
	DefaultBaseDomain() string

	// ClusterSpecs returns the cluster variants this platform creates.
	// The releaseImage and n1Image are the current and N-1 release
	// images from the CI environment.
	ClusterSpecs(releaseImage, n1Image string) []ClusterSpec

	// CreateArgs returns platform-specific args for
	// "hypershift create cluster <platform>".
	CreateArgs() []string

	// PreCreate runs platform-specific setup before clusters are
	// created (e.g., deploying OIDC providers that must be ready
	// before the cluster exists).
	PreCreate(ctx context.Context, cl crclient.WithWatch, namespace string) error

	// PostCreate runs platform-specific setup after clusters are
	// created (e.g., patching OperatorConfiguration).
	PostCreate(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error

	// PostAvailable runs platform-specific operations after all
	// clusters reach the Available condition (e.g., waiting for
	// day-2 configuration transitions to complete). Control plane
	// components are guaranteed to exist at this point.
	PostAvailable(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error

	// PostVersionRollout runs day-2 operations after all clusters
	// reach VersionState=Completed. Use this for configuration changes
	// that disrupt ClusterOperators (e.g., External OIDC), which would
	// block the initial version rollout if applied earlier.
	PostVersionRollout(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error

	// DefaultTestPlan returns the full test plan for this platform,
	// selecting all variants returned by ClusterSpecs and the complete
	// test matrix using those variants.
	DefaultTestPlan() TestPlan

	// TestMatrix returns the test groups for this platform.
	TestMatrix() TestMatrix

	// SetupTestEnv sets platform-specific environment variables
	// before test execution (e.g., reading subnet IDs from
	// SHARED_DIR files).
	SetupTestEnv(sharedDir string)

	// DestroyArgs returns platform-specific args for
	// "hypershift destroy cluster <platform>".
	DestroyArgs() []string
}

// NewPlatformConfig creates a PlatformConfig for the given platform
// name. The sharedDir is passed for platforms that read fallback
// config from files.
func NewPlatformConfig(platform, sharedDir string) (PlatformConfig, error) {
	switch platform {
	case "azure", "":
		return NewAzurePlatformConfig(sharedDir), nil
	case "aws":
		return NewAWSPlatformConfig(AWSPlatformOptions{
			Region: envOrDefault("HYPERSHIFT_AWS_REGION", "us-east-1"),
			Zones:  envOrDefault("HYPERSHIFT_AWS_ZONES", "us-east-1a"),
		}, sharedDir), nil
	default:
		return nil, fmt.Errorf("unsupported platform %q (supported: azure, aws)", platform)
	}
}

// DeriveClusterName builds a human-readable, deterministic cluster name
// from the prow job ID and cluster variant. The format is
// "{variant}-{hash10}" where hash10 is the first 10 hex characters of
// SHA-256(prowJobID), giving uniqueness per CI run while keeping the
// variant visible in artifacts and namespaces.
func DeriveClusterName(prowJobID, variant string) string {
	hash := sha256.Sum256([]byte(prowJobID))
	return variant + "-" + fmt.Sprintf("%x", hash)[:10]
}
