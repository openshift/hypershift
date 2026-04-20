package agent

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/upsert"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"

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
	CAPIProviderRoleName  = "capi-provider-role"
	// capiProviderAgentRBACLabel marks Role/RoleBinding reconciled for the agent CAPI provider.
	capiProviderAgentRBACLabelKey   = "hypershift.openshift.io/capi-provider-agent-rbac"
	capiProviderAgentRBACLabelValue = "true"
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

func (p Agent) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	return nil
}

// ReconcileCAPIProviderRole creates the RBAC Role and RoleBinding in the agent
// namespace so the CAPI provider can manage Agent resources. Each HostedCluster
// creates its own Role (named per control plane namespace) to enable proper
// lifecycle management and watch-based reconciliation.
func ReconcileCAPIProviderRole(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {

	roleName := fmt.Sprintf("%s-%s", CAPIProviderRoleName, controlPlaneNamespace)
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hcluster.Spec.Platform.Agent.AgentNamespace,
			Name:      roleName,
		},
	}
	_, err := createOrUpdate(ctx, c, role, func() error {
		if role.Labels == nil {
			role.Labels = map[string]string{}
		}
		role.Labels[capiProviderAgentRBACLabelKey] = capiProviderAgentRBACLabelValue
		if role.Annotations == nil {
			role.Annotations = map[string]string{}
		}
		role.Annotations[k8sutil.HostedClusterAnnotation] = client.ObjectKeyFromObject(hcluster).String()
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{"agent-install.openshift.io"},
				Resources: []string{"agents"},
				Verbs:     []string{"get", "list", "watch", "update", "patch"},
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile Agent Role: %w", err)
	}

	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hcluster.Spec.Platform.Agent.AgentNamespace,
			Name:      fmt.Sprintf("%s-%s", CredentialsRBACPrefix, controlPlaneNamespace),
		},
	}
	_, err = createOrUpdate(ctx, c, roleBinding, func() error {
		if roleBinding.Labels == nil {
			roleBinding.Labels = map[string]string{}
		}
		roleBinding.Labels[capiProviderAgentRBACLabelKey] = capiProviderAgentRBACLabelValue
		if roleBinding.Annotations == nil {
			roleBinding.Annotations = map[string]string{}
		}
		roleBinding.Annotations[k8sutil.HostedClusterAnnotation] = client.ObjectKeyFromObject(hcluster).String()
		roleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "capi-provider",
				Namespace: controlPlaneNamespace,
			},
		}
		roleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     roleName,
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

// DeleteCredentials removes this HostedCluster's CAPI provider Role and RoleBinding.
// Each cluster has its own Role, so both can be safely deleted without affecting other clusters.
func (Agent) DeleteCredentials(ctx context.Context, c client.Client,
	hc *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	agentNS := hc.Spec.Platform.Agent.AgentNamespace
	roleName := fmt.Sprintf("%s-%s", CAPIProviderRoleName, controlPlaneNamespace)
	bindingName := fmt.Sprintf("%s-%s", CredentialsRBACPrefix, controlPlaneNamespace)

	if _, err := k8sutil.DeleteIfNeeded(ctx, c, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: bindingName, Namespace: agentNS},
	}); err != nil {
		return fmt.Errorf("failed to clean up CAPI provider RoleBinding: %w", err)
	}

	if _, err := k8sutil.DeleteIfNeeded(ctx, c, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: agentNS},
	}); err != nil {
		return fmt.Errorf("failed to clean up CAPI provider Role: %w", err)
	}

	return nil
}
