package util

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hccomanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
					SecurityContext: &corev1.PodSecurityContext{},
					DNSPolicy:       corev1.DNSDefault,
					Tolerations:     []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
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
									corev1.ResourceMemory: resource.MustParse("40Mi"),
									corev1.ResourceCPU:    resource.MustParse("20m"),
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

// VerifyKubeletConfigWithDaemonSet implements complete verification using DaemonSet.
// expectedNodeCount should be the NodePool replicas
func VerifyKubeletConfigWithDaemonSet(t *testing.T, ctx context.Context, guestClient crclient.Client, dsImage string, expectedNodeCount int32) {
	g := NewWithT(t)

	// Create the DaemonSet
	t.Log("Creating kubelet config verifier DaemonSet")
	err := CreateKubeletConfigVerifierDaemonSet(ctx, guestClient, dsImage)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubelet config verifier DaemonSet")

	// Wait for all DaemonSets to be ready using NodePool replicas as authoritative source
	t.Log("Waiting for OVN, GlobalPullSecret, Konnectivity and kubelet config verifier DaemonSets to be ready")

	g.Expect(waitForDaemonSetReady(t, ctx, guestClient, "ovnkube-node", "openshift-ovn-kubernetes", expectedNodeCount)).To(Succeed())
	g.Expect(waitForDaemonSetReady(t, ctx, guestClient, hccomanifests.GlobalPullSecretDSName, hccomanifests.GlobalPullSecretNamespace, expectedNodeCount)).To(Succeed())
	konnectivityDS := hccomanifests.KonnectivityAgentDaemonSet()
	g.Expect(waitForDaemonSetReady(t, ctx, guestClient, konnectivityDS.Name, konnectivityDS.Namespace, expectedNodeCount)).To(Succeed())
	g.Expect(waitForDaemonSetReady(t, ctx, guestClient, KubeletConfigVerifierDaemonSetName, KubeletConfigVerifierNamespace, expectedNodeCount)).To(Succeed())

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
