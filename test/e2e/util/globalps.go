package util

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	KubeletConfigVerifierDaemonSetName = "kubelet-config-verifier"
	KubeletConfigVerifierNamespace     = "kube-system"
	NodePullSecretPath                 = "/var/lib/kubelet/config.json"
)

// CreateKubeletConfigVerifierDaemonSet creates a DaemonSet that verifies the config.json file
// on all nodes of the cluster, comparing it with the cluster's pull secret
func CreateKubeletConfigVerifierDaemonSet(ctx context.Context, guestClient crclient.Client, dsImage string) error {
	// Get the cluster's pull secret for comparison
	pullSecret := &corev1.Secret{}
	if err := guestClient.Get(ctx, crclient.ObjectKey{Name: "pull-secret", Namespace: "openshift-config"}, pullSecret); err != nil {
		return fmt.Errorf("failed to get pull secret: %w", err)
	}

	newPullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: KubeletConfigVerifierNamespace,
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: pullSecret.Data,
	}

	// replicate pull secret in kube-system namespace
	if err := guestClient.Create(ctx, newPullSecret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create pull secret: %w", err)
		}
	}

	daemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeletConfigVerifierDaemonSetName,
			Namespace: KubeletConfigVerifierNamespace,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": KubeletConfigVerifierDaemonSetName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": KubeletConfigVerifierDaemonSetName,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           manifests.GlobalPullSecretDSName,
					AutomountServiceAccountToken: ptr.To(true),
					SecurityContext:              &corev1.PodSecurityContext{},
					DNSPolicy:                    corev1.DNSDefault,
					Tolerations:                  []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					Containers: []corev1.Container{
						{
							Name:            KubeletConfigVerifierDaemonSetName,
							Image:           dsImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command: []string{
								"/bin/sh", "-c",
							},
							Args: []string{
								fmt.Sprintf(`
									echo "Starting pull secret verification..."
									echo "Checking node path: %s"
									echo "Checking cluster pull secret path: /etc/pull-secret/config.json"

									# Check 1: Verify that the pull secret exists on the node
									if [ ! -f %s ]; then
										echo "ERROR: Pull secret does not exist at %s"
										exit 1
									fi

									echo "SUCCESS: Pull secret file exists at %s"
									echo "File size: $(stat -c%%s %s 2>/dev/null || stat -f%%z %s 2>/dev/null) bytes"

									# Verify that the file contains valid JSON structure (basic check)
									if ! grep -q '"auths"' %s; then
										echo "ERROR: config.json does not contain 'auths' field"
										echo "File content (first 500 chars):"
										head -c 500 %s || echo "Cannot read file"
										exit 1
									fi

									echo "SUCCESS: config.json contains 'auths' field"

									# Check 2: Compare if both pull secrets are equal
									if [ -f /etc/pull-secret/config.json ]; then
										echo "Cluster pull secret exists, comparing files..."

										# Get MD5 hashes of both files
										node_hash=$(md5sum %s 2>/dev/null | cut -d' ' -f1 || echo "FAILED")
										cluster_hash=$(md5sum /etc/pull-secret/config.json 2>/dev/null | cut -d' ' -f1 || echo "FAILED")

										if [ "$node_hash" = "FAILED" ] || [ "$cluster_hash" = "FAILED" ]; then
											echo "ERROR: Failed to calculate hashes"
											echo "Node file readable: $(test -r %s && echo "YES" || echo "NO")"
											echo "Cluster file readable: $(test -r /etc/pull-secret/config.json && echo "YES" || echo "NO")"
											exit 1
										fi

										if [ "$node_hash" = "$cluster_hash" ]; then
											echo "SUCCESS: Pull secrets are identical (MD5: $node_hash)"
										else
											echo "ERROR: Pull secrets are different"
											echo "Node pull secret MD5: $node_hash"
											echo "Cluster pull secret MD5: $cluster_hash"
											exit 1
										fi
									else
										echo "ERROR: Cluster pull secret not available for comparison"
										echo "Files in /etc/pull-secret:"
										ls -la /etc/pull-secret/ || echo "Cannot list /etc/pull-secret"
										exit 1
									fi

									echo "SUCCESS: Pull secret verification completed"

									# Keep the pod running with infinite loop
									echo "Starting infinite loop to keep pod running..."
									while true; do
										echo "Pod is still running... $(date)"
										sleep 30
									done
								`, NodePullSecretPath, NodePullSecretPath, NodePullSecretPath, NodePullSecretPath, NodePullSecretPath, NodePullSecretPath, NodePullSecretPath, NodePullSecretPath, NodePullSecretPath, NodePullSecretPath),
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: ptr.To(true),
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "kubelet-config",
									MountPath: "/var/lib/kubelet",
								},
								{
									Name:      "pull-secret",
									MountPath: "/etc/pull-secret",
									ReadOnly:  true,
								},
							},
							TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("50Mi"),
									corev1.ResourceCPU:    resource.MustParse("40m"),
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "kubelet-config",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/kubelet",
									Type: ptr.To(corev1.HostPathDirectory),
								},
							},
						},
						{
							Name: "pull-secret",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "pull-secret",
									Items: []corev1.KeyToPath{
										{
											Key:  corev1.DockerConfigJsonKey,
											Path: "config.json",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return guestClient.Create(ctx, daemonSet)
}

// WaitForKubeletConfigVerifierDaemonSet waits for the DaemonSet to be ready
func WaitForKubeletConfigVerifierDaemonSet(ctx context.Context, guestClient crclient.Client) error {
	return wait.PollUntilContextTimeout(ctx, 10*time.Second, 20*time.Minute, true,
		func(ctx context.Context) (done bool, err error) {
			ds := &appsv1.DaemonSet{}
			if err := guestClient.Get(ctx, crclient.ObjectKey{Name: KubeletConfigVerifierDaemonSetName, Namespace: KubeletConfigVerifierNamespace}, ds); err != nil {
				return false, err
			}
			return ds.Status.NumberReady == ds.Status.DesiredNumberScheduled, nil
		})
}

// VerifyKubeletConfigWithDaemonSet implements complete verification using DaemonSet
func VerifyKubeletConfigWithDaemonSet(t *testing.T, ctx context.Context, guestClient crclient.Client, dsImage string) {
	g := NewWithT(t)

	// Create the DaemonSet
	t.Log("Creating kubelet config verifier DaemonSet")
	err := CreateKubeletConfigVerifierDaemonSet(ctx, guestClient, dsImage)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubelet config verifier DaemonSet")

	// Wait for the DaemonSet to be ready
	t.Log("Waiting for DaemonSet to be ready")
	err = WaitForKubeletConfigVerifierDaemonSet(ctx, guestClient)
	g.Expect(err).NotTo(HaveOccurred(), "failed to wait for kubelet config verifier DaemonSet")

	// Verify that all DaemonSet pods are running
	t.Log("Verifying all DaemonSet pods are running")
	EventuallyObjects(t, ctx, "DaemonSet pods to be running", func(ctx context.Context) ([]*corev1.Pod, error) {
		pods := &corev1.PodList{}
		err := guestClient.List(ctx, pods, &crclient.ListOptions{
			Namespace: KubeletConfigVerifierNamespace,
			LabelSelector: labels.Set(map[string]string{
				"name": KubeletConfigVerifierDaemonSetName,
			}).AsSelector(),
		})
		if err != nil {
			return nil, err
		}
		var items []*corev1.Pod
		for i := range pods.Items {
			items = append(items, &pods.Items[i])
		}
		return items, nil
	}, nil, []Predicate[*corev1.Pod]{func(pod *corev1.Pod) (done bool, reasons string, err error) {
		return pod.Status.Phase == corev1.PodRunning, fmt.Sprintf("Pod has phase %s", pod.Status.Phase), nil
	}}, WithInterval(5*time.Second), WithTimeout(30*time.Minute))

	// Clean up the DaemonSet after verification
	t.Log("Cleaning up kubelet config verifier DaemonSet")
	daemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeletConfigVerifierDaemonSetName,
			Namespace: KubeletConfigVerifierNamespace,
		},
	}
	g.Expect(guestClient.Delete(ctx, daemonSet)).To(Succeed())

	// Clean up pull-secret secret in kube-system namespace
	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: KubeletConfigVerifierNamespace,
		},
	}
	g.Expect(guestClient.Delete(ctx, pullSecret)).To(Succeed())
}

// GetKubeletConfigVerifierLogs gets logs from all pods of the kubelet config verifier DaemonSet
func GetKubeletConfigVerifierLogs(ctx context.Context, guestClient crclient.Client) (map[string]string, error) {
	pods := &corev1.PodList{}
	err := guestClient.List(ctx, pods, &crclient.ListOptions{
		Namespace: KubeletConfigVerifierNamespace,
		LabelSelector: labels.Set(map[string]string{
			"name": KubeletConfigVerifierDaemonSetName,
		}).AsSelector(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list verifier pods: %w", err)
	}

	logs := make(map[string]string)
	for _, pod := range pods.Items {
		// Get logs from the container
		logs[pod.Name] = fmt.Sprintf("Pod Phase: %s\n", pod.Status.Phase)

		// Add container status information
		for _, container := range pod.Status.ContainerStatuses {
			logs[pod.Name] += fmt.Sprintf("Container %s: Ready=%v, RestartCount=%d\n",
				container.Name, container.Ready, container.RestartCount)

			if container.State.Terminated != nil {
				logs[pod.Name] += fmt.Sprintf("Exit Code: %d, Reason: %s\n",
					container.State.Terminated.ExitCode, container.State.Terminated.Reason)
			}
		}
	}

	return logs, nil
}
