package kubevirt

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/upsert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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
	kubevirtCluster.TypeMeta = metav1.TypeMeta{
		Kind:       "KubevirtCluster",
		APIVersion: capikubevirt.GroupVersion.String(),
	}

	return kubevirtCluster, nil
}

func reconcileKubevirtCluster(kubevirtCluster *capikubevirt.KubevirtCluster, hcluster *hyperv1.HostedCluster) {
	// We only create this resource once and then let CAPI own it
	kubevirtCluster.Annotations = map[string]string{
		hostedClusterAnnotation:    client.ObjectKeyFromObject(hcluster).String(),
		capiv1.ManagedByAnnotation: "external",
	}
	// Set the values for upper level controller
	kubevirtCluster.Status.Ready = true
}

func (p Kubevirt) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, tokenMinterImage string) (*appsv1.DeploymentSpec, error) {
	defaultMode := int32(420)
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
						Image:           imageCAPK,
						ImagePullPolicy: corev1.PullAlways,
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
	}
}
