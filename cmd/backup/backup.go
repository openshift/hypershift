package backup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/oadp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

type CreateOptions struct {
	// Required flags
	HCName      string
	HCNamespace string

	// Optional flags with defaults
	OADPNamespace            string
	StorageLocation          string
	TTL                      time.Duration
	SnapshotMoveData         bool
	DefaultVolumesToFsBackup bool
	Render                   bool
	IncludedResources        []string

	// Client context
	Log    logr.Logger
	Client client.Client
}

var (
	// Base resources common to all platforms
	baseResources = []string{
		"serviceaccounts", "roles", "rolebindings", "pods", "persistentvolumeclaims", "persistentvolumes", "configmaps",
		"priorityclasses", "poddisruptionbudgets", "hostedclusters.hypershift.openshift.io", "nodepools.hypershift.openshift.io",
		"secrets", "services", "deployments", "statefulsets",
		"hostedcontrolplanes.hypershift.openshift.io", "clusters.cluster.x-k8s.io",
		"machinedeployments.cluster.x-k8s.io", "machinesets.cluster.x-k8s.io", "machines.cluster.x-k8s.io",
		"routes.route.openshift.io", "clusterdeployments.hive.openshift.io",
	}

	// Platform-specific resources constants
	awsResources = []string{
		"awsclusters.infrastructure.cluster.x-k8s.io", "awsmachinetemplates.infrastructure.cluster.x-k8s.io", "awsmachines.infrastructure.cluster.x-k8s.io",
	}
	agentResources = []string{
		"agentclusters.infrastructure.cluster.x-k8s.io", "agentmachinetemplates.infrastructure.cluster.x-k8s.io", "agentmachines.infrastructure.cluster.x-k8s.io",
		"agents.agent-install.openshift.io", "infraenvs.agent-install.openshift.io", "baremetalhosts.metal3.io",
	}
	kubevirtResources = []string{
		"kubevirtclusters.infrastructure.cluster.x-k8s.io", "kubevirtmachinetemplates.infrastructure.cluster.x-k8s.io",
	}
	openstackResources = []string{
		"openstackclusters.infrastructure.cluster.x-k8s.io", "openstackmachinetemplates.infrastructure.cluster.x-k8s.io", "openstackmachines.infrastructure.cluster.x-k8s.io",
	}
	azureResources = []string{
		"azureclusters.infrastructure.cluster.x-k8s.io", "azuremachinetemplates.infrastructure.cluster.x-k8s.io", "azuremachines.infrastructure.cluster.x-k8s.io",
	}

	// Platform resource mapping
	platformResourceMap = map[string][]string{
		"AWS":       awsResources,
		"AGENT":     agentResources,
		"KUBEVIRT":  kubevirtResources,
		"OPENSTACK": openstackResources,
		"AZURE":     azureResources,
	}
)

func NewCreateCommand() *cobra.Command {
	opts := &CreateOptions{
		Log: log.Log,
	}

	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create a backup of a hosted cluster",
		Long: `Create a backup of a hosted cluster using OADP (OpenShift API for Data Protection).

The backup command automatically detects the platform type of your HostedCluster and includes
the appropriate platform-specific resources. It validates OADP installation and creates
comprehensive backups that can be used for disaster recovery scenarios.

Examples:
  # Basic backup with defaults
  hypershift create backup --hc-name production --hc-namespace clusters

  # Backup with custom settings
  hypershift create backup --hc-name production --hc-namespace clusters --storage-location s3-backup --ttl 24h

  # Backup with custom resource selection
  hypershift create backup --hc-name production --hc-namespace clusters --included-resources hostedcluster,nodepool,secrets,configmap

  # Render backup YAML without creating it
  hypershift create backup --hc-name production --hc-namespace clusters --render

  # Cross-region backup with data movement
  hypershift create backup --hc-name production --hc-namespace clusters --storage-location cross-region-s3 --snapshot-move-data=true --ttl 720h

  # Configuration-only backup (faster)
  hypershift create backup --hc-name dev-cluster --hc-namespace dev --included-resources hostedcluster,nodepool --ttl 24h

For detailed documentation and examples, visit:
https://hypershift.pages.dev/how-to/disaster-recovery/dr-cli/`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	// Required flags
	cmd.Flags().StringVar(&opts.HCName, "hc-name", "", "Name of the hosted cluster to backup (required)")
	cmd.Flags().StringVar(&opts.HCNamespace, "hc-namespace", "", "Namespace of the hosted cluster to backup (required)")

	// Optional flags with defaults
	cmd.Flags().StringVar(&opts.OADPNamespace, "oadp-namespace", "openshift-adp", "Namespace where OADP operator is installed")
	cmd.Flags().StringVar(&opts.StorageLocation, "storage-location", "default", "Storage location for the backup")
	cmd.Flags().DurationVar(&opts.TTL, "ttl", 2*time.Hour, "Time to live for the backup")
	cmd.Flags().BoolVar(&opts.SnapshotMoveData, "snapshot-move-data", true, "Enable snapshot move data feature")
	cmd.Flags().BoolVar(&opts.DefaultVolumesToFsBackup, "default-volumes-to-fs-backup", false, "Use filesystem backup for volumes by default")
	cmd.Flags().BoolVar(&opts.Render, "render", false, "Render the backup object to STDOUT instead of creating it")
	cmd.Flags().StringSliceVar(&opts.IncludedResources, "included-resources", nil, "Comma-separated list of resources to include in backup (overrides defaults)")

	// Mark required flags
	_ = cmd.MarkFlagRequired("hc-name")
	_ = cmd.MarkFlagRequired("hc-namespace")

	return cmd
}

func (o *CreateOptions) Run(ctx context.Context) error {
	// Client is needed for validations and actual creation
	if o.Client == nil {
		var err error
		o.Client, err = util.GetClient()
		if err != nil {
			if o.Render {
				// In render mode, if we can't connect to cluster, we'll still render but skip validations
				o.Log.Info("Warning: Cannot connect to cluster for validation, skipping all checks")
				// Step: Generate backup object with default platform (AWS)
				backup, _, err := o.generateBackupObjectWithPlatform("AWS")
				if err != nil {
					return fmt.Errorf("backup generation failed: %w", err)
				}
				return oadp.RenderVeleroResource(backup)
			}
			return fmt.Errorf("failed to create kubernetes client: %w", err)
		}
	}

	var platform string
	if o.Client != nil {
		// Step 1: Validate HostedCluster exists and get platform
		o.Log.Info("Validating HostedCluster...")
		detectedPlatform, err := oadp.ValidateAndGetHostedClusterPlatform(ctx, o.Client, o.HCName, o.HCNamespace)
		if err != nil {
			if o.Render {
				o.Log.Info("Warning: HostedCluster validation failed, using default platform (AWS)", "error", err.Error())
				platform = "AWS"
			} else {
				return fmt.Errorf("HostedCluster validation failed: %w", err)
			}
		} else {
			platform = detectedPlatform
		}

		if !o.Render {
			// Step 2: Validate OADP components (only in non-render mode)
			o.Log.Info("Validating OADP installation...")
			if err := oadp.ValidateOADPComponents(ctx, o.Client, o.OADPNamespace); err != nil {
				return fmt.Errorf("OADP validation failed: %w", err)
			}

			// Step 3: Verify DPA CR exists (only in non-render mode)
			o.Log.Info("Verifying DataProtectionApplication resource...")
			if err := oadp.VerifyDPAStatus(ctx, o.Client, o.OADPNamespace); err != nil {
				return fmt.Errorf("DPA verification failed: %w", err)
			}
		} else {
			// In render mode, run optional validations
			o.Log.Info("Validating OADP installation...")
			if err := oadp.ValidateOADPComponents(ctx, o.Client, o.OADPNamespace); err != nil {
				o.Log.Info("Warning: OADP validation failed, but continuing with render", "error", err.Error())
			} else {
				o.Log.Info("Verifying DataProtectionApplication resource...")
				if err := oadp.VerifyDPAStatus(ctx, o.Client, o.OADPNamespace); err != nil {
					o.Log.Info("Warning: DPA verification failed, but continuing with render", "error", err.Error())
				}
			}
		}
	} else {
		// This shouldn't happen but just in case
		platform = "AWS"
	}

	// Step 4: Generate backup object with detected platform
	backup, backupName, err := o.generateBackupObjectWithPlatform(platform)
	if err != nil {
		return fmt.Errorf("backup generation failed: %w", err)
	}

	if o.Render {
		// Render mode: output YAML to STDOUT
		return oadp.RenderVeleroResource(backup)
	} else {
		// Normal mode: create the backup
		o.Log.Info("Creating backup...")
		if err := o.Client.Create(ctx, backup); err != nil {
			return fmt.Errorf("failed to create backup resource: %w", err)
		}
		o.Log.Info("Backup created successfully", "name", backupName, "namespace", o.OADPNamespace, "platform", platform)
	}

	return nil
}

// randomStringGenerator is a function type for generating random strings
type randomStringGenerator func(int) string

// generateBackupName creates a backup name using the format: {hcName}-{hcNamespace}-{randomSuffix}
func generateBackupName(hcName, hcNamespace string, randomGen randomStringGenerator) string {
	randomSuffix := randomGen(6)
	return fmt.Sprintf("%s-%s-%s", hcName, hcNamespace, randomSuffix)
}

func (o *CreateOptions) generateBackupObjectWithPlatform(platform string) (*velerov1.Backup, string, error) {
	// Generate backup name with random suffix
	backupName := generateBackupName(o.HCName, o.HCNamespace, utilrand.String)

	// Determine which resources to include
	var includedResources []string
	if len(o.IncludedResources) > 0 {
		// Use custom resources provided by user
		includedResources = o.IncludedResources
	} else {
		// Use default resources based on platform
		includedResources = getDefaultResourcesForPlatform(platform)
	}

	// Create backup object using Velero API
	backup := &velerov1.Backup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "velero.io/v1",
			Kind:       "Backup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupName,
			Namespace: o.OADPNamespace,
			Labels: map[string]string{
				"velero.io/storage-location": o.StorageLocation,
			},
		},
		Spec: velerov1.BackupSpec{
			IncludedNamespaces: []string{
				o.HCNamespace,
				fmt.Sprintf("%s-%s", o.HCNamespace, o.HCName),
			},
			IncludedResources:        includedResources,
			StorageLocation:          o.StorageLocation,
			TTL:                      metav1.Duration{Duration: o.TTL},
			SnapshotMoveData:         &o.SnapshotMoveData,
			DefaultVolumesToFsBackup: &o.DefaultVolumesToFsBackup,
			DataMover:                "velero",
			SnapshotVolumes:          ptr.To(true),
		},
	}

	return backup, backupName, nil
}

// getDefaultResourcesForPlatform returns the default resource list based on the platform
func getDefaultResourcesForPlatform(platform string) []string {
	// Get platform-specific resources, default to AWS if platform is unknown
	platformResources, exists := platformResourceMap[strings.ToUpper(platform)]
	if !exists {
		platformResources = awsResources
	}

	// Combine base and platform-specific resources
	result := make([]string, len(baseResources)+len(platformResources))
	copy(result, baseResources)
	copy(result[len(baseResources):], platformResources)

	return result
}
