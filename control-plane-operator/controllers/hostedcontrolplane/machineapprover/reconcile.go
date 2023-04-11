package machineapprover

import (
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	"gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
)

func ReconcileMachineApproverConfig(cm *corev1.ConfigMap, owner config.OwnerRef) error {
	owner.ApplyTo(cm)
	type NodeClientCert struct {
		Disabled bool `json:"disabled,omitempty"`
	}
	type ClusterMachineApproverConfig struct {
		NodeClientCert NodeClientCert `json:"nodeClientCert,omitempty"`
	}

	// Enable the client cert csr approval
	cfg := ClusterMachineApproverConfig{
		NodeClientCert: NodeClientCert{
			Disabled: false,
		},
	}
	if b, err := yaml.Marshal(cfg); err != nil {
		return err
	} else {
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data["config.yaml"] = string(b)
	}

	return nil
}

func ReconcileMachineApproverRole(role *rbacv1.Role, owner config.OwnerRef) error {
	owner.ApplyTo(role)
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"cluster.x-k8s.io"},
			Resources: []string{"machines", "machines/status"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
	return nil
}

func ReconcileMachineApproverRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount, owner config.OwnerRef) error {
	owner.ApplyTo(binding)
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

func ReconcileMachineApproverDeployment(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane, sa *corev1.ServiceAccount, kubeconfigSecretName string, cm *corev1.ConfigMap, machineApproverImage, availabilityProberImage string, setDefaultSecurityContext bool) error {
	config.OwnerRefFrom(hcp).ApplyTo(deployment)

	// TODO: enable leader election when the flag is added in machine-approver
	args := []string{
		"--config=/var/run/configmaps/config/config.yaml",
		"-v=3",
		"--logtostderr",
		"--apigroup=cluster.x-k8s.io",
		"--workload-cluster-kubeconfig=/etc/kubernetes/kubeconfig/kubeconfig",
		"--machine-namespace=" + deployment.Namespace,
		"--disable-status-controller",
	}

	labels := map[string]string{
		"app":                         "machine-approver",
		hyperv1.ControlPlaneComponent: "machine-approver",
	}
	// The selector needs to be invariant for the lifecycle of the project as it's an immutable field,
	// otherwise changing would prevent an upgrade from happening.
	selector := map[string]string{
		"app": "machine-approver",
	}
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: k8sutilspointer.Int32Ptr(1),
		Selector: &metav1.LabelSelector{
			MatchLabels: selector,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: labels,
				Name:   "machine-approver",
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: sa.Name,
				Tolerations: []corev1.Toleration{
					{
						Key:    "node-role.kubernetes.io/master",
						Effect: corev1.TaintEffectNoSchedule,
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "kubeconfig",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: kubeconfigSecretName,
							},
						},
					},
					{
						Name: "config",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: cm.Name,
								},
								Optional:    k8sutilspointer.BoolPtr(true),
								DefaultMode: k8sutilspointer.Int32Ptr(440),
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:            "machine-approver-controller",
						Image:           machineApproverImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "kubeconfig",
								MountPath: "/etc/kubernetes/kubeconfig",
							},
							{
								Name:      "config",
								MountPath: "/var/run/configmaps/config",
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("50Mi"),
								corev1.ResourceCPU:    resource.MustParse("10m"),
							},
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path:   "/metrics",
									Port:   intstr.FromInt(9191),
									Scheme: corev1.URISchemeHTTP,
								},
							},
							InitialDelaySeconds: int32(60),
							PeriodSeconds:       int32(60),
							SuccessThreshold:    int32(1),
							FailureThreshold:    int32(5),
							TimeoutSeconds:      int32(5),
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path:   "/metrics",
									Port:   intstr.FromInt(9191),
									Scheme: corev1.URISchemeHTTP,
								},
							},
							InitialDelaySeconds: int32(15),
							PeriodSeconds:       int32(60),
							SuccessThreshold:    int32(1),
							FailureThreshold:    int32(3),
							TimeoutSeconds:      int32(5),
						},
						Command: []string{"/usr/bin/machine-approver"},
						Args:    args,
					},
				},
			},
		},
	}

	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, util.APIPort(hcp)), availabilityProberImage, &deployment.Spec.Template.Spec)

	deploymentConfig := config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
		SetDefaultSecurityContext: setDefaultSecurityContext,
	}

	deploymentConfig.SetDefaults(hcp, nil, k8sutilspointer.Int(1))
	deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	deploymentConfig.ApplyTo(deployment)

	return nil
}
