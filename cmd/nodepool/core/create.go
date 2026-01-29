package core

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/supportedversion"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
	"github.com/spf13/cobra"
)

type CreateNodePoolOptions struct {
	Name            string
	Namespace       string
	ClusterName     string
	Replicas        int32
	ReleaseImage    string
	Render          bool
	NodeUpgradeType hyperv1.UpgradeType
	Arch            string
	AutoRepair      bool
}

type PlatformOptions interface {
	// UpdateNodePool is used to update the platform specific values in the NodePool
	UpdateNodePool(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, client crclient.Client) error
	// Type returns the platform type
	Type() hyperv1.PlatformType
}

// NodePoolPlatformValidator knows how to validate platform-specific nodepool options.
// All nodepool platform implementations SHOULD use this exact signature for their Validate() method
// to enable interface-based programming and easier maintenance across providers.
// Note: This interface is optional for simple platforms that don't need validation/completion phases.
type NodePoolPlatformValidator interface {
	// Validate allows the platform-specific logic to validate nodepool inputs.
	// Returns a NodePoolPlatformCompleter on success, or an error if validation fails.
	Validate(context.Context, *CreateNodePoolOptions) (NodePoolPlatformCompleter, error)
}

// NodePoolPlatformCompleter knows how to complete platform-specific nodepool configuration.
// All nodepool platform implementations SHOULD use this exact signature for their Complete() method
// to enable interface-based programming and easier maintenance across providers.
// Note: This interface is optional for simple platforms that don't need validation/completion phases.
type NodePoolPlatformCompleter interface {
	// Complete allows the platform-specific logic to default values and finalize configuration.
	// Returns a PlatformOptions implementation on success, or an error if completion fails.
	Complete(context.Context, *CreateNodePoolOptions) (PlatformOptions, error)
}

func (o *CreateNodePoolOptions) CreateRunFunc(platformOpts PlatformOptions) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if err := o.CreateNodePool(cmd.Context(), platformOpts); err != nil {
			log.Log.Error(err, "Failed to create nodepool")
			return err
		}
		return nil
	}
}

func (o *CreateNodePoolOptions) Validate(ctx context.Context, c crclient.Client) error {
	// Validate HostedCluster payload can support the NodePool CPU type
	if err := validateHostedClusterPayloadSupportsNodePoolCPUArch(ctx, c, o.ClusterName, o.Namespace, o.Arch); err != nil {
		return err
	}

	if err := validMinorVersionCompatibility(ctx, c, o.ClusterName, o.Namespace, o.ReleaseImage, &releaseinfo.RegistryClientProvider{}); err != nil {
		return err
	}

	return nil
}

func (o *CreateNodePoolOptions) CreateNodePool(ctx context.Context, platformOpts PlatformOptions) error {
	client, err := util.GetClient()
	if err != nil {
		return err
	}

	if err = o.Validate(ctx, client); err != nil {
		return err
	}

	hcluster := &hyperv1.HostedCluster{}
	err = client.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.ClusterName}, hcluster)
	if err != nil {
		return fmt.Errorf("failed to get HostedCluster %s/%s: %w", o.Namespace, o.ClusterName, err)
	}

	if platformOpts.Type() != hcluster.Spec.Platform.Type {
		return fmt.Errorf("NodePool platform type %s must be HostedCluster type %s", platformOpts.Type(), hcluster.Spec.Platform.Type)
	}

	nodePool := &hyperv1.NodePool{}
	err = client.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, nodePool)
	if err == nil && !o.Render {
		return fmt.Errorf("NodePool %s/%s already exists", o.Namespace, o.Name)
	}

	var releaseImage string
	if len(o.ReleaseImage) > 0 {
		releaseImage = o.ReleaseImage
	} else {
		releaseImage = hcluster.Spec.Release.Image
	}

	// Set default upgrade type when the flag is empty
	if o.NodeUpgradeType == "" {
		switch hcluster.Spec.Platform.Type {
		case hyperv1.AWSPlatform:
			o.NodeUpgradeType = hyperv1.UpgradeTypeReplace
		case hyperv1.KubevirtPlatform:
			o.NodeUpgradeType = hyperv1.UpgradeTypeReplace
		case hyperv1.NonePlatform:
			o.NodeUpgradeType = hyperv1.UpgradeTypeInPlace
		case hyperv1.AgentPlatform:
			o.NodeUpgradeType = hyperv1.UpgradeTypeInPlace
		case hyperv1.AzurePlatform:
			o.NodeUpgradeType = hyperv1.UpgradeTypeReplace
		case hyperv1.PowerVSPlatform:
			o.NodeUpgradeType = hyperv1.UpgradeTypeReplace
		case hyperv1.OpenStackPlatform:
			o.NodeUpgradeType = hyperv1.UpgradeTypeReplace
		default:
			panic("Unsupported platform")
		}
	}

	nodePool = &hyperv1.NodePool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodePool",
			APIVersion: hyperv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace,
			Name:      o.Name,
		},
		Spec: hyperv1.NodePoolSpec{
			Management: hyperv1.NodePoolManagement{
				UpgradeType: o.NodeUpgradeType,
				AutoRepair:  o.AutoRepair,
			},
			ClusterName: o.ClusterName,
			Replicas:    &o.Replicas,
			Release: hyperv1.Release{
				Image: releaseImage,
			},
			Platform: hyperv1.NodePoolPlatform{
				Type: hcluster.Spec.Platform.Type,
			},
			Arch: o.Arch,
		},
	}

	if err := platformOpts.UpdateNodePool(ctx, nodePool, hcluster, client); err != nil {
		return err
	}

	if o.Render {
		err := hyperapi.YamlSerializer.Encode(nodePool, os.Stdout)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(os.Stderr, "NodePool %s was rendered to yaml output file\n", o.Name)
		return nil
	}

	err = client.Create(ctx, nodePool)
	if err != nil {
		return err
	}

	fmt.Printf("NodePool %s created\n", o.Name)
	return nil
}

// validateHostedClusterPayloadSupportsNodePoolCPUArch validates the HostedCluster payload type can support the CPU architecture
// of the NodePool.
func validateHostedClusterPayloadSupportsNodePoolCPUArch(ctx context.Context, client crclient.Client, name, namespace, arch string) error {
	logger := ctrl.LoggerFrom(ctx)

	hc := &hyperv1.HostedCluster{}
	err := client.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, hc)
	if err != nil {
		// This is expected to happen when we create a cluster since there is no created HostedCluster CR to check the
		// payload from.
		logger.Info("WARNING: failed to get HostedCluster to check payload type")
		return nil
	}

	if hc.Status.PayloadArch == "" {
		logger.Info("WARNING: Unable to validate NodePool CPU arch: HostedCluster.Status.PayloadArch unspecified - skipping validation for this NodePool")
	}

	if hc.Status.PayloadArch != "" && hc.Status.PayloadArch != hyperv1.Multi && hc.Status.PayloadArch != hyperv1.ToPayloadArch(arch) {
		return fmt.Errorf("NodePool CPU arch, %s, is not supported by the HostedCluster payload type, %s", arch, hc.Status.PayloadArch)
	}

	return nil
}

// validMinorVersionCompatibility validates that the NodePool version is compatible with the HostedCluster version.
// All 4.y versions now support n-3 version skew.
// NodePool version cannot be higher than control plane version.
func validMinorVersionCompatibility(ctx context.Context, client crclient.Client, name, namespace, nodePoolReleaseImage string, releaseProvider releaseinfo.Provider) error {
	if nodePoolReleaseImage == "" {
		return nil
	}
	logger := ctrl.LoggerFrom(ctx)

	hcluster := &hyperv1.HostedCluster{}
	if err := client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, hcluster); err != nil {
		if !apierrors.IsNotFound(err) {
			// For other errors (e.g. API server issues, RBAC problems), we should return the error
			return fmt.Errorf("failed to get HostedCluster to check version compatibility: %w", err)
		}

		// This is expected to happen when we create a cluster since there is no created HostedCluster CR to check the
		// payload from.
		logger.Info("WARNING: failed to get HostedCluster to check version compatibility")
		return nil
	}

	// Get the control plane version string
	var controlPlaneVersionStr string
	if len(hcluster.Status.Version.History) == 0 {
		// If the cluster is in the process of installation, there is no history
		// Use the desired version as the control plane version
		controlPlaneVersionStr = hcluster.Status.Version.Desired.Version
	} else {
		// If the cluster is installed or upgrading
		// History is ordered by recency with the newest update first (History[0])
		// Use the most recent version from history as the default
		controlPlaneVersionStr = hcluster.Status.Version.History[0].Version
		// If the most recent version is not Completed, find the most recent Completed version
		for _, history := range hcluster.Status.Version.History {
			if history.State == "Completed" {
				controlPlaneVersionStr = history.Version
				break
			}
		}
	}

	// Parse control plane version
	controlPlaneVersion, err := semver.Parse(controlPlaneVersionStr)
	if err != nil {
		return fmt.Errorf("parsing control plane version (%s): %w", controlPlaneVersionStr, err)
	}

	pullSecret := &corev1.Secret{}
	if err = client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: hcluster.Spec.PullSecret.Name}, pullSecret); err != nil {
		return fmt.Errorf("failed to get pull secret: %w", err)
	}

	releaseImage, err := releaseProvider.Lookup(ctx, nodePoolReleaseImage, pullSecret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		// Skip version check in disconnected environment where registry access is not available
		logger.Info("WARNING: Unable to access the payload, skipping the Minor Version check.", "error", err.Error())
		return nil
	}

	// Parse NodePool version
	nodePoolVersion, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return fmt.Errorf("parsing NodePool version (%s): %w", releaseImage.Version(), err)
	}

	// Validate version skew using the centralized validation logic
	return supportedversion.ValidateVersionSkew(&controlPlaneVersion, &nodePoolVersion)
}
