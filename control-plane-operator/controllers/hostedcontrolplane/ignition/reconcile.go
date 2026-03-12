package ignition

import (
	"bytes"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"

	configv1 "github.com/openshift/api/config/v1"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	jsonserializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/clarketm/json"
	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/vincent-petithory/dataurl"
)

const (
	ignitionConfigKey = "config"
	ignitionVersion   = "3.2.0"
)

var (
	defaultMachineConfigLabels = map[string]string{
		"machineconfiguration.openshift.io/role": "worker",
	}

	defaultIgnitionConfigMapLabels = map[string]string{
		"hypershift.openshift.io/core-ignition-config": "true",
	}
)

func ReconcileFIPSIgnitionConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, fipsEnabled bool) error {
	machineConfig := manifests.MachineConfigFIPS()
	SetMachineConfigLabels(machineConfig)
	machineConfig.Spec.FIPS = fipsEnabled
	return reconcileMachineConfigIgnitionConfigMap(cm, machineConfig, ownerRef)
}

func ReconcileWorkerSSHIgnitionConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, sshKey string) error {
	machineConfig := manifests.MachineConfigWorkerSSH()
	SetMachineConfigLabels(machineConfig)
	serializedConfig, err := workerSSHConfig(sshKey)
	if err != nil {
		return fmt.Errorf("failed to serialize ignition config: %w", err)
	}
	machineConfig.Spec.Config.Raw = serializedConfig
	return reconcileMachineConfigIgnitionConfigMap(cm, machineConfig, ownerRef)
}

func ReconcileImageSourceMirrorsIgnitionConfigFromIDMS(cm *corev1.ConfigMap, ownerRef config.OwnerRef, imageDigestMirrorSet *configv1.ImageDigestMirrorSet) error {
	return reconcileImageContentTypeIgnitionConfigMap(cm, imageDigestMirrorSet, ownerRef)
}

func workerSSHConfig(sshKey string) ([]byte, error) {
	config := &igntypes.Config{}
	config.Ignition.Version = ignitionVersion
	config.Passwd = igntypes.Passwd{
		Users: []igntypes.PasswdUser{
			{
				Name: "core",
			},
		},
	}
	if len(sshKey) > 0 {
		config.Passwd.Users[0].SSHAuthorizedKeys = []igntypes.SSHAuthorizedKey{
			igntypes.SSHAuthorizedKey(sshKey),
		}
	}
	return serializeIgnitionConfig(config)
}

func serializeIgnitionConfig(cfg *igntypes.Config) ([]byte, error) {
	jsonBytes, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("error marshaling ignition config: %w", err)
	}
	return jsonBytes, nil
}

func SetMachineConfigLabels(mc *mcfgv1.MachineConfig) {
	if mc.Labels == nil {
		mc.Labels = map[string]string{}
	}
	for k, v := range defaultMachineConfigLabels {
		mc.Labels[k] = v
	}
}

func reconcileImageContentTypeIgnitionConfigMap(cm *corev1.ConfigMap, imageContentType client.Object, ownerRef config.OwnerRef) error {
	scheme := runtime.NewScheme()
	err := operatorv1alpha1.Install(scheme)
	if err != nil {
		return err
	}
	err = configv1.Install(scheme)
	if err != nil {
		return err
	}
	yamlSerializer := jsonserializer.NewSerializerWithOptions(
		jsonserializer.DefaultMetaFactory, scheme, scheme,
		jsonserializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true})
	imageContentTypeBytesBuffer := bytes.NewBuffer([]byte{})
	if err := yamlSerializer.Encode(imageContentType, imageContentTypeBytesBuffer); err != nil {
		return fmt.Errorf("failed to serialize image content type policy: %w", err)
	}
	return ReconcileIgnitionConfigMap(cm, imageContentTypeBytesBuffer.String(), ownerRef)
}

func reconcileMachineConfigIgnitionConfigMap(cm *corev1.ConfigMap, mc *mcfgv1.MachineConfig, ownerRef config.OwnerRef) error {
	buf := &bytes.Buffer{}
	mc.APIVersion = mcfgv1.SchemeGroupVersion.String()
	mc.Kind = "MachineConfig"
	if err := api.YamlSerializer.Encode(mc, buf); err != nil {
		return fmt.Errorf("failed to serialize machine config %s: %w", cm.Name, err)
	}
	return ReconcileIgnitionConfigMap(cm, buf.String(), ownerRef)
}

const (
	// imageRegistryCertPath is the path on worker nodes where the image registry
	// service-CA certificate is placed. CRI-O uses this to trust the internal
	// image registry's TLS certificate when pulling images.
	imageRegistryCertPath = "/etc/docker/certs.d/image-registry.openshift-image-registry.svc:5000/ca.crt"
)

// ReconcileImageRegistryCAIgnitionConfig creates a MachineConfig that places the
// image registry service-CA certificate on worker nodes. This allows nodes to
// trust the internal image registry without requiring the node-ca daemonset.
// When serviceCA is empty (e.g., during initial cluster bootstrap before the
// service-ca operator has started), a no-op MachineConfig is created.
func ReconcileImageRegistryCAIgnitionConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, serviceCA string) error {
	machineConfig := manifests.MachineConfigImageRegistryCA()
	SetMachineConfigLabels(machineConfig)

	ignConfig := &igntypes.Config{}
	ignConfig.Ignition.Version = ignitionVersion

	if len(serviceCA) > 0 {
		fileMode := 0o644
		ignConfig.Storage = igntypes.Storage{
			Files: []igntypes.File{
				{
					Node: igntypes.Node{
						Path:      imageRegistryCertPath,
						Overwrite: ptr.To(true),
					},
					FileEmbedded1: igntypes.FileEmbedded1{
						Mode: &fileMode,
						Contents: igntypes.Resource{
							Source: ptr.To(dataurl.EncodeBytes([]byte(serviceCA))),
						},
					},
				},
			},
		}
	}

	serializedConfig, err := serializeIgnitionConfig(ignConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize image registry CA ignition config: %w", err)
	}
	machineConfig.Spec.Config.Raw = serializedConfig
	return reconcileMachineConfigIgnitionConfigMap(cm, machineConfig, ownerRef)
}

func ReconcileIgnitionConfigMap(cm *corev1.ConfigMap, content string, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	for k, v := range defaultIgnitionConfigMapLabels {
		cm.Labels[k] = v
	}
	cm.Data = map[string]string{
		ignitionConfigKey: content,
	}
	return nil
}
