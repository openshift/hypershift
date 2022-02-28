package agent

import (
	"context"
	"fmt"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/upsert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// TODO Pin to specific release
	imageCAPAgent       = "quay.io/edge-infrastructure/cluster-api-provider-agent:latest"
	credentialsRBACName = "cluster-api-agent"
)

type Agent struct{}

func (p Agent) ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string, apiEndpoint hyperv1.APIEndpoint) (client.Object, error) {

	hcp := controlplaneoperator.HostedControlPlane(controlPlaneNamespace, hcluster.Name)
	if err := c.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
		return nil, fmt.Errorf("failed to get control plane ref: %w", err)
	}

	agentCluster := &agentv1.AgentCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      hcluster.Name,
		},
	}

	_, err := createOrUpdate(ctx, c, agentCluster, func() error {
		return reconcileAgentCluster(agentCluster, hcluster, hcp)
	})
	if err != nil {
		return nil, err
	}

	return agentCluster, nil
}

func (p Agent) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, _ *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	providerImage := imageCAPAgent
	if override, ok := hcluster.Annotations[hyperv1.ClusterAPIAgentProviderImage]; ok {
		providerImage = override
	}
	deploymentSpec := &appsv1.DeploymentSpec{
		Replicas: k8sutilspointer.Int32Ptr(1),
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				TerminationGracePeriodSeconds: k8sutilspointer.Int64Ptr(10),
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

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hcluster.Spec.Platform.Agent.AgentNamespace,
			Name:      credentialsRBACName,
		},
	}
	_, err := createOrUpdate(ctx, c, role, func() error {
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{"agent-install.openshift.io"},
				Resources: []string{"agents"},
				Verbs:     []string{"*"},
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
			Name:      credentialsRBACName,
		},
	}
	_, err = createOrUpdate(ctx, c, roleBinding, func() error {
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
			Name:     credentialsRBACName,
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile Agent RoleBinding: %w", err)
	}

	return p.reconcileClusterRole(ctx, c, createOrUpdate, controlPlaneNamespace)
}

func (p Agent) reconcileClusterRole(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	controlPlaneNamespace string) error {

	role := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: credentialsRBACName,
		},
	}
	_, err := createOrUpdate(ctx, c, role, func() error {
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{"cluster.open-cluster-management.io"},
				Resources: []string{"managedclustersets/join"},
				Verbs:     []string{"create"},
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile Agent ClusterRole: %w", err)
	}

	roleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", credentialsRBACName, controlPlaneNamespace),
		},
	}
	_, err = createOrUpdate(ctx, c, roleBinding, func() error {
		roleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "capi-provider",
				Namespace: controlPlaneNamespace,
			},
		}
		roleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     credentialsRBACName,
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile Agent ClusterRoleBinding: %w", err)
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

func reconcileAgentCluster(agentCluster *agentv1.AgentCluster, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) error {
	caSecret := ignitionserver.IgnitionCACertSecret(hcp.Namespace)
	if hcluster.Status.IgnitionEndpoint != "" {
		agentCluster.Spec.IgnitionEndpoint = &agentv1.IgnitionEndpoint{
			Url:                    "https://" + hcluster.Status.IgnitionEndpoint + "/ignition",
			CaCertificateReference: &agentv1.CaCertificateReference{Name: caSecret.Name, Namespace: caSecret.Namespace}}
	}
	agentCluster.Spec.ControlPlaneEndpoint = capiv1.APIEndpoint{
		Host: hcp.Status.ControlPlaneEndpoint.Host,
		Port: hcp.Status.ControlPlaneEndpoint.Port,
	}

	return nil
}
