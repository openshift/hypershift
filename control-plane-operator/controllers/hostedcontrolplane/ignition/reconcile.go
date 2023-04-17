package ignition

import (
	"bytes"
	"fmt"

	"github.com/clarketm/json"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	jsonserializer "k8s.io/apimachinery/pkg/runtime/serializer/json"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"
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

func ReconcileImageSourceMirrorsIgnitionConfig(cm *corev1.ConfigMap, ownerRef config.OwnerRef, imageDigestMirrorSet *configv1.ImageDigestMirrorSet) error {
	return reconcileImageContentPolicyIgnitionConfigMap(cm, imageDigestMirrorSet, ownerRef)
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

func reconcileImageContentPolicyIgnitionConfigMap(cm *corev1.ConfigMap, imageDigestMirrorSet *configv1.ImageDigestMirrorSet, ownerRef config.OwnerRef) error {
	scheme := runtime.NewScheme()
	err := configv1.Install(scheme)
	if err != nil {
		return err
	}
	yamlSerializer := jsonserializer.NewSerializerWithOptions(
		jsonserializer.DefaultMetaFactory, scheme, scheme,
		jsonserializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true})
	imageContentBytesBuffer := bytes.NewBuffer([]byte{})
	if err := yamlSerializer.Encode(imageDigestMirrorSet, imageContentBytesBuffer); err != nil {
		return fmt.Errorf("failed to serialize image digest mirror set: %w", err)
	}
	return ReconcileIgnitionConfigMap(cm, imageContentBytesBuffer.String(), ownerRef)
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
