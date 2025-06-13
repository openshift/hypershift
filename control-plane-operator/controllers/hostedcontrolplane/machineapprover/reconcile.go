package machineapprover

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"gopkg.in/yaml.v2"
)

const (
	machineApproverName = "machine-approver-controller"
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

func ReconcileMachineApproverDeployment(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane, sa *corev1.ServiceAccount, kubeconfigSecretName string, cm *corev1.ConfigMap, machineApproverImage, availabilityProberImage string, setDefaultSecurityContext bool, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(deployment)

	machineApproverResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("50Mi"),
			corev1.ResourceCPU:    resource.MustParse("10m"),
		},
	}
	// preserve existing resource requirements
	mainContainer := util.FindContainer(machineApproverName, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		if len(mainContainer.Resources.Requests) > 0 || len(mainContainer.Resources.Limits) > 0 {
			machineApproverResources = mainContainer.Resources
		}
	}

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
		"app":                              "machine-approver",
		hyperv1.ControlPlaneComponentLabel: "machine-approver",
	}
	// The selector needs to be invariant for the lifecycle of the project as it's an immutable field,
	// otherwise changing would prevent an upgrade from happening.
	selector := map[string]string{
		"app": "machine-approver",
	}
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: ptr.To[int32](1),
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
								Optional:    ptr.To(true),
								DefaultMode: ptr.To[int32](440),
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:            machineApproverName,
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
						Resources: machineApproverResources,
						Command:   []string{"/usr/bin/machine-approver"},
						Args:      args,
					},
				},
			},
		},
	}

	util.AvailabilityProber(kas.InClusterKASReadyURL(hcp.Spec.Platform.Type), availabilityProberImage, &deployment.Spec.Template.Spec)

	deploymentConfig := config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
		SetDefaultSecurityContext: setDefaultSecurityContext,
		AdditionalLabels: map[string]string{
			config.NeedManagementKASAccessLabel: "true",
		},
	}
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		deploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	deploymentConfig.SetDefaults(hcp, nil, ptr.To(1))
	deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	deploymentConfig.ApplyTo(deployment)

	return nil
}

func ReconcileMachineApprover(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane, machineApproverImage, availabilityProberImage string, createOrUpdate upsert.CreateOrUpdateFN, setDefaultSecurityContext bool, ownerRef config.OwnerRef) error {
	role := manifests.MachineApproverRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, c, role, func() error {
		return ReconcileMachineApproverRole(role, ownerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile machine-approver role: %w", err)
	}

	sa := manifests.MachineApproverServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, c, sa, func() error {
		util.EnsurePullSecret(sa, controlplaneoperator.PullSecret("").Name)
		ownerRef.ApplyTo(sa)
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile machine-approver service account: %w", err)
	}

	roleBinding := manifests.MachineApproverRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, c, roleBinding, func() error {
		return ReconcileMachineApproverRoleBinding(roleBinding, role, sa, ownerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile machine-approver role binding: %w", err)
	}
	cm := manifests.ConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, c, cm, func() error {
		return ReconcileMachineApproverConfig(cm, ownerRef)
	}); err != nil {
		return fmt.Errorf("failed to reconcile machine-approver config: %w", err)
	}

	if hcp.Status.KubeConfig != nil {
		// Resolve the kubeconfig secret for machine-approver
		kubeconfigSecretName := manifests.KASServiceKubeconfigSecret(hcp.Namespace).Name
		deployment := manifests.MachineApproverDeployment(hcp.Namespace)
		if _, err := createOrUpdate(ctx, c, deployment, func() error {
			return ReconcileMachineApproverDeployment(deployment, hcp, sa, kubeconfigSecretName, cm, machineApproverImage, availabilityProberImage, setDefaultSecurityContext, ownerRef)
		}); err != nil {
			return fmt.Errorf("failed to reconcile machine-approver deployment: %w", err)
		}
	}

	return nil
}
