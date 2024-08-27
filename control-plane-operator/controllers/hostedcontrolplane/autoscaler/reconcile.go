package autoscaler

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	autoscalerName = "cluster-autoscaler"

	ImageStreamAutoscalerImage = "cluster-autoscaler"

	kubeconfigVolumeName = "kubeconfig"
	kubeconfigKey        = "target-kubeconfig"
)

var _ component.DeploymentReconciler = &AutoscalerReconciler{}

type AutoscalerReconciler struct {
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(&AutoscalerReconciler{}).
		WithRBAC(autoscalerRoleRules()).
		WithPredicate(Predicate).
		NeedsManagementKASAccess().
		Build()
}

// Name implements controlplanecomponent.DeploymentReconciler.
func (a *AutoscalerReconciler) Name() string {
	return autoscalerName
}

// Volumes implements controlplanecomponent.DeploymentReconciler.
func (a *AutoscalerReconciler) Volumes(cpContext component.ControlPlaneContext) component.Volumes {
	volumeSource := component.SecretVolumeSource(manifests.KASServiceCAPIKubeconfigSecret(cpContext.HCP.Namespace, cpContext.HCP.Spec.InfraID).Name)
	volumeSource.Secret.Items = []corev1.KeyToPath{
		{
			// TODO: should the key be published on status?
			Key:  "value",
			Path: kubeconfigKey,
		},
	}

	return component.Volumes{
		kubeconfigVolumeName: component.Volume{
			Source: volumeSource,
			Mounts: map[string]string{
				autoscalerName: "/mnt/kubeconfig",
			},
		},
	}
}

func Predicate(cpContext component.ControlPlaneContext) (bool, error) {
	hcp := cpContext.HCP

	// Disable cluster-autoscaler component if DisableMachineManagement label is set.
	if _, exists := hcp.Annotations[hyperv1.DisableMachineManagement]; exists {
		return false, nil
	}

	// The deployment depends on the kubeconfig being reported.
	if hcp.Status.KubeConfig == nil {
		return false, nil
	}
	// Resolve the kubeconfig secret for CAPI which the autoscaler is deployed alongside of.
	capiKubeConfigSecret := manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(capiKubeConfigSecret), capiKubeConfigSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get hosted controlplane kubeconfig secret %q: %w", capiKubeConfigSecret.Name, err)
	}

	return true, nil
}

// reconcileDeployment implements controlplanecomponent.DeploymentReconciler.
func (a *AutoscalerReconciler) ReconcileDeployment(cpContext component.ControlPlaneContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	clusterAutoscalerImage := cpContext.ReleaseImageProvider.GetImage(ImageStreamAutoscalerImage)
	availabilityProberImage := cpContext.ReleaseImageProvider.GetImage(util.AvailabilityProberImageName)

	labels := map[string]string{
		"app":                         autoscalerName,
		hyperv1.ControlPlaneComponent: autoscalerName,
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
				ServiceAccountName:            autoscalerName,
				TerminationGracePeriodSeconds: ptr.To[int64](10),
				Tolerations: []corev1.Toleration{
					{
						Key:    "node-role.kubernetes.io/master",
						Effect: corev1.TaintEffectNoSchedule,
					},
				},
				Containers: []corev1.Container{
					buildContainer(clusterAutoscalerImage, hcp.Spec.Autoscaling, a.Volumes(cpContext)),
				},
			},
		},
	}

	util.AvailabilityProber(kas.InClusterKASReadyURL(hcp.Spec.Platform.Type), availabilityProberImage, &deployment.Spec.Template.Spec)

	deployment.Spec.Replicas = ptr.To[int32](1)
	if _, exists := hcp.Annotations[hyperv1.DisableClusterAutoscalerAnnotation]; exists {
		deployment.Spec.Replicas = ptr.To[int32](0)
	}

	return nil
}

func buildContainer(image string, options hyperv1.ClusterAutoscaling, volumes component.Volumes) corev1.Container {
	builder := component.NewContainer(autoscalerName).
		Image(image).
		Command("/usr/bin/cluster-autoscaler").
		WithEnv("MY_NAMESPACE", &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath: "metadata.namespace",
			},
		}).
		WithPort(corev1.ContainerPort{
			Name:          "metrics",
			ContainerPort: 8085,
		}).
		WithMemoryResourcesRequest(resource.MustParse("60Mi")).
		WithCPUResourcesRequest(resource.MustParse("10m")).
		WithHTTPLivnessProbe(&corev1.HTTPGetAction{
			Path:   "/health-check",
			Port:   intstr.FromInt(8085),
			Scheme: corev1.URISchemeHTTP,
		}).
		WithHTTPReadinessProbe(&corev1.HTTPGetAction{
			Path:   "/health-check",
			Port:   intstr.FromInt(8085),
			Scheme: corev1.URISchemeHTTP,
		}).
		WithArgs(
			"--expander=priority,least-waste",
			"--cloud-provider=clusterapi",
			"--node-group-auto-discovery=clusterapi:namespace=$(MY_NAMESPACE)",
			fmt.Sprintf("--kubeconfig=%s", path.Join(volumes.Path(autoscalerName, kubeconfigVolumeName), kubeconfigKey)),
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
		)

	ignoreLabels := GetIgnoreLabels()
	for _, v := range ignoreLabels {
		builder.WithArgs(fmt.Sprintf("%s=%v", BalancingIgnoreLabelArg, v))
	}

	// TODO if the options for the cluster autoscaler continues to grow, we should take inspiration
	// from the cluster-autoscaler-operator and create some utility functions for these assignments.
	if options.MaxNodesTotal != nil {
		builder.WithArgs(fmt.Sprintf("--max-nodes-total=%d", *options.MaxNodesTotal))
	}

	if options.MaxPodGracePeriod != nil {
		builder.WithArgs(fmt.Sprintf("--max-graceful-termination-sec=%d", *options.MaxPodGracePeriod))
	}

	if options.MaxNodeProvisionTime != "" {
		builder.WithArgs(fmt.Sprintf("--max-node-provision-time=%s", options.MaxNodeProvisionTime))
	}

	if options.PodPriorityThreshold != nil {
		builder.WithArgs(fmt.Sprintf("--expendable-pods-priority-cutoff=%d", *options.PodPriorityThreshold))
	}

	return builder.Build()
}

func autoscalerRoleRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
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
}

const BalancingIgnoreLabelArg = "--balancing-ignore-label"

// AWS cloud provider ignore labels for the autoscaler.
const (
	// AwsIgnoredLabelEbsCsiZone is a label used by the AWS EBS CSI driver as a target for Persistent Volume Node Affinity.
	AwsIgnoredLabelEbsCsiZone = "topology.ebs.csi.aws.com/zone"
)

// IBM cloud provider ignore labels for the autoscaler.
const (
	// IbmcloudIgnoredLabelWorkerId is a label used by the IBM Cloud Cloud Controler Manager.
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
