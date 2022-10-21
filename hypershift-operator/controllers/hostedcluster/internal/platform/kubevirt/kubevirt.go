package kubevirt

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	nmstate "github.com/nmstate/kubernetes-nmstate/api/shared"
	nmstatev1 "github.com/nmstate/kubernetes-nmstate/api/v1"
	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/upsert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	hostedClusterAnnotation = "hypershift.openshift.io/cluster"
	imageCAPK               = "registry.ci.openshift.org/hypershift/cluster-api-kubevirt-controller:0.0.1-prerelease"
)

type Kubevirt struct{}

func (p Kubevirt) ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string, apiEndpoint hyperv1.APIEndpoint) (client.Object, error) {
	kubevirtCluster := &capikubevirt.KubevirtCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      hcluster.Spec.InfraID,
		},
	}
	bridgeName, err := bridgeName(kubevirtCluster)
	if err != nil {
		return nil, err
	}
	vlan := 1
	vlanString, ok := hcluster.Annotations["hypershift.openshift.io/capi-provider-kubevirt-vlan"]
	if ok {
		var err error
		vlan, err = strconv.Atoi(vlanString)
		if err != nil {
			return nil, err
		}
	}
	if _, err := createOrUpdate(ctx, c, kubevirtCluster, func() error {
		reconcileKubevirtCluster(kubevirtCluster, hcluster)
		return nil
	}); err != nil {
		return nil, err
	}

	nncp := &nmstatev1.NodeNetworkConfigurationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: controlPlaneNamespace,
		},
	}
	if _, err := createOrUpdate(ctx, c, nncp, func() error {
		reconcileNNCP(nncp, bridgeName, vlan, kubevirtCluster)
		return nil
	}); err != nil {
		return nil, err
	}

	vmsNAD := &nadv1.NetworkAttachmentDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "bridge-network-vmis",
		},
	}
	if _, err := createOrUpdate(ctx, c, vmsNAD, func() error {
		reconcileVMsNAD(vmsNAD, bridgeName, kubevirtCluster)
		return nil
	}); err != nil {
		return nil, err
	}

	podsNAD := &nadv1.NetworkAttachmentDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "bridge-network-pods",
		},
	}
	if _, err := createOrUpdate(ctx, c, podsNAD, func() error {
		return reconcilePodsNAD(podsNAD, bridgeName, kubevirtCluster)
	}); err != nil {
		return nil, err
	}

	return kubevirtCluster, nil
}

func reconcileKubevirtCluster(kubevirtCluster *capikubevirt.KubevirtCluster, hcluster *hyperv1.HostedCluster) {
	// We only create this resource once and then let CAPI own it
	kubevirtCluster.Annotations = map[string]string{
		hostedClusterAnnotation:    client.ObjectKeyFromObject(hcluster).String(),
		capiv1.ManagedByAnnotation: "external",
	}
	kubevirtCluster.Spec = capikubevirt.KubevirtClusterSpec{
		Network: "192.168.4.0/24", //TODO: Read this from hosted network fields
	}
	// Set the values for upper level controller
	kubevirtCluster.Status.Ready = true
}

func init() {
	nadv1.AddToScheme(hyperapi.Scheme)
	nmstatev1.AddToScheme(hyperapi.Scheme)
}

func reconcileVMsNAD(nad *nadv1.NetworkAttachmentDefinition, bridgeName string, kubevirtCluster *capikubevirt.KubevirtCluster) {
	if nad.Labels == nil {
		nad.Labels = map[string]string{}
	}
	nad.Labels["capk.cluster.x-k8s.io/template-kind"] = "external"
	nad.Labels["cluster.x-k8s.io/cluster-name"] = kubevirtCluster.Name
	if nad.Annotations == nil {
		nad.Annotations = map[string]string{}
	}
	nad.Annotations["k8s.v1.cni.cncf.io/resourceName"] = "bridge.network.kubevirt.io/" + bridgeName
	nad.Spec.Config = fmt.Sprintf(`
{
  "cniVersion": "0.3.1",
  "name": "%s",
  "plugins": [{
      "type": "bridge",
      "bridge": "%s",
      "ipam": {}
  }]
}
`, bridgeName, bridgeName)
}

func reconcilePodsNAD(nad *nadv1.NetworkAttachmentDefinition, bridgeName string, kubevirtCluster *capikubevirt.KubevirtCluster) error {
	if nad.Labels == nil {
		nad.Labels = map[string]string{}
	}
	nad.Labels["capk.cluster.x-k8s.io/template-kind"] = "external"
	nad.Labels["cluster.x-k8s.io/cluster-name"] = kubevirtCluster.Name
	if nad.Annotations == nil {
		nad.Annotations = map[string]string{}
	}
	nad.Annotations["k8s.v1.cni.cncf.io/resourceName"] = "bridge.network.kubevirt.io/" + bridgeName
	nad.Spec.Config = fmt.Sprintf(`
{
  "cniVersion": "0.3.1",
  "name": "%s",
  "plugins": [{
      "type": "bridge",
      "bridge": "%s",
      "ipam": {
		"type": "whereabouts",
    	"range": "%s"
	  }
  }]
}
`, bridgeName, bridgeName, kubevirtCluster.Spec.Network)
	return nil
}

func reconcileNNCP(nncp *nmstatev1.NodeNetworkConfigurationPolicy, bridgeName string, vlan int, kubevirtCluster *capikubevirt.KubevirtCluster) {
	if nncp.Labels == nil {
		nncp.Labels = map[string]string{}
	}
	nncp.Labels["capk.cluster.x-k8s.io/template-kind"] = "external"
	nncp.Labels["cluster.x-k8s.io/cluster-name"] = kubevirtCluster.Name

	nncp.Spec = nmstate.NodeNetworkConfigurationPolicySpec{
		Capture: map[string]string{
			"default-gw": "routes.running.destination=='0.0.0.0/0'",
			"base-iface": "interfaces.name==capture.default-gw.routes.running.0.next-hop-interface",
		},
		DesiredState: nmstate.NewState(fmt.Sprintf(`
interfaces:
- name: base.%[2]d
  type: vlan
  state: up 
  vlan:
    base-iface: "{{ capture.base-iface.interfaces.0.name }}"
    id: %[2]d
- name: %[1]s
  type: linux-bridge
  state: up
  ipv4:
    enabled: false
  ipv6:
    enabled: false
  bridge:
    options:
      stp:
        enabled: false
    port:
    - name: base.%[2]d
`, bridgeName, vlan)),
	}

}

func bridgeName(kubevirtCluster *capikubevirt.KubevirtCluster) (string, error) {
	maxNetInterfaceSize := 15
	bridgePrefix := "br-"
	clusterName := strings.TrimPrefix(kubevirtCluster.Namespace, "clusters-")
	bridgeName := bridgePrefix + clusterName
	if len(bridgeName) > maxNetInterfaceSize {
		return "", fmt.Errorf("cluster name '%s' is longer than %d characters", clusterName, maxNetInterfaceSize-len(bridgePrefix))
	}
	return bridgeName, nil
}

func (p Kubevirt) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, _ *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	providerImage := imageCAPK
	if envImage := os.Getenv(images.KubevirtCAPIProviderEnvVar); len(envImage) > 0 {
		providerImage = envImage
	}
	if override, ok := hcluster.Annotations[hyperv1.ClusterAPIKubeVirtProviderImage]; ok {
		providerImage = override
	}
	defaultMode := int32(416)
	return &appsv1.DeploymentSpec{
		Replicas: k8sutilspointer.Int32Ptr(1),
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				TerminationGracePeriodSeconds: k8sutilspointer.Int64Ptr(10),
				Tolerations: []corev1.Toleration{
					{
						Key:    "node-role.kubernetes.io/master",
						Effect: corev1.TaintEffectNoSchedule,
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "capi-webhooks-tls",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								DefaultMode: &defaultMode,
								SecretName:  "capi-webhooks-tls",
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:            "manager",
						Image:           providerImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("100Mi"),
								corev1.ResourceCPU:    resource.MustParse("10m"),
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "capi-webhooks-tls",
								ReadOnly:  true,
								MountPath: "/tmp/k8s-webhook-server/serving-certs",
							},
						},
						Env: []corev1.EnvVar{
							{
								Name: "MY_NAMESPACE",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.namespace",
									},
								},
							},
						},
						Command: []string{"/manager"},
						Args: []string{
							"--namespace", "$(MY_NAMESPACE)",
							"--alsologtostderr",
							"--v=4",
							"--leader-elect=true",
						},
						Ports: []corev1.ContainerPort{
							{
								Name:          "healthz",
								ContainerPort: 9440,
								Protocol:      corev1.ProtocolTCP,
							},
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz",
									Port: intstr.FromString("healthz"),
								},
							},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/readyz",
									Port: intstr.FromString("healthz"),
								},
							},
						},
					},
				},
			},
		},
	}, nil
}

func (p Kubevirt) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	return nil
}

func (Kubevirt) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	return nil
}

func (Kubevirt) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"services"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"apps"},
			Resources: []string{"deployments"},
			Verbs:     []string{"get", "watch", "update", "create", "delete", "list"},
		},
		{
			APIGroups: []string{"kubevirt.io"},
			Resources: []string{"virtualmachineinstances", "virtualmachines"},
			Verbs:     []string{"*"},
		},
	}
}

func (Kubevirt) DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	return nil
}
