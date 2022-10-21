package kubevirt

import (
	"context"
	"fmt"
	"os"
	"strings"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	nmstate "github.com/nmstate/kubernetes-nmstate/api/shared"
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
	if _, err := createOrUpdate(ctx, c, kubevirtCluster, func() error {
		reconcileKubevirtCluster(kubevirtCluster, hcluster)
		return nil
	}); err != nil {
		return nil, err
	}

	nad := &nadv1.NetworkAttachmentDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "bridge-network",
		},
	}
	if _, err := createOrUpdate(ctx, c, nad, func() error {
		reconcileNAD(nad, kubevirtCluster)
		return nil
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
	bridgeName := bridgeName(kubevirtCluster)
	kubevirtCluster.Spec = capikubevirt.KubevirtClusterSpec{
		Network: "192.168.4.0/24", //TODO: Read this from hosted network fields
		InfraClusterNodeNetwork: &capikubevirt.InfraClusterNodeNetwork{
			Setup: nmstate.NodeNetworkConfigurationPolicySpec{
				Capture: map[string]string{
					"default-gw": "routes.running.destination=='0.0.0.0/0'",
					"base-iface": "interfaces.name==capture.default-gw.routes.running.0.next-hop-interface",
				},
				DesiredState: nmstate.NewState(fmt.Sprintf(`
interfaces:
- name: base.100
  type: vlan
  state: up 
  vlan:
    base-iface: "{{ capture.base-iface.interfaces.0.name }}"
    id: 100
- name: %s
  description:  "capk.cluster.x-k8s.io/interface"
  type: linux-bridge
  state: up
  ipv4:
    enabled: true
    dhcp: false
  ipv6:
    enabled: false
  bridge:
    options:
      stp:
        enabled: false
    port:
    - name: base.100
`, bridgeName)),
			},
			TearDown: nmstate.NodeNetworkConfigurationPolicySpec{
				DesiredState: nmstate.NewState(fmt.Sprintf(`
interfaces:
  - name: base.100
    state: absent
  - name: %s
    state: absent
`, bridgeName))}}}
	// Set the values for upper level controller
	kubevirtCluster.Status.Ready = true
}

func init() {
	nadv1.AddToScheme(hyperapi.Scheme)
}

func reconcileNAD(nad *nadv1.NetworkAttachmentDefinition, kubevirtCluster *capikubevirt.KubevirtCluster) {
	bridgeName := bridgeName(kubevirtCluster)
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

func bridgeName(kubevirtCluster *capikubevirt.KubevirtCluster) string {
	return "br-" + strings.TrimPrefix(kubevirtCluster.Namespace, "clusters-")
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
			APIGroups: []string{"kubevirt.io"},
			Resources: []string{"virtualmachineinstances", "virtualmachines"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"nodes"},
			Verbs:     []string{"list"},
		},
		{
			APIGroups: []string{"nmstate.io"},
			Resources: []string{"nodenetworkconfigurationpolicies"},
			Verbs:     []string{"*"},
		},
	}
}

func (Kubevirt) DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	return nil
}
