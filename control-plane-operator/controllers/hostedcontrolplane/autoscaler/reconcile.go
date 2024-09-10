package autoscaler

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
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	autoscalerName = "cluster-autoscaler"
)

func ReconcileAutoscalerDeployment(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane, sa *corev1.ServiceAccount, kubeConfigSecret *corev1.Secret, options hyperv1.ClusterAutoscaling, clusterAutoscalerImage, availabilityProberImage string, setDefaultSecurityContext bool, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(deployment)

	autoscalerResources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("60Mi"),
			corev1.ResourceCPU:    resource.MustParse("10m"),
		},
	}
	// preserve existing resource requirements
	mainContainer := util.FindContainer(autoscalerName, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		if len(mainContainer.Resources.Requests) > 0 || len(mainContainer.Resources.Limits) > 0 {
			autoscalerResources = mainContainer.Resources
		}
	}

	args := []string{
		"--expander=priority,least-waste",
		"--cloud-provider=clusterapi",
		"--node-group-auto-discovery=clusterapi:namespace=$(MY_NAMESPACE)",
		"--kubeconfig=/mnt/kubeconfig/target-kubeconfig",
		"--clusterapi-cloud-config-authoritative",
		// TODO (alberto): Is this a fair assumption?
		// There's currently pods with local storage e.g grafana and image-registry.
		// Without this option after after a scaling out operation and an “unfortunate” reschedule
		// we might end up locked with three nodes.
		"--skip-nodes-with-local-storage=false",
		"--alsologtostderr",
		fmt.Sprintf("--leader-elect-lease-duration=%s", config.RecommendedLeaseDuration),
		fmt.Sprintf("--leader-elect-retry-period=%s", config.RecommendedRetryPeriod),
		fmt.Sprintf("--leader-elect-renew-deadline=%s", config.RecommendedRenewDeadline),
		"--balance-similar-node-groups=true",
		"--v=4",
	}

	ignoreLabels := GetIgnoreLabels()
	for _, v := range ignoreLabels {
		args = append(args, fmt.Sprintf("%s=%v", BalancingIgnoreLabelArg, v))
	}

	// TODO if the options for the cluster autoscaler continues to grow, we should take inspiration
	// from the cluster-autoscaler-operator and create some utility functions for these assignments.
	if options.MaxNodesTotal != nil {
		arg := fmt.Sprintf("%s=%d", "--max-nodes-total", *options.MaxNodesTotal)
		args = append(args, arg)
	}

	if options.MaxPodGracePeriod != nil {
		arg := fmt.Sprintf("%s=%d", "--max-graceful-termination-sec", *options.MaxPodGracePeriod)
		args = append(args, arg)
	}

	if options.MaxNodeProvisionTime != "" {
		arg := fmt.Sprintf("%s=%s", "--max-node-provision-time", options.MaxNodeProvisionTime)
		args = append(args, arg)
	}

	if options.PodPriorityThreshold != nil {
		arg := fmt.Sprintf("%s=%d", "--expendable-pods-priority-cutoff", *options.PodPriorityThreshold)
		args = append(args, arg)
	}

	labels := map[string]string{
		"app":                              autoscalerName,
		hyperv1.ControlPlaneComponentLabel: autoscalerName,
	}
	// The selector needs to be invariant for the lifecycle of the project as it's an immutable field,
	// otherwise changing would prevent an upgrade from happening.
	selector := map[string]string{
		"app": autoscalerName,
	}

	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: selector,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: labels,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName:            sa.Name,
				TerminationGracePeriodSeconds: ptr.To[int64](10),
				Tolerations: []corev1.Toleration{
					{
						Key:    "node-role.kubernetes.io/master",
						Effect: corev1.TaintEffectNoSchedule,
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "target-kubeconfig",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName:  kubeConfigSecret.Name,
								DefaultMode: ptr.To[int32](0640),
								Items: []corev1.KeyToPath{
									{
										// TODO: should the key be published on status?
										Key:  "value",
										Path: "target-kubeconfig",
									},
								},
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:            autoscalerName,
						Image:           clusterAutoscalerImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "target-kubeconfig",
								MountPath: "/mnt/kubeconfig",
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
						Resources: autoscalerResources,
						Command:   []string{"/usr/bin/cluster-autoscaler"},
						Args:      args,
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path:   "/health-check",
									Port:   intstr.FromInt(8085),
									Scheme: corev1.URISchemeHTTP,
								},
							},
							InitialDelaySeconds: 60,
							PeriodSeconds:       60,
							SuccessThreshold:    1,
							FailureThreshold:    5,
							TimeoutSeconds:      5,
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path:   "/health-check",
									Port:   intstr.FromInt(8085),
									Scheme: corev1.URISchemeHTTP,
								},
							},
							PeriodSeconds:    10,
							SuccessThreshold: 1,
							FailureThreshold: 3,
							TimeoutSeconds:   5,
						},
						Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8085}},
					},
				},
			},
		},
	}

	util.AvailabilityProber(kas.InClusterKASReadyURL(hcp.Spec.Platform.Type), availabilityProberImage, &deployment.Spec.Template.Spec)

	deploymentConfig := config.DeploymentConfig{
		AdditionalLabels: map[string]string{
			config.NeedManagementKASAccessLabel: "true",
		},
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
		SetDefaultSecurityContext: setDefaultSecurityContext,
	}
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		deploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}

	replicas := ptr.To(1)
	if _, exists := hcp.Annotations[hyperv1.DisableClusterAutoscalerAnnotation]; exists {
		replicas = ptr.To(0)
	}
	deploymentConfig.SetDefaults(hcp, nil, replicas)
	deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	deploymentConfig.ApplyTo(deployment)

	return nil
}

func ReconcileAutoscalerRole(role *rbacv1.Role, owner config.OwnerRef) error {
	owner.ApplyTo(role)
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"cluster.x-k8s.io"},
			Resources: []string{
				"machinedeployments",
				"machinedeployments/scale",
				"machines",
				"machinesets",
				"machinesets/scale",
				"machinepools",
				"machinepools/scale",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{"infrastructure.cluster.x-k8s.io"},
			Resources: []string{"*"},
			Verbs:     []string{"get", "list"},
		},
		{
			APIGroups: []string{"capi-provider.agent-install.openshift.io"},
			Resources: []string{"agentmachinetemplates"},
			Verbs:     []string{"get", "list"},
		},
	}
	return nil
}

func ReconcileAutoscalerRoleBinding(binding *rbacv1.RoleBinding, role *rbacv1.Role, sa *corev1.ServiceAccount, owner config.OwnerRef) error {
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

// ReconcileAutoscaler orchestrates reconciliation of autoscaler components.
func ReconcileAutoscaler(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane, autoscalerImage, availabilityProberImage string, createOrUpdate upsert.CreateOrUpdateFN, setDefaultSecurityContext bool, ownerRef config.OwnerRef) error {
	autoscalerRole := manifests.AutoscalerRole(hcp.Namespace)
	_, err := createOrUpdate(ctx, c, autoscalerRole, func() error {
		return ReconcileAutoscalerRole(autoscalerRole, ownerRef)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile autoscaler role: %w", err)
	}

	autoscalerServiceAccount := manifests.AutoscalerServiceAccount(hcp.Namespace)
	_, err = createOrUpdate(ctx, c, autoscalerServiceAccount, func() error {
		util.EnsurePullSecret(autoscalerServiceAccount, controlplaneoperator.PullSecret("").Name)
		ownerRef.ApplyTo(autoscalerServiceAccount)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile autoscaler service account: %w", err)
	}

	autoscalerRoleBinding := manifests.AutoscalerRoleBinding(hcp.Namespace)
	_, err = createOrUpdate(ctx, c, autoscalerRoleBinding, func() error {
		return ReconcileAutoscalerRoleBinding(autoscalerRoleBinding, autoscalerRole, autoscalerServiceAccount, ownerRef)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile autoscaler role binding: %w", err)
	}

	// The deployment depends on the kubeconfig being reported.
	if hcp.Status.KubeConfig != nil {
		// Resolve the kubeconfig secret for CAPI which the
		// autoscaler is deployed alongside of.
		capiKubeConfigSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hcp.Namespace,
				Name:      fmt.Sprintf("%s-kubeconfig", hcp.Spec.InfraID),
			},
		}
		err = c.Get(ctx, client.ObjectKeyFromObject(capiKubeConfigSecret), capiKubeConfigSecret)
		if err != nil {
			return fmt.Errorf("failed to get hosted controlplane kubeconfig secret %q: %w", capiKubeConfigSecret.Name, err)
		}

		autoscalerDeployment := manifests.AutoscalerDeployment(hcp.Namespace)
		_, err = createOrUpdate(ctx, c, autoscalerDeployment, func() error {
			return ReconcileAutoscalerDeployment(autoscalerDeployment, hcp, autoscalerServiceAccount, capiKubeConfigSecret, hcp.Spec.Autoscaling, autoscalerImage, availabilityProberImage, setDefaultSecurityContext, ownerRef)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile autoscaler deployment: %w", err)
		}
	}

	return nil
}

const BalancingIgnoreLabelArg = "--balancing-ignore-label"

// AWS cloud provider ignore labels for the autoscaler.
const (
	// AwsIgnoredLabelEbsCsiZone is a label used by the AWS EBS CSI driver as a target for Persistent Volume Node Affinity.
	AwsIgnoredLabelEbsCsiZone = "topology.ebs.csi.aws.com/zone"
)

// IBM cloud provider ignore labels for the autoscaler.
const (
	// IbmcloudIgnoredLabelWorkerId is a label used by the IBM Cloud Cloud Controller Manager.
	IbmcloudIgnoredLabelWorkerId = "ibm-cloud.kubernetes.io/worker-id"

	// IbmcloudIgnoredLabelVpcBlockCsi is a label used by the IBM Cloud CSI driver as a target for Persistent Volume Node Affinity.
	IbmcloudIgnoredLabelVpcBlockCsi = "vpc-block-csi-driver-labels"
)

// Azure cloud provider ignore labels for the autoscaler.
const (
	// AzureDiskTopologyKey is the topology key of Azure Disk CSI driver.
	AzureDiskTopologyKey = "topology.disk.csi.azure.com/zone"
)

func GetIgnoreLabels() []string {
	return []string{
		// Hypershift
		"hypershift.openshift.io/nodePool",
		// AWS
		AwsIgnoredLabelEbsCsiZone,
		// Azure
		AzureDiskTopologyKey,
		// IBM
		IbmcloudIgnoredLabelWorkerId,
		IbmcloudIgnoredLabelVpcBlockCsi,
	}
}
