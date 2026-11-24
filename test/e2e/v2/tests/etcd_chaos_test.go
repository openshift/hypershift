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
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cpomanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	etcdrecoverymanifests "github.com/openshift/hypershift/pkg/manifests/etcdrecovery"
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

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:EtcdResilience] Etcd Chaos", Label("lifecycle", "etcd-chaos"), Ordered, func() {
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
	It("should recover after a single member loses its data", func(specCtx SpecContext) {
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

		waitForEtcdConvergence(specCtx, testCtx.Context, testCtx.MgmtClient, cpNamespace, ptr.Deref(etcdSts.Spec.Replicas, 0))
	})
}

// EtcdKillRandomMembersTest creates a marker ConfigMap in the hosted cluster,
// deletes random etcd pods every 5 seconds for 30 seconds, then verifies
// StatefulSet convergence and that the marker data survived.
func EtcdKillRandomMembersTest(getTestCtx internal.TestContextGetter) {
	It("should preserve data when random members are repeatedly killed", func(specCtx SpecContext) {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace

		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())

		// Create marker data that should survive the chaos
		markerCM := createMarkerConfigMap(ctx, hcClient)
		DeferCleanup(func() {
			err := hcClient.Delete(ctx, markerCM)
			if err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete marker ConfigMap %s", markerCM.Name)
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

		waitForEtcdConvergence(specCtx, testCtx.Context, testCtx.MgmtClient, cpNamespace, ptr.Deref(etcdSts.Spec.Replicas, 0))

		verifyMarkerSurvived(ctx, hcClient, markerCM)
	})
}

// EtcdKillAllMembersTest creates a marker ConfigMap, deletes ALL etcd pods simultaneously
// via goroutines, then verifies convergence and marker survival.
func EtcdKillAllMembersTest(getTestCtx internal.TestContextGetter) {
	It("should preserve data when all members are killed simultaneously", func(specCtx SpecContext) {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace

		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())

		// Create marker data that should survive the chaos
		markerCM := createMarkerConfigMap(ctx, hcClient)
		DeferCleanup(func() {
			err := hcClient.Delete(ctx, markerCM)
			if err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete marker ConfigMap %s", markerCM.Name)
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

		waitForEtcdConvergence(specCtx, testCtx.Context, testCtx.MgmtClient, cpNamespace, ptr.Deref(etcdSts.Spec.Replicas, 0))

		verifyMarkerSurvived(ctx, hcClient, markerCM)
	})
}

// EtcdSingleMemberCorruptionTest destroys a random member's data directory using
// RunCommandInPod, then waits for etcd to crash in-place so the recovery
// controller detects the failing member and creates a recovery job.
func EtcdSingleMemberCorruptionTest(getTestCtx internal.TestContextGetter) {
	It("should recover after a single member's data is corrupted", func(specCtx SpecContext) {
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

		waitForEtcdConvergence(specCtx, testCtx.Context, testCtx.MgmtClient, cpNamespace, ptr.Deref(etcdSts.Spec.Replicas, 0))
	})
}

// EtcdMissingMemberRecoveryTest removes a member from the etcd cluster via
// etcdctl member remove, deletes the pod, verifies the recovery job,
// and waits for StatefulSet convergence.
func EtcdMissingMemberRecoveryTest(getTestCtx internal.TestContextGetter) {
	It("should recover after a member is removed from the etcd cluster", func(specCtx SpecContext) {
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

		waitForEtcdConvergence(specCtx, testCtx.Context, testCtx.MgmtClient, cpNamespace, ptr.Deref(etcdSts.Spec.Replicas, 0))
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

// etcdConvergencePollInterval is how often waitForEtcdConvergence polls the etcd StatefulSet.
const etcdConvergencePollInterval = 5 * time.Second

// waitForEtcdConvergence polls the etcd StatefulSet until ReadyReplicas equals the expected
// replica count. The wait has no timeout of its own: it is bounded by specCtx, which Ginkgo
// cancels when the suite or spec deadline elapses or the run is interrupted, and by the
// framework lifecycle context. Condition changes of unready etcd pods are logged to
// GinkgoWriter while waiting.
func waitForEtcdConvergence(specCtx, lifecycleCtx context.Context, client crclient.Client, cpNamespace string, expectedReplicas int32) {
	GinkgoHelper()
	awaitEtcdConvergence(specCtx, lifecycleCtx, Default, GinkgoWriter, client, cpNamespace, expectedReplicas)
}

// awaitEtcdConvergence implements waitForEtcdConvergence, reporting failures through g and
// diagnostics to out. It returns whether the StatefulSet converged.
func awaitEtcdConvergence(specCtx, lifecycleCtx context.Context, g Gomega, out io.Writer, client crclient.Client, cpNamespace string, expectedReplicas int32) bool {
	GinkgoHelper()
	stsKey := crclient.ObjectKeyFromObject(cpomanifests.EtcdStatefulSet(cpNamespace))
	if !g.Expect(expectedReplicas).To(BeNumerically(">", 0), "etcd StatefulSet %s must expect at least one replica", stsKey) {
		return false
	}

	// Stop when either the spec context or the lifecycle context is done.
	ctx, cancel := context.WithCancelCause(specCtx)
	defer cancel(nil)
	if lifecycleCtx.Err() != nil {
		cancel(context.Cause(lifecycleCtx))
	}
	stop := context.AfterFunc(lifecycleCtx, func() { cancel(context.Cause(lifecycleCtx)) })
	defer stop()
	if ctx.Err() != nil {
		g.Expect(context.Cause(ctx)).NotTo(HaveOccurred(), "etcd StatefulSet %s convergence wait was canceled before polling", stsKey)
		return false
	}

	diagnostics := &etcdPodDiagnostics{out: out, start: time.Now()}
	lastObserved := "no StatefulSet status observed yet"
	// Deliberately no WithTimeout: given a context and no timeout, Gomega's Eventually polls until
	// the context is done. This relies on Gomega's default of not enforcing timeouts with contexts
	// (EnforceDefaultTimeoutsWhenUsingContexts / GOMEGA_ENFORCE_DEFAULT_TIMEOUTS_WHEN_USING_CONTEXTS
	// unset), which this suite keeps. ctx derives from the Ginkgo SpecContext, so Gomega also
	// attaches the latest failure message, including the unready pod summary, to Ginkgo's
	// progress report.
	return g.Eventually(func(g Gomega, pollCtx context.Context) {
		sts := &appsv1.StatefulSet{}
		g.Expect(client.Get(pollCtx, stsKey, sts)).To(Succeed(), "failed to get etcd StatefulSet %s; %s\n%s", stsKey, lastObserved, diagnostics.summary())
		got := sts.Status.ReadyReplicas
		if got == expectedReplicas {
			return
		}
		lastObserved = fmt.Sprintf("last observed %d of %d ready replicas at +%s", got, expectedReplicas, time.Since(diagnostics.start).Round(time.Second))
		// Diagnostics never change the verdict, which comes only from the StatefulSet status.
		diagnostics.observe(pollCtx, client, sts)
		g.Expect(got).To(Equal(expectedReplicas), "wanted %d ready replicas, got %d\n%s", expectedReplicas, got, diagnostics.summary())
	}).WithContext(ctx).WithPolling(etcdConvergencePollInterval).Should(Succeed(), "etcd StatefulSet %s to converge to %d ready replicas", stsKey, expectedReplicas)
}

// etcdPodDiagnostics tracks unready etcd pods while waiting for convergence. It logs each
// change to an unready pod's phase or conditions once and keeps a summary of the last
// successful observation for failure messages.
type etcdPodDiagnostics struct {
	out   io.Writer
	start time.Time
	// unready holds the last logged state of each unready pod, keyed by pod name.
	unready map[string]etcdPodState
	// snapshot summarizes the last successful observation; it never contains errors.
	snapshot string
	// failure describes the current diagnostics error and is cleared by the next successful
	// observation. loggedErr is the last error written to out, so repeats are not logged.
	failure   string
	loggedErr string
}

// etcdPodState is the part of a pod's status that is compared between observations.
// Timestamps are excluded so that only real changes are logged.
type etcdPodState struct {
	uid        string
	phase      corev1.PodPhase
	conditions map[corev1.PodConditionType]etcdPodCondition
}

type etcdPodCondition struct {
	status  corev1.ConditionStatus
	reason  string
	message string
}

// observe lists the pods selected by the live StatefulSet selector in the StatefulSet
// namespace and logs changes of unready pods. Errors are logged once and recorded in the
// summary without discarding the last successful observation.
func (d *etcdPodDiagnostics) observe(ctx context.Context, client crclient.Client, sts *appsv1.StatefulSet) {
	elapsed := time.Since(d.start).Round(time.Second)
	selector, err := metav1.LabelSelectorAsSelector(sts.Spec.Selector)
	if err == nil && (sts.Spec.Selector == nil || selector.Empty()) {
		err = errors.New("etcd StatefulSet has no pod selector")
	}
	pods := &corev1.PodList{}
	if err == nil {
		err = client.List(ctx, pods, crclient.InNamespace(sts.Namespace), crclient.MatchingLabelsSelector{Selector: selector})
	}
	if err != nil {
		d.failure = fmt.Sprintf("etcd pod diagnostics unavailable at +%s: %v", elapsed, err)
		if ctx.Err() == nil && err.Error() != d.loggedErr {
			d.logf(elapsed, "etcd pod diagnostics unavailable: %v", err)
			d.loggedErr = err.Error()
		}
		return
	}
	d.failure, d.loggedErr = "", ""
	if d.unready == nil {
		d.unready = map[string]etcdPodState{}
	}

	slices.SortFunc(pods.Items, func(a, b corev1.Pod) int { return strings.Compare(a.Name, b.Name) })
	listed := map[string]bool{}
	var unreadySummaries []string
	for i := range pods.Items {
		pod := &pods.Items[i]
		listed[pod.Name] = true
		current, ready := etcdPodStateOf(pod)
		previous, logged := d.unready[pod.Name]
		if ready {
			if logged {
				d.logf(elapsed, "etcd pod %s/%s is ready", pod.Namespace, pod.Name)
				delete(d.unready, pod.Name)
			}
			continue
		}
		description := current.describe(nil)
		unreadySummaries = append(unreadySummaries, fmt.Sprintf("- %s %s", pod.Name, description))
		switch {
		case !logged || previous.uid != current.uid:
			d.logf(elapsed, "etcd pod %s/%s unready %s", pod.Namespace, pod.Name, description)
		case !current.equal(previous):
			d.logf(elapsed, "etcd pod %s/%s still unready, changed %s", pod.Namespace, pod.Name, current.describe(&previous))
		}
		d.unready[pod.Name] = current
	}
	for _, name := range slices.Sorted(maps.Keys(d.unready)) {
		if !listed[name] {
			d.logf(elapsed, "etcd pod %s/%s is no longer listed", sts.Namespace, name)
			delete(d.unready, name)
		}
	}

	scope := fmt.Sprintf("%d of %d desired pods listed; namespace %s, selector %s", len(pods.Items), ptr.Deref(sts.Spec.Replicas, 1), sts.Namespace, selector)
	if len(unreadySummaries) == 0 {
		d.snapshot = fmt.Sprintf("no unready etcd pods at +%s (%s)", elapsed, scope)
		return
	}
	d.snapshot = fmt.Sprintf("unready etcd pods at +%s (%s):\n%s", elapsed, scope, strings.Join(unreadySummaries, "\n"))
}

// logf writes one diagnostic line prefixed with the time elapsed since the wait started.
func (d *etcdPodDiagnostics) logf(elapsed time.Duration, format string, args ...any) {
	fmt.Fprintf(d.out, "[+%s] %s\n", elapsed, fmt.Sprintf(format, args...))
}

// summary returns the latest diagnostics for failure messages.
func (d *etcdPodDiagnostics) summary() string {
	snapshot := d.snapshot
	if snapshot == "" {
		snapshot = "no successful etcd pod observation yet"
	}
	if d.failure == "" {
		return snapshot
	}
	return fmt.Sprintf("%s\nlast successful observation: %s", d.failure, snapshot)
}

// etcdPodStateOf returns the comparable state of pod and whether its Ready condition is True.
func etcdPodStateOf(pod *corev1.Pod) (etcdPodState, bool) {
	state := etcdPodState{
		uid:        string(pod.UID),
		phase:      pod.Status.Phase,
		conditions: map[corev1.PodConditionType]etcdPodCondition{},
	}
	ready := false
	for _, condition := range pod.Status.Conditions {
		state.conditions[condition.Type] = etcdPodCondition{status: condition.Status, reason: condition.Reason, message: condition.Message}
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			ready = true
		}
	}
	return state, ready
}

func (s etcdPodState) equal(other etcdPodState) bool {
	return s.uid == other.uid && s.phase == other.phase && maps.Equal(s.conditions, other.conditions)
}

// describe formats the pod state. With a previous state, only the phase and conditions that
// changed are included, each with its new status, reason and message (empty values explicit).
func (s etcdPodState) describe(previous *etcdPodState) string {
	var parts []string
	if previous == nil || previous.phase != s.phase {
		parts = append(parts, fmt.Sprintf("phase=%s", s.phase))
	}
	conditionTypes := slices.Sorted(maps.Keys(s.conditions))
	if previous != nil {
		for conditionType := range previous.conditions {
			if _, ok := s.conditions[conditionType]; !ok {
				conditionTypes = append(conditionTypes, conditionType)
			}
		}
		slices.Sort(conditionTypes)
	}
	for _, conditionType := range conditionTypes {
		condition, ok := s.conditions[conditionType]
		if !ok {
			parts = append(parts, fmt.Sprintf("%s no longer reported", conditionType))
			continue
		}
		if previous != nil {
			if previousCondition, reported := previous.conditions[conditionType]; reported && previousCondition == condition {
				continue
			}
		}
		part := fmt.Sprintf("%s=%s", conditionType, condition.status)
		switch {
		case condition.reason != "":
			part += " reason=" + condition.reason
		case previous != nil:
			part += ` reason=""`
		}
		if condition.message != "" || previous != nil {
			part += fmt.Sprintf(" message=%q", condition.message)
		}
		parts = append(parts, part)
	}
	if previous == nil && len(s.conditions) == 0 {
		parts = append(parts, "no conditions reported")
	}
	return fmt.Sprintf("(uid=%s): %s", s.uid, strings.Join(parts, "; "))
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
