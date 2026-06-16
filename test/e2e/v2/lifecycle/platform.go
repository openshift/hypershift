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
	// The releaseImage is the current release, n1Image is N-1, and
	// n3Image is N-3.
	ClusterSpecs(releaseImage, n1Image, n3Image string) []ClusterSpec

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

	// TestMatrix returns the test groups for this platform.
	// n1Image, n2Image, n3Image are the N-1, N-2, N-3 release images.
	TestMatrix(releaseImage, n1Image, n2Image, n3Image string) TestMatrix

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
	default:
		return nil, fmt.Errorf("unsupported platform %q (supported: azure)", platform)
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
