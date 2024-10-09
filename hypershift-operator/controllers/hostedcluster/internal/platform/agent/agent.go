package agent

import (
	"context"
	"fmt"
	"os"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/upsert"
	hyperutil "github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// TODO Pin to specific release
	imageCAPAgent         = "quay.io/edge-infrastructure/cluster-api-provider-agent:latest"
	CredentialsRBACPrefix = "cluster-api-agent"
)

type Agent struct{}

func (p Agent) ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string, apiEndpoint hyperv1.APIEndpoint) (client.Object, error) {

	// Ensure we create the agentCluster only after ignition endpoint exists
	// so AgentClusterInstall is only created with the right ign to boot machines.
	// https://bugzilla.redhat.com/show_bug.cgi?id=2097895
	// https://github.com/openshift/assisted-service/blob/241ad46db74add5f16e153f5f7ba0a5496fb06ba/pkg/validating-webhooks/hiveextension/v1beta1/agentclusterinstall_admission_hook.go#L185
	// https://github.com/openshift/cluster-api-provider-agent/blob/master/controllers/agentcluster_controller.go#L126
	if hcluster.Status.IgnitionEndpoint == "" {
		return nil, nil
	}

	agentCluster := &agentv1.AgentCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      hcluster.Name,
		},
	}

	_, err := createOrUpdate(ctx, c, agentCluster, func() error {
		return reconcileAgentCluster(agentCluster, hcluster.Status.IgnitionEndpoint, controlPlaneNamespace, apiEndpoint)
	})
	if err != nil {
		return nil, err
	}

	return agentCluster, nil
}

func (p Agent) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, _ *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	providerImage := imageCAPAgent
	if envImage := os.Getenv(images.AgentCAPIProviderEnvVar); len(envImage) > 0 {
		providerImage = envImage
	}
	if override, ok := hcluster.Annotations[hyperv1.ClusterAPIAgentProviderImage]; ok {
		providerImage = override
	}
	deploymentSpec := &appsv1.DeploymentSpec{
		Replicas: ptr.To[int32](1),
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				TerminationGracePeriodSeconds: ptr.To[int64](10),
				Containers: []corev1.Container{
					{
						Name:  "manager",
						Image: providerImage,
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
							"--health-probe-bind-address=:8081",
							"--metrics-bind-address=127.0.0.1:8080",
							"--leader-elect",
							"--agent-namespace", hcluster.Spec.Platform.Agent.AgentNamespace,
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz",
									Port: intstr.FromInt(8081),
								},
							},
							InitialDelaySeconds: 15,
							PeriodSeconds:       20,
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/readyz",
									Port: intstr.FromInt(8081),
								},
							},
							InitialDelaySeconds: 15,
							PeriodSeconds:       20,
						},
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("200m"),
								corev1.ResourceMemory: resource.MustParse("100Mi"),
							},
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("20Mi"),
							},
						},
					},
				},
			},
		},
	}
	return deploymentSpec, nil
}

// TODO add a new method to Platform interface?
func (p Agent) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {

	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hcluster.Spec.Platform.Agent.AgentNamespace,
			Name:      fmt.Sprintf("%s-%s", CredentialsRBACPrefix, controlPlaneNamespace),
		},
	}
	_, err := createOrUpdate(ctx, c, roleBinding, func() error {
		roleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "capi-provider",
				Namespace: controlPlaneNamespace,
			},
		}
		roleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "capi-provider-role",
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile Agent RoleBinding: %w", err)
	}
	return nil
}

func (Agent) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	return nil
}

func (Agent) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{"extensions.hive.openshift.io"},
			Resources: []string{"agentclusterinstalls"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"capi-provider.agent-install.openshift.io"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"hive.openshift.io"},
			Resources: []string{"clusterdeployments"},
			Verbs:     []string{"*"},
		},
	}
}

func reconcileAgentCluster(agentCluster *agentv1.AgentCluster, ignEndpoint, controlPlaneNamespace string, apiEndpoint hyperv1.APIEndpoint) error {
	caSecret := ignitionserver.IgnitionCACertSecret(controlPlaneNamespace)
	agentCluster.Spec.IgnitionEndpoint = &agentv1.IgnitionEndpoint{
		Url:                    "https://" + ignEndpoint + "/ignition",
		CaCertificateReference: &agentv1.CaCertificateReference{Name: caSecret.Name, Namespace: caSecret.Namespace}}

	agentCluster.Spec.ControlPlaneEndpoint = capiv1.APIEndpoint{
		Host: apiEndpoint.Host,
		Port: apiEndpoint.Port,
	}

	return nil
}

func (Agent) DeleteCredentials(ctx context.Context, c client.Client,
	hc *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	if _, err := hyperutil.DeleteIfNeeded(ctx, c, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-%s", CredentialsRBACPrefix, controlPlaneNamespace), Namespace: hc.Spec.Platform.Agent.AgentNamespace}}); err != nil {
		return fmt.Errorf("failed to clean up CAPI provider rolebinding: %w", err)
	}
	return nil
}
