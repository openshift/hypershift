package util

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hccomanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	KubeletConfigVerifierDaemonSetName = "kubelet-config-verifier"
	KubeletConfigVerifierNamespace     = "kube-system"
	NodePullSecretPath                 = "/var/lib/kubelet/config.json"
)

// CreateKubeletConfigVerifierDaemonSet creates a DaemonSet that mounts the
// kubelet config directory on each node and uses a readiness probe to compare
// the on-disk pull secret against the cluster's original-pull-secret.
// The DS mounts kube-system/original-pull-secret — the same secret the
// global-pull-secret-syncer uses as its source — so it tracks the live
// feature state instead of a point-in-time snapshot.
// Stale resources from a previous failed run are cleaned up before creation.
func CreateKubeletConfigVerifierDaemonSet(ctx context.Context, guestClient crclient.Client, dsImage string) error {
	originalPS := hccomanifests.OriginalPullSecret()
	if err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(originalPS), originalPS); err != nil {
		return fmt.Errorf("failed to get %s/%s: %w", originalPS.Namespace, originalPS.Name, err)
	}

	// Clean up any stale DaemonSet left by a previous failed run
	staleDS := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeletConfigVerifierDaemonSetName,
			Namespace: KubeletConfigVerifierNamespace,
		},
	}
	if err := guestClient.Delete(ctx, staleDS); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete stale DaemonSet: %w", err)
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
							Command: []string{"/bin/sh", "-c", fmt.Sprintf(
								`while true; do `+
									`node=$(printf '%%s' "$(cat %s 2>/dev/null)" | md5sum | cut -d' ' -f1 || echo UNAVAILABLE) && `+
									`cluster=$(printf '%%s' "$(cat /etc/pull-secret/config.json 2>/dev/null)" | md5sum | cut -d' ' -f1 || echo UNAVAILABLE) && `+
									`echo "$(date -u +%%H:%%M:%%S) node=$node cluster=$cluster match=$([ "$node" = "$cluster" ] && echo yes || echo no)"; `+
									`sleep 10; `+
									`done`, NodePullSecretPath)},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"/bin/sh", "-c",
											fmt.Sprintf(`test "$(printf '%%s' "$(cat %s 2>/dev/null)" | md5sum | cut -d' ' -f1 || echo FAIL_NODE)" = "$(printf '%%s' "$(cat /etc/pull-secret/config.json 2>/dev/null)" | md5sum | cut -d' ' -f1 || echo FAIL_CLUSTER)"`,
												NodePullSecretPath)},
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
								FailureThreshold:    30,
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
									SecretName: originalPS.Name,
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

// VerifyKubeletConfigWithDaemonSet deploys a DaemonSet on every node whose
// readiness probe compares the on-disk kubelet config.json against the cluster
// pull secret. Pods only become Ready when the hashes match, so waiting for
// DaemonSet readiness IS the assertion that secrets are consistent on all nodes.
//
// Before deploying the verifier, waits for the global-pull-secret-syncer DS to
// complete its rollout. When HCCO updates original-pull-secret, it recalculates
// the configSeed hash and triggers a syncer pod restart. Without this wait, the
// verifier could start comparing hashes while the syncer is still restarting and
// the on-disk config.json has stale content.
func VerifyKubeletConfigWithDaemonSet(t *testing.T, ctx context.Context, guestClient crclient.Client, dsImage string, expectedNodeCount int32) {
	g := NewWithT(t)

	t.Cleanup(func() {
		t.Log("Cleaning up kubelet config verifier DaemonSet")
		ds := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      KubeletConfigVerifierDaemonSetName,
				Namespace: KubeletConfigVerifierNamespace,
			},
		}
		if err := guestClient.Delete(ctx, ds); err != nil && !apierrors.IsNotFound(err) {
			t.Logf("Warning: failed to clean up DaemonSet: %v", err)
		}
	})

	t.Log("Waiting for syncer DS rollout to complete before deploying verifier")
	g.Expect(waitForDaemonSetRollout(t, ctx, guestClient, hccomanifests.GlobalPullSecretDSName, hccomanifests.GlobalPullSecretNamespace, expectedNodeCount)).To(Succeed())

	t.Log("Creating kubelet config verifier DaemonSet")
	err := CreateKubeletConfigVerifierDaemonSet(ctx, guestClient, dsImage)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubelet config verifier DaemonSet")

	t.Log("Waiting for OVN, GlobalPullSecret, Konnectivity and kubelet config verifier DaemonSets to be ready")
	g.Expect(waitForDaemonSetReady(t, ctx, guestClient, "ovnkube-node", "openshift-ovn-kubernetes", expectedNodeCount)).To(Succeed())
	g.Expect(waitForDaemonSetReady(t, ctx, guestClient, hccomanifests.GlobalPullSecretDSName, hccomanifests.GlobalPullSecretNamespace, expectedNodeCount)).To(Succeed())
	konnectivityDS := hccomanifests.KonnectivityAgentDaemonSet()
	g.Expect(waitForDaemonSetReady(t, ctx, guestClient, konnectivityDS.Name, konnectivityDS.Namespace, expectedNodeCount)).To(Succeed())
	g.Expect(waitForDaemonSetReady(t, ctx, guestClient, KubeletConfigVerifierDaemonSetName, KubeletConfigVerifierNamespace, expectedNodeCount)).To(Succeed())
	t.Log("All verifier pods are Ready — on-disk kubelet config.json matches cluster pull secret on all nodes")
}

// waitForDaemonSetRollout waits for a DaemonSet to complete its rollout: all pods
// must be updated with the latest template AND ready. This catches the case where
// HCCO updates the pod template (e.g. new configSeed) and the DS controller is
// still replacing pods.
func waitForDaemonSetRollout(t *testing.T, ctx context.Context, client crclient.Client, name, namespace string, minExpected int32) error {
	t.Logf("Waiting for %s DaemonSet rollout to complete (min expected: %d)", name, minExpected)

	return wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		ds := &appsv1.DaemonSet{}
		if err := client.Get(ctx, crclient.ObjectKey{Name: name, Namespace: namespace}, ds); err != nil {
			t.Logf("Failed to get DaemonSet %s: %v", name, err)
			return false, nil
		}

		if ds.Status.ObservedGeneration < ds.Generation {
			t.Logf("DaemonSet %s generation %d not yet observed (current %d)", name, ds.Generation, ds.Status.ObservedGeneration)
			return false, nil
		}

		desired := ds.Status.DesiredNumberScheduled
		if desired < minExpected {
			t.Logf("DaemonSet %s: desired=%d < minExpected=%d", name, desired, minExpected)
			return false, nil
		}

		updated := ds.Status.UpdatedNumberScheduled
		ready := ds.Status.NumberReady

		if updated != desired || ready != desired {
			t.Logf("DaemonSet %s rollout: %d/%d updated, %d/%d ready", name, updated, desired, ready, desired)
			return false, nil
		}

		t.Logf("DaemonSet %s rollout complete: %d/%d pods updated and ready", name, ready, desired)
		return true, nil
	})
}
