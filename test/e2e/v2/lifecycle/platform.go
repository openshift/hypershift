//go:build e2ev2

package lifecycle

import (
	"context"
	"crypto/sha256"
	"fmt"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterSpec describes a single cluster to create for lifecycle tests.
type ClusterSpec struct {
	Variant      string
	Suffix       string // hash suffix for name derivation
	OutputFile   string // filename under SHARED_DIR
	ExtraArgs    []string
	ReleaseImage string // override (empty = use default)
}

// TestGroup describes one logical group of e2e tests to execute.
type TestGroup struct {
	Name        string
	ClusterFile string // filename under SHARED_DIR containing cluster name
	LabelFilter string
	Skip        string
	JUnitFile   string
	ExtraEnv    []string
}

// SequentialGroup runs its Steps one after another within a single
// goroutine. If any step fails, subsequent steps are skipped.
type SequentialGroup struct {
	Name  string
	Steps []TestGroup
}

// TestMatrix defines the full set of test groups for a platform.
// Parallel groups all run concurrently. Each SequentialGroup also
// runs concurrently with everything else, but its internal Steps
// run one after another.
type TestMatrix struct {
	Parallel   []TestGroup
	Sequential []SequentialGroup
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

	// PostCreate runs platform-specific setup after clusters are
	// created (e.g., patching OperatorConfiguration).
	PostCreate(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error

	// TestMatrix returns the test groups for this platform.
	TestMatrix(releaseImage string) TestMatrix

	// SetupTestEnv sets platform-specific environment variables
	// before test execution (e.g., reading subnet IDs from
	// SHARED_DIR files).
	SetupTestEnv(sharedDir string)

	// DestroyArgs returns platform-specific args for
	// "hypershift destroy cluster <platform>".
	DestroyArgs() []string

	// Suffixes returns the hash suffixes for cluster name derivation,
	// matching the order of ClusterSpecs.
	Suffixes() []string
}

// NewPlatformConfig creates a PlatformConfig for the given platform
// name. The sharedDir is passed for platforms that read fallback
// config from files.
func NewPlatformConfig(platform, sharedDir string) (PlatformConfig, error) {
	switch platform {
	case "azure", "":
		return NewAzurePlatformConfig(sharedDir), nil
	default:
		return nil, fmt.Errorf("unsupported platform %q (supported: azure)", platform)
	}
}

// DeriveClusterName hashes prowJobID+suffix with SHA-256 and returns
// the first 20 hex characters.
func DeriveClusterName(prowJobID, suffix string) string {
	hash := sha256.Sum256([]byte(prowJobID + suffix))
	return fmt.Sprintf("%x", hash)[:20]
}

// DeriveClusterNames returns cluster names for the given suffixes.
func DeriveClusterNames(prowJobID string, suffixes []string) []string {
	names := make([]string, 0, len(suffixes))
	for _, suffix := range suffixes {
		names = append(names, DeriveClusterName(prowJobID, suffix))
	}
	return names
}
