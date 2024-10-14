package pkioperator

import (
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/proxy"
	hyperutil "github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func ReconcileServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	return nil
}

func ReconcileDeployment(
	deployment *appsv1.Deployment,
	openShiftTrustedCABundleConfigMapExists bool,
	hcp *hyperv1.HostedControlPlane,
	cpoPKIImage string,
	setDefaultSecurityContext bool,
	sa *corev1.ServiceAccount,
	certRotationScale time.Duration,
) error {
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"name": "control-plane-pki-operator",
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"name":                             "control-plane-pki-operator",
					"app":                              "control-plane-pki-operator",
					hyperv1.ControlPlaneComponentLabel: "control-plane-pki-operator",
				},
			},
			Spec: corev1.PodSpec{
				ImagePullSecrets: []corev1.LocalObjectReference{
					{
						Name: "pull-secret",
					},
				},
				ServiceAccountName: sa.Name,
				Containers: []corev1.Container{
					{
						Name:            "control-plane-pki-operator",
						Image:           cpoPKIImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Env: []corev1.EnvVar{
							{
								Name: "POD_NAME",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.name",
									},
								},
							},
							{
								Name: "HOSTED_CONTROL_PLANE_NAMESPACE",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										FieldPath: "metadata.namespace",
									},
								},
							},
							{
								Name:  "HOSTED_CONTROL_PLANE_NAME",
								Value: hcp.Name,
							},
							{
								Name:  "CERT_ROTATION_SCALE",
								Value: certRotationScale.String(),
							},
						},
						Command: []string{"/usr/bin/control-plane-pki-operator"},
						Args: []string{
							"operator",
							"--namespace", deployment.Namespace,
						},
						Ports: []corev1.ContainerPort{{Name: "metrics", Protocol: "TCP", ContainerPort: 8443}},
					},
				},
			},
		},
	}

	if openShiftTrustedCABundleConfigMapExists {
		hyperutil.DeploymentAddOpenShiftTrustedCABundleConfigMap(deployment)
	}

	mainContainer := hyperutil.FindContainer("control-plane-pki-operator", deployment.Spec.Template.Spec.Containers)
	proxy.SetEnvVars(&mainContainer.Env)

	deploymentConfig := config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
		SetDefaultSecurityContext: setDefaultSecurityContext,
		AdditionalLabels: map[string]string{
			config.NeedManagementKASAccessLabel: "true",
		},
		Resources: map[string]corev1.ResourceRequirements{
			"control-plane-pki-operator": {
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("80Mi"),
					corev1.ResourceCPU:    resource.MustParse("10m"),
				},
			},
		},
	}
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		deploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	deploymentConfig.SetDefaults(hcp, nil, ptr.To(1))
	deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	deploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	deploymentConfig.ApplyTo(deployment)
	config.OwnerRefFrom(hcp).ApplyTo(deployment)

	return nil
}

func ReconcileRole(role *rbacv1.Role, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(role)
	role.Rules = []rbacv1.PolicyRule{
		{ // to create owner refs
			APIGroups: []string{"hypershift.openshift.io"},
			Resources: []string{"hostedcontrolplanes"},
			Verbs:     []string{"get"},
		},
		{ // to report status
			APIGroups: []string{"hypershift.openshift.io"},
			Resources: []string{"hostedcontrolplanes/status"},
			Verbs:     []string{"patch"},
		},
		{ // to do the work of the controller
			APIGroups: []string{""},
			Resources: []string{"configmaps", "secrets", "events"},
			Verbs:     []string{"get", "list", "watch", "create", "delete", "update", "patch"},
		},
		{ // for owner reference resolution
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"get"},
		},
		{ // for owner reference resolution
			APIGroups: []string{"apps"},
			Resources: []string{"replicasets"},
			Verbs:     []string{"get"},
		},
		{ // for leader election
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{"leases"},
			Verbs:     []string{"get", "list", "watch", "create", "delete", "update", "patch"},
		},
		{ // to approve certificate signing requests
			APIGroups: []string{"certificates.hypershift.openshift.io"},
			Resources: []string{"certificatesigningrequestapprovals"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{ // for certificate revocation
			APIGroups: []string{"certificates.hypershift.openshift.io"},
			Resources: []string{"certificaterevocationrequests"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{ // for certificate revocation
			APIGroups: []string{"certificates.hypershift.openshift.io"},
			Resources: []string{"certificaterevocationrequests/status"},
			Verbs:     []string{"patch"},
		},
	}
	return nil
}

func ReconcileRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(role)
	binding.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     role.Name,
	}

	binding.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      sa.Name,
			Namespace: sa.Namespace,
		},
	}

	return nil
}
