//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	etcdrecoverymanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/etcdrecovery"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterEtcdChaosTests registers all etcd chaos test cases.
func RegisterEtcdChaosTests(getTestCtx internal.TestContextGetter) {
	EtcdSingleMemberRecoveryTest(getTestCtx)
	EtcdKillRandomMembersTest(getTestCtx)
	EtcdKillAllMembersTest(getTestCtx)
	EtcdSingleMemberCorruptionTest(getTestCtx)
	EtcdMissingMemberRecoveryTest(getTestCtx)
}

var _ = Describe("Etcd Chaos", Label("lifecycle", "etcd-chaos"), Ordered, func() {
	var testCtx *internal.TestContext

	BeforeAll(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterEtcdChaosTests(func() *internal.TestContext { return testCtx })
})

// EtcdSingleMemberRecoveryTest deletes one random etcd pod and its PVC simultaneously,
// then verifies the pod is replaced (different UID) and the StatefulSet converges.
func EtcdSingleMemberRecoveryTest(getTestCtx internal.TestContextGetter) {
	It("should recover after a single member loses its data", func() {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace

		etcdSts, etcdPods := getEtcdStsAndPods(ctx, testCtx.MgmtClient, cpNamespace)

		randomPod := randomEtcdPods(etcdPods.Items, 1)[0]
		originalUID := randomPod.UID
		pvcName := "data-etcd" + strings.TrimPrefix(randomPod.Name, "etcd")
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: pvcName, Namespace: cpNamespace},
		}

		GinkgoWriter.Printf("Deleting etcd pod %s and PVC %s\n", randomPod.Name, pvcName)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			Expect(testCtx.MgmtClient.Delete(ctx, &randomPod)).To(Succeed(), "failed to delete etcd pod %s", randomPod.Name)
			GinkgoWriter.Printf("Deleted etcd pod %s\n", randomPod.Name)
		}()
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			Expect(testCtx.MgmtClient.Delete(ctx, pvc)).To(Succeed(), "failed to delete etcd PVC %s", pvcName)
			GinkgoWriter.Printf("Deleted etcd PVC %s\n", pvcName)
		}()
		wg.Wait()

		// Verify pod is replaced with a new UID
		e2eutil.EventuallyObject(GinkgoTB(), ctx, "deleted etcd pod is replaced",
			func(ctx context.Context) (*corev1.Pod, error) {
				pod := &corev1.Pod{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&randomPod), pod)
				return pod, err
			},
			[]e2eutil.Predicate[*corev1.Pod]{func(pod *corev1.Pod) (bool, string, error) {
				return originalUID != pod.UID, fmt.Sprintf("pod UID %s", pod.UID), nil
			}},
			e2eutil.WithInterval(5*time.Second),
			e2eutil.WithTimeout(30*time.Minute),
		)

		waitForEtcdConvergence(ctx, testCtx.MgmtClient, cpNamespace, ptr.Deref(etcdSts.Spec.Replicas, 0))
	})
}

// EtcdKillRandomMembersTest creates a marker ConfigMap in the hosted cluster,
// deletes random etcd pods every 5 seconds for 30 seconds, then verifies
// StatefulSet convergence and that the marker data survived.
func EtcdKillRandomMembersTest(getTestCtx internal.TestContextGetter) {
	It("should preserve data when random members are repeatedly killed", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace

		guestClient := testCtx.GetHostedClusterClient()

		// Create marker data that should survive the chaos
		markerCM := createMarkerConfigMap(ctx, guestClient)
		DeferCleanup(func() {
			if err := guestClient.Delete(ctx, markerCM); err != nil && !apierrors.IsNotFound(err) {
				GinkgoWriter.Printf("Warning: failed to cleanup marker ConfigMap: %v\n", err)
			}
		})

		etcdSts, etcdPods := getEtcdStsAndPods(ctx, testCtx.MgmtClient, cpNamespace)

		// Delete random etcd pods every 5s for 30s
		duration, period := 30*time.Second, 5*time.Second
		GinkgoWriter.Printf("Deleting random etcd pods every %s for %s\n", period, duration)
		deletionCount := 0
		deadline := time.Now().Add(duration)
		for time.Now().Before(deadline) {
			pod := randomEtcdPods(etcdPods.Items, 1)[0]
			err := testCtx.MgmtClient.Delete(ctx, &pod, &crclient.DeleteOptions{
				GracePeriodSeconds: ptr.To[int64](0),
			})
			if err != nil {
				GinkgoWriter.Printf("Warning: failed to delete pod %s: %v\n", pod.Name, err)
			} else {
				GinkgoWriter.Printf("Deleted pod %s\n", pod.Name)
				deletionCount++
			}
			time.Sleep(period)
		}
		Expect(deletionCount).To(BeNumerically(">", 0), "at least one pod deletion should have succeeded")

		waitForEtcdConvergence(ctx, testCtx.MgmtClient, cpNamespace, ptr.Deref(etcdSts.Spec.Replicas, 0))

		verifyMarkerSurvived(ctx, guestClient, markerCM)
	})
}

// EtcdKillAllMembersTest creates a marker ConfigMap, deletes ALL etcd pods simultaneously
// via goroutines, then verifies convergence and marker survival.
func EtcdKillAllMembersTest(getTestCtx internal.TestContextGetter) {
	It("should preserve data when all members are killed simultaneously", func() {
		testCtx := getTestCtx()
		testCtx.ValidateHostedClusterClient()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace

		guestClient := testCtx.GetHostedClusterClient()

		// Create marker data that should survive the chaos
		markerCM := createMarkerConfigMap(ctx, guestClient)
		DeferCleanup(func() {
			if err := guestClient.Delete(ctx, markerCM); err != nil && !apierrors.IsNotFound(err) {
				GinkgoWriter.Printf("Warning: failed to cleanup marker ConfigMap: %v\n", err)
			}
		})

		etcdSts, etcdPods := getEtcdStsAndPods(ctx, testCtx.MgmtClient, cpNamespace)

		// Delete all etcd pods simultaneously
		GinkgoWriter.Printf("Deleting all %d etcd pods simultaneously\n", len(etcdPods.Items))
		var wg sync.WaitGroup
		wg.Add(len(etcdPods.Items))
		for i := range etcdPods.Items {
			go func(pod *corev1.Pod) {
				defer GinkgoRecover()
				defer wg.Done()
				deleteCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				err := testCtx.MgmtClient.Delete(deleteCtx, pod, &crclient.DeleteOptions{
					GracePeriodSeconds: ptr.To[int64](0),
				})
				if err != nil {
					GinkgoWriter.Printf("Warning: failed to delete pod %s: %v\n", pod.Name, err)
				} else {
					GinkgoWriter.Printf("Deleted pod %s\n", pod.Name)
				}
			}(&etcdPods.Items[i])
		}
		wg.Wait()

		// Verify all etcd pods are replaced with new UIDs
		e2eutil.EventuallyObjects(GinkgoTB(), ctx, "etcd pods to be replaced",
			func(ctx context.Context) ([]*corev1.Pod, error) {
				pods := &corev1.PodList{}
				err := testCtx.MgmtClient.List(ctx, pods, &crclient.ListOptions{
					Namespace:     cpNamespace,
					LabelSelector: labels.Set(etcdSts.Spec.Selector.MatchLabels).AsSelector(),
				})
				items := make([]*corev1.Pod, len(pods.Items))
				for i := range pods.Items {
					items[i] = &pods.Items[i]
				}
				return items, err
			},
			nil,
			[]e2eutil.Predicate[*corev1.Pod]{func(pod *corev1.Pod) (bool, string, error) {
				for _, previousPod := range etcdPods.Items {
					if previousPod.Namespace == pod.Namespace && previousPod.Name == pod.Name {
						return previousPod.UID != pod.UID, fmt.Sprintf("pod UID %s", pod.UID), nil
					}
				}
				return false, "pod not found in previous list", nil
			}},
			e2eutil.WithInterval(5*time.Second),
			e2eutil.WithTimeout(30*time.Minute),
		)

		waitForEtcdConvergence(ctx, testCtx.MgmtClient, cpNamespace, ptr.Deref(etcdSts.Spec.Replicas, 0))

		verifyMarkerSurvived(ctx, guestClient, markerCM)
	})
}

// EtcdSingleMemberCorruptionTest destroys a random member's data directory using
// RunCommandInPod, then waits for etcd to crash in-place so the recovery
// controller detects the failing member and creates a recovery job.
func EtcdSingleMemberCorruptionTest(getTestCtx internal.TestContextGetter) {
	It("should recover after a single member's data is corrupted", func() {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace

		etcdSts, etcdPods := getEtcdStsAndPods(ctx, testCtx.MgmtClient, cpNamespace)
		if ptr.Deref(etcdSts.Spec.Replicas, 0) < 3 {
			Skip("etcd corruption recovery requires HighlyAvailable etcd (>= 3 replicas)")
		}

		pod := randomEtcdPods(etcdPods.Items, 1)[0]
		// Remove the entire member directory so etcd cannot start.
		// Deleting only a single WAL file is insufficient because etcd
		// can recover from partial WAL loss using its snapshot database.
		// Do NOT delete the pod afterward — let etcd crash and restart
		// in-place so RestartCount increments on the same pod. The
		// recovery controller requires RestartCount > 0 to detect a
		// failing member; deleting the pod resets RestartCount to 0.
		command := `rm -rf /var/lib/data/member`

		GinkgoWriter.Printf("Destroying data directory on etcd pod: %s\n", pod.Name)
		_, err := e2eutil.RunCommandInPod(ctx, testCtx.MgmtClient, "etcd", pod.Namespace, []string{"/bin/sh", "-c", command}, "etcd", 5*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "failed to destroy data directory on etcd pod %s", pod.Name)

		// Etcd recovery job should be created.
		// We don't check if the job completed because it will be deleted after completion.
		e2eutil.EventuallyObject(GinkgoTB(), ctx, "etcd recovery job to be active",
			func(ctx context.Context) (*batchv1.Job, error) {
				recoveryJob := etcdrecoverymanifests.EtcdRecoveryJob(cpNamespace)
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(recoveryJob), recoveryJob)
				return recoveryJob, err
			},
			[]e2eutil.Predicate[*batchv1.Job]{func(job *batchv1.Job) (bool, string, error) {
				got := job.Status.Active
				return got == 1, fmt.Sprintf("wanted status active to be 1, got %d", got), nil
			}},
			e2eutil.WithInterval(5*time.Second),
			e2eutil.WithTimeout(15*time.Minute),
		)

		waitForEtcdConvergence(ctx, testCtx.MgmtClient, cpNamespace, ptr.Deref(etcdSts.Spec.Replicas, 0))
	})
}

// EtcdMissingMemberRecoveryTest removes a member from the etcd cluster via
// etcdctl member remove, deletes the pod, verifies the recovery job,
// and waits for StatefulSet convergence.
func EtcdMissingMemberRecoveryTest(getTestCtx internal.TestContextGetter) {
	It("should recover after a member is removed from the etcd cluster", func() {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace

		etcdSts, etcdPods := getEtcdStsAndPods(ctx, testCtx.MgmtClient, cpNamespace)
		if ptr.Deref(etcdSts.Spec.Replicas, 0) < 3 {
			Skip("etcd missing member recovery requires HighlyAvailable etcd (>= 3 replicas)")
		}

		pod := randomEtcdPods(etcdPods.Items, 1)[0]
		ep := fmt.Sprintf("https://etcd-client.%s.svc:2379", cpNamespace)

		// Step 1: Discover the member ID
		discoverCommand := []string{
			"/bin/sh", "-c",
			fmt.Sprintf("/usr/bin/etcdctl --cacert=/etc/etcd/tls/etcd-ca/ca.crt --cert=/etc/etcd/tls/server/server.crt --key=/etc/etcd/tls/server/server.key --endpoints=%s member list | grep %s | awk '{print $1}' | tr -d ,", ep, pod.Name),
		}

		GinkgoWriter.Printf("Discovering member ID for: %s\n", pod.Name)
		memberID, err := e2eutil.RunCommandInPod(ctx, testCtx.MgmtClient, "etcd", pod.Namespace, discoverCommand, "etcd", 5*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "failed to discover etcd member ID for %s", pod.Name)
		memberID = strings.TrimSpace(memberID)
		Expect(memberID).NotTo(BeEmpty(), "member ID should not be empty for %s", pod.Name)

		// Step 2: Remove the member
		removeCommand := []string{
			"/usr/bin/etcdctl",
			"--cacert=/etc/etcd/tls/etcd-ca/ca.crt",
			"--cert=/etc/etcd/tls/server/server.crt",
			"--key=/etc/etcd/tls/server/server.key",
			fmt.Sprintf("--endpoints=%s", ep),
			"member", "remove", memberID,
		}

		GinkgoWriter.Printf("Removing etcd member %s (ID: %s)\n", pod.Name, memberID)
		cmdStdout, err := e2eutil.RunCommandInPod(ctx, testCtx.MgmtClient, "etcd", pod.Namespace, removeCommand, "etcd", 5*time.Minute)
		Expect(err).NotTo(HaveOccurred(), "failed to remove etcd member %s", pod.Name)
		Expect(cmdStdout).NotTo(ContainSubstring("Error:"), "failed to remove etcd member %s", pod.Name)

		GinkgoWriter.Printf("Deleting pod: %s\n", pod.Name)
		Expect(testCtx.MgmtClient.Delete(ctx, &pod)).To(Succeed(), "failed to delete pod %s", pod.Name)

		// Etcd recovery job should be created.
		// We don't check if the job completed because it will be deleted after completion.
		e2eutil.EventuallyObject(GinkgoTB(), ctx, "etcd recovery job to be active",
			func(ctx context.Context) (*batchv1.Job, error) {
				recoveryJob := etcdrecoverymanifests.EtcdRecoveryJob(cpNamespace)
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(recoveryJob), recoveryJob)
				return recoveryJob, err
			},
			[]e2eutil.Predicate[*batchv1.Job]{func(job *batchv1.Job) (bool, string, error) {
				got := job.Status.Active
				return got == 1, fmt.Sprintf("wanted status active to be 1, got %d", got), nil
			}},
			e2eutil.WithInterval(5*time.Second),
			e2eutil.WithTimeout(15*time.Minute),
		)

		waitForEtcdConvergence(ctx, testCtx.MgmtClient, cpNamespace, ptr.Deref(etcdSts.Spec.Replicas, 0))
	})
}

// getEtcdStsAndPods fetches the etcd StatefulSet and its pods from the control plane namespace.
func getEtcdStsAndPods(ctx context.Context, client crclient.Client, cpNamespace string) (*appsv1.StatefulSet, *corev1.PodList) {
	GinkgoHelper()

	etcdSts := cpomanifests.EtcdStatefulSet(cpNamespace)
	Expect(client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)).To(Succeed(), "failed to get etcd StatefulSet")

	etcdPods := &corev1.PodList{}
	Expect(client.List(ctx, etcdPods, &crclient.ListOptions{
		Namespace:     cpNamespace,
		LabelSelector: labels.Set(etcdSts.Spec.Selector.MatchLabels).AsSelector(),
	})).To(Succeed(), "failed to list etcd pods")
	Expect(etcdPods.Items).NotTo(BeEmpty(), "no etcd pods found")
	GinkgoWriter.Printf("Found %d etcd pods\n", len(etcdPods.Items))

	return etcdSts, etcdPods
}

// waitForEtcdConvergence polls the etcd StatefulSet until ReadyReplicas equals the expected replica count.
func waitForEtcdConvergence(ctx context.Context, client crclient.Client, cpNamespace string, expectedReplicas int32) {
	GinkgoHelper()

	e2eutil.EventuallyObject(GinkgoTB(), ctx, "etcd StatefulSet replicas to converge",
		func(ctx context.Context) (*appsv1.StatefulSet, error) {
			sts := cpomanifests.EtcdStatefulSet(cpNamespace)
			err := client.Get(ctx, crclient.ObjectKeyFromObject(sts), sts)
			return sts, err
		},
		[]e2eutil.Predicate[*appsv1.StatefulSet]{func(sts *appsv1.StatefulSet) (bool, string, error) {
			got := sts.Status.ReadyReplicas
			return expectedReplicas != 0 && expectedReplicas == got, fmt.Sprintf("wanted %d ready replicas, got %d", expectedReplicas, got), nil
		}},
		e2eutil.WithInterval(5*time.Second),
		e2eutil.WithTimeout(30*time.Minute),
	)
}

// randomEtcdPods selects count random pods from the provided slice.
func randomEtcdPods(pods []corev1.Pod, count int) []corev1.Pod {
	indexes := rand.Perm(len(pods))
	selected := make([]corev1.Pod, count)
	for i := 0; i < count; i++ {
		selected[i] = pods[indexes[i]]
	}
	return selected
}

// createMarkerConfigMap creates a ConfigMap with timestamp data in the hosted cluster
// and returns it for later verification.
func createMarkerConfigMap(ctx context.Context, client crclient.Client) *corev1.ConfigMap {
	GinkgoHelper()

	value, err := time.Now().MarshalText()
	Expect(err).NotTo(HaveOccurred(), "failed to marshal timestamp")
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      e2eutil.SimpleNameGenerator.GenerateName("marker-"),
		},
		Data: map[string]string{"value": string(value)},
	}
	e2eutil.EventuallyObject(GinkgoTB(), ctx, "create marker ConfigMap",
		func(ctx context.Context) (*corev1.ConfigMap, error) {
			err := client.Create(ctx, cm)
			return cm, err
		}, nil,
	)
	GinkgoWriter.Printf("Created marker ConfigMap %s/%s\n", cm.Namespace, cm.Name)
	return cm
}

// verifyMarkerSurvived verifies that the marker ConfigMap still has its original data
// after etcd chaos operations.
func verifyMarkerSurvived(ctx context.Context, client crclient.Client, expected *corev1.ConfigMap) {
	GinkgoHelper()

	e2eutil.EventuallyObject(GinkgoTB(), ctx, "verify marker data survived disruption",
		func(ctx context.Context) (*corev1.ConfigMap, error) {
			actual := &corev1.ConfigMap{}
			err := client.Get(ctx, crclient.ObjectKeyFromObject(expected), actual)
			return actual, err
		},
		[]e2eutil.Predicate[*corev1.ConfigMap]{func(configMap *corev1.ConfigMap) (bool, string, error) {
			diff := cmp.Diff(expected.Data, configMap.Data)
			return diff == "", fmt.Sprintf("incorrect data: %v", diff), nil
		}},
		e2eutil.WithInterval(5*time.Second),
		e2eutil.WithTimeout(30*time.Minute),
	)
}
