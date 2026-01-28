package connectionchecker

import (
	"context"
	"fmt"
	"net"

	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	pauseImage                 = "registry.k8s.io/pause:3.9"
	systemNodeCriticalPriority = "system-node-critical"
)

// ReconcileDaemonSet creates or updates the KAS connection checker DaemonSet.
// The DaemonSet deploys lightweight pause container pods on all worker nodes with
// readiness probes that check connectivity to the KAS advertise address.
func ReconcileDaemonSet(
	ctx context.Context,
	daemonSet *appsv1.DaemonSet,
	kasIP string,
	kasPort int32,
	endpoint string,
	createOrUpdate upsert.CreateOrUpdateFN,
	client crclient.Client,
) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling KAS connection checker DaemonSet")

	if _, err := createOrUpdate(ctx, client, daemonSet, func() error {
		daemonSet.Spec = appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "kas-connection-checker",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "kas-connection-checker",
					},
				},
				Spec: corev1.PodSpec{
					// AutomountServiceAccountToken is false because the pod doesn't need API access
					AutomountServiceAccountToken: ptr.To(false),
					// HostNetwork is required to access the loopback-bound advertise address (172.20.0.1 or fd00::1)
					HostNetwork: true,
					// DNSPolicy must be ClusterFirstWithHostNet when using hostNetwork
					DNSPolicy: corev1.DNSClusterFirstWithHostNet,
					// PriorityClassName ensures the pod is not evicted under resource pressure
					PriorityClassName: systemNodeCriticalPriority,
					// Tolerations allow the pod to run on all nodes, including those with taints
					Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					Containers: []corev1.Container{
						{
							Name:            "kas-connection-checker",
							Image:           pauseImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							// Readiness probe checks KAS connectivity via HTTPS GET
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Scheme: corev1.URISchemeHTTPS,
										// Host is the advertise address (172.20.0.1 or fd00::1)
										// This address is bound to the loopback interface on each worker node
										Host: kasIP,
										// Port is the KAS pod port (typically 6443)
										Port: intstr.FromInt(int(kasPort)),
										// Path is platform-specific: /version for most, /livez for IBM Cloud
										Path: endpoint,
									},
								},
								// InitialDelaySeconds gives the node time to set up the advertise address
								InitialDelaySeconds: 5,
								// TimeoutSeconds matches the curl timeout from the old implementation
								TimeoutSeconds: 10,
								// PeriodSeconds determines how often the probe runs
								PeriodSeconds: 10,
								// SuccessThreshold of 1 means the pod becomes ready after 1 successful check
								SuccessThreshold: 1,
								// FailureThreshold of 3 means the pod becomes not ready after 3 failed checks
								FailureThreshold: 3,
							},
							// TerminationMessagePolicy ensures we get logs if the container fails
							TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
							// Resources are minimal since the pause container does nothing except sleep
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("10Mi"),
									corev1.ResourceCPU:    resource.MustParse("5m"),
								},
							},
						},
					},
				},
			},
		}

		return nil
	}); err != nil {
		return fmt.Errorf("failed to create KAS connection checker daemon set: %w", err)
	}

	log.Info("Successfully reconciled KAS connection checker DaemonSet",
		"kasIP", kasIP,
		"kasPort", kasPort,
		"endpoint", endpoint,
		"url", fmt.Sprintf("https://%s/%s", net.JoinHostPort(kasIP, fmt.Sprintf("%d", kasPort)), endpoint))

	return nil
}
