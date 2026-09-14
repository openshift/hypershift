package globalpullsecret

import (
	"context"
	"embed"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignition"
	api "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/util"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/vincent-petithory/dataurl"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"github.com/clarketm/json"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ControlPlaneOperatorManagesGlobalPullSecretAuthDLabel = "io.openshift.hypershift.control-plane-operator-manages.global-pull-secret-auth-d"
)

type GlobalPullSecret struct {
	crclient.Client

	ReleaseProvider       releaseinfo.Provider
	ImageMetadataProvider util.ImageMetadataProvider
	HypershiftOperatorImage string
}

//go:embed assets/*
var content embed.FS

func mustAsset(name string) string {
	b, err := content.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// isGlobalPullSecretAuthDManaged checks if the CPO image contains the label indicating it manages the auth.d approach
func (r *GlobalPullSecret) isGlobalPullSecretAuthDManaged(ctx context.Context, hcluster *hyperv1.HostedCluster) (managed bool, cpoImage string, err error) {
	var pullSecret corev1.Secret
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: hcluster.Namespace, Name: hcluster.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return false, "", fmt.Errorf("failed to get pull secret: %w", err)
	}
	pullSecretBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return false, "", fmt.Errorf("expected %s key in pull secret", corev1.DockerConfigJsonKey)
	}
	controlPlaneOperatorImage, err := util.GetControlPlaneOperatorImage(ctx, hcluster, r.ReleaseProvider, r.HypershiftOperatorImage, pullSecretBytes)
	if err != nil {
		return false, "", fmt.Errorf("failed to get controlPlaneOperatorImage: %w", err)
	}

	controlPlaneOperatorImageMetadata, err := r.ImageMetadataProvider.ImageMetadata(ctx, controlPlaneOperatorImage, pullSecretBytes)
	if err != nil {
		return false, "", fmt.Errorf("failed to look up image metadata for %s: %w", controlPlaneOperatorImage, err)
	}

	_, cpoManages := util.ImageLabels(controlPlaneOperatorImageMetadata)[ControlPlaneOperatorManagesGlobalPullSecretAuthDLabel]
	return cpoManages, controlPlaneOperatorImage, nil
}

// GenerateGlobalPullSecretMachineConfig generates a MachineConfig that sets up the auth.d infrastructure
func (r *GlobalPullSecret) GenerateGlobalPullSecretMachineConfig(ctx context.Context, hcluster *hyperv1.HostedCluster) (string, error) {
	isManaged, _, err := r.isGlobalPullSecretAuthDManaged(ctx, hcluster)
	if err != nil {
		return "", fmt.Errorf("failed to check if CPO manages global pull secret auth.d: %w", err)
	}
	if !isManaged {
		// CPO doesn't support auth.d yet, return empty string
		return "", nil
	}

	config := &ignitionapi.Config{}
	config.Ignition.Version = ignitionapi.MaxVersion.String()

	// Add merge script
	mergeScriptContent := mustAsset("assets/merge-pull-secrets.sh")
	config.Storage.Files = append(config.Storage.Files, fileFromBytes("/usr/local/bin/merge-pull-secrets.sh", 0755, []byte(mergeScriptContent)))

	// Add CRI-O config drop-in
	crioConfigContent := mustAsset("assets/crio-auth.conf")
	config.Storage.Files = append(config.Storage.Files, fileFromBytes("/etc/crio/crio.conf.d/99-additional-auth.conf", 0644, []byte(crioConfigContent)))

	// Create auth.d directory
	config.Storage.Directories = append(config.Storage.Directories, ignitionapi.Directory{
		Node: ignitionapi.Node{
			Path: "/var/lib/kubelet/auth.d",
		},
		DirectoryEmbedded1: ignitionapi.DirectoryEmbedded1{
			Mode: ptr.To(0700),
		},
	})

	// Add systemd units
	pathUnitContent := mustAsset("assets/merge-pull-secrets.path")
	config.Systemd.Units = append(config.Systemd.Units, ignitionapi.Unit{
		Name:     "merge-pull-secrets.path",
		Enabled:  ptr.To(true),
		Contents: ptr.To(pathUnitContent),
	})

	serviceUnitContent := mustAsset("assets/merge-pull-secrets.service")
	config.Systemd.Units = append(config.Systemd.Units, ignitionapi.Unit{
		Name:     "merge-pull-secrets.service",
		Enabled:  ptr.To(false),
		Contents: ptr.To(serviceUnitContent),
	})

	serializedConfig, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("failed to marshal ignition config: %w", err)
	}

	machineConfig := &mcfgv1.MachineConfig{}
	ignition.SetMachineConfigLabels(machineConfig)
	machineConfig.ObjectMeta.Name = "99-additional-registry-auth"
	machineConfig.Spec.Config.Raw = serializedConfig

	machineConfig.APIVersion = mcfgv1.SchemeGroupVersion.String()
	machineConfig.Kind = "MachineConfig"
	encoded, err := api.CompatibleYAMLEncode(machineConfig, api.YamlSerializer)
	if err != nil {
		return "", fmt.Errorf("failed to serialize global pull secret machine config: %w", err)
	}

	return string(encoded), nil
}

// fileFromBytes creates an ignition-config file with the given contents.
func fileFromBytes(path string, mode int, contents []byte) ignitionapi.File {
	return ignitionapi.File{
		Node: ignitionapi.Node{
			Path:      path,
			Overwrite: ptr.To(true),
		},
		FileEmbedded1: ignitionapi.FileEmbedded1{
			Mode: &mode,
			Contents: ignitionapi.Resource{
				Source: ptr.To(dataurl.EncodeBytes(contents)),
			},
		},
	}
}
