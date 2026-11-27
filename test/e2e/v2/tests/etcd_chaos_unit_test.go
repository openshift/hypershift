//go:build e2ev2

package tests

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	etcdTestNamespace            = "clusters-etcd-chaos"
	etcdTestUnschedulableMessage = "0/3 nodes are available: 3 node(s) didn't match pod anti-affinity rules."
)

// errEtcdTestListUnavailable is a representative API error returned when listing pods fails.
var errEtcdTestListUnavailable = errors.New("the server is currently unable to handle the request")

// errEtcdTestLeaderChanged is a representative API error returned when reading the StatefulSet fails.
var errEtcdTestLeaderChanged = errors.New("etcdserver: leader changed")

type etcdTestContextKey struct{}

func etcdTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register apps/v1: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register core/v1: %v", err)
	}
	return scheme
}

func etcdTestStatefulSet(readyReplicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: etcdTestNamespace, Name: "etcd"},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To[int32](3),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "etcd"}},
		},
		Status: appsv1.StatefulSetStatus{ReadyReplicas: readyReplicas},
	}
}

func etcdTestPod(name string, phase corev1.PodPhase, conditions ...corev1.PodCondition) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: etcdTestNamespace, Name: name, UID: types.UID(name + "-uid"), Labels: map[string]string{"app": "etcd"}},
		Status:     corev1.PodStatus{Phase: phase, Conditions: conditions},
	}
}

func etcdTestCondition(conditionType corev1.PodConditionType, status corev1.ConditionStatus, reason, message string) corev1.PodCondition {
	return corev1.PodCondition{Type: conditionType, Status: status, Reason: reason, Message: message}
}

func etcdTestReadyPod(name string) corev1.Pod {
	return etcdTestPod(name, corev1.PodRunning,
		etcdTestCondition(corev1.PodScheduled, corev1.ConditionTrue, "", ""),
		etcdTestCondition(corev1.PodReady, corev1.ConditionTrue, "", ""))
}

func etcdTestUnschedulablePod(name string) corev1.Pod {
	return etcdTestPod(name, corev1.PodPending,
		etcdTestCondition(corev1.PodScheduled, corev1.ConditionFalse, corev1.PodReasonUnschedulable, etcdTestUnschedulableMessage))
}

// etcdTestPodClient serves pods through a fake client so that namespace and label selectors apply.
func etcdTestPodClient(scheme *runtime.Scheme, pods []corev1.Pod, listErr error) crclient.Client {
	objects := make([]crclient.Object, 0, len(pods))
	for i := range pods {
		objects = append(objects, pods[i].DeepCopy())
	}
	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...)
	if listErr != nil {
		builder = builder.WithInterceptorFuncs(interceptor.Funcs{
			List: func(context.Context, crclient.WithWatch, crclient.ObjectList, ...crclient.ListOption) error {
				return listErr
			},
		})
	}
	return builder.Build()
}

func etcdTestLines(output string) []string {
	return strings.FieldsFunc(output, func(r rune) bool { return r == '\n' })
}

// etcdConvergenceFixture scripts the StatefulSet and pods observed at each elapsed (virtual) time.
type etcdConvergenceFixture struct {
	readyReplicas func(elapsed time.Duration) int32
	pods          func(elapsed time.Duration) []corev1.Pod
	getErr        func(elapsed time.Duration) error
	listErr       func(elapsed time.Duration) error
	// blockGetFrom makes StatefulSet reads block until their context is done from this elapsed
	// time on; zero disables blocking.
	blockGetFrom time.Duration
}

type etcdConvergenceCalls struct {
	gets, lists       int
	deadlineSeen      bool
	specValueMissing  bool
	unexpectedRequest string
}

func (f etcdConvergenceFixture) client(t *testing.T, start time.Time, calls *etcdConvergenceCalls) crclient.Client {
	t.Helper()
	scheme := etcdTestScheme(t)
	checkContext := func(ctx context.Context) {
		if _, ok := ctx.Deadline(); ok {
			calls.deadlineSeen = true
		}
		if ctx.Value(etcdTestContextKey{}) == nil {
			calls.specValueMissing = true
		}
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, _ crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, _ ...crclient.GetOption) error {
			calls.gets++
			checkContext(ctx)
			elapsed := time.Since(start)
			if f.blockGetFrom > 0 && elapsed >= f.blockGetFrom {
				<-ctx.Done()
				return ctx.Err()
			}
			if f.getErr != nil {
				if err := f.getErr(elapsed); err != nil {
					return err
				}
			}
			sts, ok := obj.(*appsv1.StatefulSet)
			if !ok || key != (crclient.ObjectKey{Namespace: etcdTestNamespace, Name: "etcd"}) {
				calls.unexpectedRequest = fmt.Sprintf("get %T %s", obj, key)
				return errors.New(calls.unexpectedRequest)
			}
			etcdTestStatefulSet(f.readyReplicas(elapsed)).DeepCopyInto(sts)
			return nil
		},
		List: func(ctx context.Context, _ crclient.WithWatch, list crclient.ObjectList, opts ...crclient.ListOption) error {
			calls.lists++
			checkContext(ctx)
			elapsed := time.Since(start)
			var listErr error
			if f.listErr != nil {
				listErr = f.listErr(elapsed)
			}
			var pods []corev1.Pod
			if f.pods != nil {
				pods = f.pods(elapsed)
			}
			return etcdTestPodClient(scheme, pods, listErr).List(ctx, list, opts...)
		},
	}).Build()
}

func TestAwaitEtcdConvergence(t *testing.T) {
	t.Parallel()

	suiteTimeout := errors.New("suite timeout occurred")
	lifecycleStopped := errors.New("lifecycle context stopped")
	ready := func(after time.Duration) func(time.Duration) int32 {
		return func(elapsed time.Duration) int32 {
			if elapsed >= after {
				return 3
			}
			return 2
		}
	}
	never := func(time.Duration) int32 { return 2 }
	unschedulable := func(time.Duration) []corev1.Pod {
		return []corev1.Pod{etcdTestReadyPod("etcd-0"), etcdTestUnschedulablePod("etcd-1"), etcdTestReadyPod("etcd-2")}
	}
	scheduledAfter := func(after time.Duration) func(time.Duration) []corev1.Pod {
		return func(elapsed time.Duration) []corev1.Pod {
			if elapsed < after {
				return unschedulable(elapsed)
			}
			return []corev1.Pod{
				etcdTestReadyPod("etcd-0"),
				etcdTestPod("etcd-1", corev1.PodRunning,
					etcdTestCondition(corev1.PodScheduled, corev1.ConditionTrue, "", ""),
					etcdTestCondition(corev1.PodReady, corev1.ConditionFalse, "ContainersNotReady", "containers with unready status: [etcd]")),
				etcdTestReadyPod("etcd-2"),
			}
		}
	}
	failAfter := func(after time.Duration, err error) func(time.Duration) error {
		return func(elapsed time.Duration) error {
			if elapsed >= after {
				return err
			}
			return nil
		}
	}

	testCases := []struct {
		name             string
		expectedReplicas int32
		fixture          etcdConvergenceFixture
		// Cancellation offsets are not multiples of the poll interval so that cancellation
		// never races a poll scheduled for the same virtual instant.
		specCancelAfter      time.Duration // zero: never; negative: before the wait starts
		lifecycleCancelAfter time.Duration // zero: never; negative: before the wait starts
		wantConverged        bool
		wantElapsed          time.Duration
		wantGets             int
		wantLists            int
		wantFailure          []string
		wantFailureCounts    map[string]int
		wantLogCounts        map[string]int
		wantLogLines         int
	}{
		{
			name:             "When the StatefulSet already reports the expected ready replicas, it should succeed after one read without listing pods",
			expectedReplicas: 3,
			fixture:          etcdConvergenceFixture{readyReplicas: ready(0), pods: unschedulable},
			wantConverged:    true,
			wantGets:         1,
		},
		{
			name:             "When an etcd pod stays unschedulable past the former 30 minute limit, it should keep polling every 5s and succeed once ready",
			expectedReplicas: 3,
			fixture:          etcdConvergenceFixture{readyReplicas: ready(45 * time.Minute), pods: scheduledAfter(35 * time.Minute)},
			wantConverged:    true,
			wantElapsed:      45 * time.Minute,
			wantGets:         int(45*time.Minute/etcdConvergencePollInterval) + 1,
			wantLists:        int(45 * time.Minute / etcdConvergencePollInterval),
			wantLogCounts: map[string]int{
				"etcd pod clusters-etcd-chaos/etcd-1 unready (uid=etcd-1-uid): phase=Pending; PodScheduled=False reason=Unschedulable":                                                                   1,
				"[+35m0s] etcd pod clusters-etcd-chaos/etcd-1 still unready, changed (uid=etcd-1-uid): phase=Running; PodScheduled=True reason=\"\" message=\"\"; Ready=False reason=ContainersNotReady": 1,
			},
			wantLogLines: 2,
		},
		{
			name:             "When the spec context is canceled while a pod is unschedulable, it should fail with the cause and the latest unready pod summary",
			expectedReplicas: 3,
			fixture:          etcdConvergenceFixture{readyReplicas: never, pods: unschedulable},
			specCancelAfter:  40*time.Minute + 2*time.Second,
			wantElapsed:      40*time.Minute + 2*time.Second,
			wantGets:         int(40*time.Minute/etcdConvergencePollInterval) + 1,
			wantLists:        int(40*time.Minute/etcdConvergencePollInterval) + 1,
			wantFailure: []string{
				"etcd StatefulSet clusters-etcd-chaos/etcd to converge to 3 ready replicas",
				"(cause: suite timeout occurred) after 2402.000s",
				"wanted 3 ready replicas, got 2",
				"unready etcd pods at +40m0s (3 of 3 desired pods listed; namespace clusters-etcd-chaos, selector app=etcd):",
				"- etcd-1 (uid=etcd-1-uid): phase=Pending; PodScheduled=False reason=Unschedulable message=\"" + etcdTestUnschedulableMessage + "\"",
			},
			wantLogCounts: map[string]int{"PodScheduled=False reason=Unschedulable": 1},
			wantLogLines:  1,
		},
		{
			name:                 "When the lifecycle context is canceled, it should stop polling and report the lifecycle cause",
			expectedReplicas:     3,
			fixture:              etcdConvergenceFixture{readyReplicas: never, pods: unschedulable},
			lifecycleCancelAfter: 10*time.Minute + 2*time.Second,
			wantElapsed:          10*time.Minute + 2*time.Second,
			wantGets:             int(10*time.Minute/etcdConvergencePollInterval) + 1,
			wantLists:            int(10*time.Minute/etcdConvergencePollInterval) + 1,
			wantFailure:          []string{"(cause: lifecycle context stopped) after 602.000s", "wanted 3 ready replicas, got 2"},
			wantLogLines:         1,
		},
		{
			name:             "When the spec context is already canceled, it should fail with the cause without reading the API",
			expectedReplicas: 3,
			fixture:          etcdConvergenceFixture{readyReplicas: ready(0), pods: unschedulable},
			specCancelAfter:  -1,
			wantFailure:      []string{"convergence wait was canceled before polling", "suite timeout occurred"},
		},
		{
			name:                 "When the lifecycle context is already canceled, it should fail with the lifecycle cause without reading the API",
			expectedReplicas:     3,
			fixture:              etcdConvergenceFixture{readyReplicas: ready(0), pods: unschedulable},
			lifecycleCancelAfter: -1,
			wantFailure:          []string{"convergence wait was canceled before polling", "lifecycle context stopped"},
		},
		{
			name:             "When pod listing fails, it should not block convergence once the StatefulSet is ready",
			expectedReplicas: 3,
			fixture: etcdConvergenceFixture{
				readyReplicas: ready(time.Minute),
				listErr:       failAfter(0, errEtcdTestListUnavailable),
			},
			wantConverged: true,
			wantElapsed:   time.Minute,
			wantGets:      int(time.Minute/etcdConvergencePollInterval) + 1,
			wantLists:     int(time.Minute / etcdConvergencePollInterval),
			wantLogCounts: map[string]int{"[+0s] etcd pod diagnostics unavailable: the server is currently unable to handle the request": 1},
			wantLogLines:  1,
		},
		{
			name:             "When pod listing fails after pods were observed, it should report the error once with the last successful observation",
			expectedReplicas: 3,
			fixture: etcdConvergenceFixture{
				readyReplicas: never,
				pods:          unschedulable,
				listErr:       failAfter(10*time.Minute, errEtcdTestListUnavailable),
			},
			specCancelAfter: 20*time.Minute + 2*time.Second,
			wantElapsed:     20*time.Minute + 2*time.Second,
			wantGets:        int(20*time.Minute/etcdConvergencePollInterval) + 1,
			wantLists:       int(20*time.Minute/etcdConvergencePollInterval) + 1,
			wantFailure: []string{
				"(cause: suite timeout occurred) after 1202.000s",
				"wanted 3 ready replicas, got 2",
				"etcd pod diagnostics unavailable at +20m0s: the server is currently unable to handle the request",
				"last successful observation: unready etcd pods at +9m55s",
				"PodScheduled=False reason=Unschedulable",
			},
			wantFailureCounts: map[string]int{"diagnostics unavailable": 1, "last successful observation": 1},
			wantLogCounts:     map[string]int{"[+10m0s] etcd pod diagnostics unavailable: ": 1, "PodScheduled=False reason=Unschedulable": 1},
			wantLogLines:      2,
		},
		{
			name:             "When reading the StatefulSet fails after pods were observed, it should keep retrying and report the error with the latest pod summary",
			expectedReplicas: 3,
			fixture: etcdConvergenceFixture{
				readyReplicas: never,
				pods:          unschedulable,
				getErr:        failAfter(10*time.Minute, errEtcdTestLeaderChanged),
			},
			specCancelAfter: 15*time.Minute + 2*time.Second,
			wantElapsed:     15*time.Minute + 2*time.Second,
			wantGets:        int(15*time.Minute/etcdConvergencePollInterval) + 1,
			wantLists:       int(10 * time.Minute / etcdConvergencePollInterval),
			wantFailure: []string{
				"(cause: suite timeout occurred) after 902.000s",
				"failed to get etcd StatefulSet clusters-etcd-chaos/etcd; last observed 2 of 3 ready replicas at +9m55s",
				"etcdserver: leader changed",
				"unready etcd pods at +9m55s",
				"PodScheduled=False reason=Unschedulable",
			},
			wantLogLines: 1,
		},
		{
			name:             "When the spec context is canceled during a StatefulSet read, it should report the read error with the last observed replicas and pod summary",
			expectedReplicas: 3,
			fixture:          etcdConvergenceFixture{readyReplicas: never, pods: unschedulable, blockGetFrom: 10 * time.Minute},
			specCancelAfter:  10*time.Minute + 2*time.Second,
			wantElapsed:      10*time.Minute + 2*time.Second,
			wantGets:         int(10*time.Minute/etcdConvergencePollInterval) + 1,
			wantLists:        int(10 * time.Minute / etcdConvergencePollInterval),
			wantFailure: []string{
				"(cause: suite timeout occurred) after 602.000s",
				"failed to get etcd StatefulSet clusters-etcd-chaos/etcd; last observed 2 of 3 ready replicas at +9m55s",
				"context canceled",
				"PodScheduled=False reason=Unschedulable",
			},
			wantLogLines: 1,
		},
		{
			name:             "When the StatefulSet cannot be read before any observation, it should report that nothing was observed yet",
			expectedReplicas: 3,
			fixture:          etcdConvergenceFixture{readyReplicas: never, getErr: failAfter(0, errEtcdTestLeaderChanged)},
			specCancelAfter:  time.Minute + 2*time.Second,
			wantElapsed:      time.Minute + 2*time.Second,
			wantGets:         int(time.Minute/etcdConvergencePollInterval) + 1,
			wantFailure: []string{
				"failed to get etcd StatefulSet clusters-etcd-chaos/etcd; no StatefulSet status observed yet",
				"etcdserver: leader changed",
				"no successful etcd pod observation yet",
			},
		},
		{
			name:             "When the expected replica count is zero, it should fail without reading the API",
			expectedReplicas: 0,
			fixture:          etcdConvergenceFixture{readyReplicas: ready(0)},
			wantFailure:      []string{"must expect at least one replica"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				g := NewWithT(t)
				start := time.Now()
				calls := &etcdConvergenceCalls{}
				client := tc.fixture.client(t, start, calls)

				specCtx, cancelSpec := context.WithCancelCause(context.WithValue(t.Context(), etcdTestContextKey{}, "spec"))
				defer cancelSpec(nil)
				lifecycleCtx, cancelLifecycle := context.WithCancelCause(t.Context())
				defer cancelLifecycle(nil)
				for _, cancellation := range []struct {
					after  time.Duration
					cancel context.CancelCauseFunc
					cause  error
				}{{tc.specCancelAfter, cancelSpec, suiteTimeout}, {tc.lifecycleCancelAfter, cancelLifecycle, lifecycleStopped}} {
					switch {
					case cancellation.after < 0:
						cancellation.cancel(cancellation.cause)
					case cancellation.after > 0:
						timer := time.AfterFunc(cancellation.after, func() { cancellation.cancel(cancellation.cause) })
						defer timer.Stop()
					}
				}

				var failures []string
				waiter := NewGomega(func(message string, _ ...int) { failures = append(failures, message) })
				out := &bytes.Buffer{}
				converged := awaitEtcdConvergence(specCtx, lifecycleCtx, waiter, out, client, etcdTestNamespace, tc.expectedReplicas)

				g.Expect(converged).To(Equal(tc.wantConverged), "unexpected convergence result; failures: %q", failures)
				g.Expect(time.Since(start)).To(Equal(tc.wantElapsed), "the wait should end exactly at convergence or cancellation")
				g.Expect(calls.gets).To(Equal(tc.wantGets), "StatefulSet reads should follow the 5s polling cadence")
				g.Expect(calls.lists).To(Equal(tc.wantLists), "pods should be listed only while the StatefulSet has not converged")
				g.Expect(calls.unexpectedRequest).To(BeEmpty())
				g.Expect(calls.deadlineSeen).To(BeFalse(), "the poll context must not carry a deadline of its own")
				g.Expect(calls.specValueMissing).To(BeFalse(), "the poll context must derive from the spec context")
				if len(tc.wantFailure) == 0 {
					g.Expect(failures).To(BeEmpty())
				} else {
					g.Expect(failures).To(HaveLen(1), "exactly one failure should be reported")
					for _, want := range tc.wantFailure {
						g.Expect(failures[0]).To(ContainSubstring(want))
					}
					for substring, count := range tc.wantFailureCounts {
						g.Expect(strings.Count(failures[0], substring)).To(Equal(count), "occurrences of %q in failure:\n%s", substring, failures[0])
					}
				}
				lines := etcdTestLines(out.String())
				g.Expect(lines).To(HaveLen(tc.wantLogLines), "diagnostic log lines: %q", lines)
				for substring, count := range tc.wantLogCounts {
					g.Expect(strings.Count(out.String(), substring)).To(Equal(count), "occurrences of %q in log:\n%s", substring, out.String())
				}
			})
		})
	}
}

func TestEtcdPodDiagnostics_Observe(t *testing.T) {
	t.Parallel()

	unschedulableAt := func(transition metav1.Time) corev1.Pod {
		pod := etcdTestUnschedulablePod("etcd-1")
		pod.Status.Conditions[0].LastTransitionTime = transition
		pod.Status.Conditions = append(pod.Status.Conditions,
			corev1.PodCondition{Type: corev1.PodReady, Status: corev1.ConditionFalse, Reason: "PodNotScheduled", LastProbeTime: transition})
		return pod
	}
	reordered := func(pod corev1.Pod) corev1.Pod {
		pod.Status.Conditions[0], pod.Status.Conditions[1] = pod.Status.Conditions[1], pod.Status.Conditions[0]
		return pod
	}
	scheduled := etcdTestPod("etcd-1", corev1.PodPending, etcdTestCondition(corev1.PodScheduled, corev1.ConditionTrue, "", ""))
	starting := etcdTestPod("etcd-1", corev1.PodRunning,
		etcdTestCondition(corev1.PodScheduled, corev1.ConditionTrue, "", ""),
		etcdTestCondition(corev1.PodReady, corev1.ConditionFalse, "ContainersNotReady", "containers with unready status: [etcd]"))
	moreNodes := etcdTestUnschedulablePod("etcd-1")
	moreNodes.Status.Conditions[0].Message = "0/4 nodes are available: 4 node(s) had untolerated taint."
	replacement := etcdTestUnschedulablePod("etcd-1")
	replacement.UID = "etcd-1-replacement-uid"
	otherApp := etcdTestUnschedulablePod("other-1")
	otherApp.Labels = map[string]string{"app": "other"}
	otherNamespace := etcdTestUnschedulablePod("etcd-9")
	otherNamespace.Namespace = "clusters-other"
	newSelector := etcdTestUnschedulablePod("etcd-new-1")
	newSelector.Labels = map[string]string{"app": "etcd-new"}
	listTimeout := errors.New("etcdserver: request timed out")

	type step struct {
		pods          []corev1.Pod
		selector      map[string]string
		noSelector    bool
		listErr       error
		canceled      bool
		wantLogs      []string
		notLogged     []string
		wantLogLines  int
		wantSummary   []string
		notSummary    []string
		summaryCounts map[string]int
	}
	testCases := []struct {
		name  string
		steps []step
	}{
		{
			name: "When an unready pod is observed again with reordered conditions and new timestamps, it should log it only once",
			steps: []step{
				{
					pods:         []corev1.Pod{unschedulableAt(metav1.NewTime(time.Unix(100, 0)))},
					wantLogs:     []string{"etcd pod clusters-etcd-chaos/etcd-1 unready (uid=etcd-1-uid): phase=Pending; PodScheduled=False reason=Unschedulable message=\"" + etcdTestUnschedulableMessage + "\"; Ready=False reason=PodNotScheduled"},
					wantLogLines: 1,
					wantSummary:  []string{"- etcd-1 (uid=etcd-1-uid): phase=Pending; PodScheduled=False reason=Unschedulable"},
				},
				{pods: []corev1.Pod{reordered(unschedulableAt(metav1.NewTime(time.Unix(200, 0))))}},
			},
		},
		{
			name: "When an unready pod's condition becomes True, it should log only the changed condition with its new values",
			steps: []step{
				{pods: []corev1.Pod{etcdTestUnschedulablePod("etcd-1")}, wantLogLines: 1},
				{
					pods:         []corev1.Pod{scheduled},
					wantLogs:     []string{"etcd pod clusters-etcd-chaos/etcd-1 still unready, changed (uid=etcd-1-uid): PodScheduled=True reason=\"\" message=\"\"\n"},
					notLogged:    []string{"Unschedulable", "phase="},
					wantLogLines: 1,
					wantSummary:  []string{"- etcd-1 (uid=etcd-1-uid): phase=Pending; PodScheduled=True"},
					notSummary:   []string{"Unschedulable"},
				},
				{
					pods:         []corev1.Pod{starting},
					wantLogs:     []string{"still unready, changed (uid=etcd-1-uid): phase=Running; Ready=False reason=ContainersNotReady message=\"containers with unready status: [etcd]\"\n"},
					notLogged:    []string{"PodScheduled"},
					wantLogLines: 1,
				},
			},
		},
		{
			name: "When an unready pod's condition message changes, it should log the new message",
			steps: []step{
				{pods: []corev1.Pod{etcdTestUnschedulablePod("etcd-1")}, wantLogLines: 1},
				{
					pods:         []corev1.Pod{moreNodes},
					wantLogs:     []string{"changed (uid=etcd-1-uid): PodScheduled=False reason=Unschedulable message=\"0/4 nodes are available: 4 node(s) had untolerated taint.\""},
					wantLogLines: 1,
				},
			},
		},
		{
			name: "When an unready pod stops reporting a condition, it should log that the condition is no longer reported",
			steps: []step{
				{pods: []corev1.Pod{starting}, wantLogLines: 1},
				{pods: []corev1.Pod{etcdTestPod("etcd-1", corev1.PodRunning, etcdTestCondition(corev1.PodScheduled, corev1.ConditionTrue, "", ""))}, wantLogs: []string{"changed (uid=etcd-1-uid): Ready no longer reported"}, wantLogLines: 1},
			},
		},
		{
			name: "When an unready pod becomes ready and later unready again, it should log each transition once",
			steps: []step{
				{pods: []corev1.Pod{etcdTestUnschedulablePod("etcd-1")}, wantLogLines: 1},
				{
					pods:         []corev1.Pod{etcdTestReadyPod("etcd-1")},
					wantLogs:     []string{"etcd pod clusters-etcd-chaos/etcd-1 is ready"},
					wantLogLines: 1,
					wantSummary:  []string{"no unready etcd pods at +", "(1 of 3 desired pods listed; namespace clusters-etcd-chaos, selector app=etcd)"},
					notSummary:   []string{"etcd-1"},
				},
				{pods: []corev1.Pod{etcdTestReadyPod("etcd-1")}},
				{
					pods:         []corev1.Pod{etcdTestUnschedulablePod("etcd-1")},
					wantLogs:     []string{"etcd pod clusters-etcd-chaos/etcd-1 unready (uid=etcd-1-uid): phase=Pending; PodScheduled=False reason=Unschedulable"},
					wantLogLines: 1,
				},
			},
		},
		{
			name: "When an unready pod is no longer listed, it should log that it disappeared",
			steps: []step{
				{pods: []corev1.Pod{etcdTestUnschedulablePod("etcd-1")}, wantLogLines: 1},
				{wantLogs: []string{"etcd pod clusters-etcd-chaos/etcd-1 is no longer listed"}, wantLogLines: 1, wantSummary: []string{"0 of 3 desired pods listed"}},
			},
		},
		{
			name: "When an unready pod is replaced by a pod with the same name and conditions, it should log the replacement in full",
			steps: []step{
				{pods: []corev1.Pod{etcdTestUnschedulablePod("etcd-1")}, wantLogLines: 1},
				{pods: []corev1.Pod{replacement}, wantLogs: []string{"etcd-1 unready (uid=etcd-1-replacement-uid): phase=Pending; PodScheduled=False reason=Unschedulable"}, wantLogLines: 1},
			},
		},
		{
			name: "When pods outside the StatefulSet selector or namespace exist, it should ignore them",
			steps: []step{{
				pods:         []corev1.Pod{etcdTestUnschedulablePod("etcd-1"), otherApp, otherNamespace},
				wantLogs:     []string{"etcd-1 unready"},
				notLogged:    []string{"other-1", "etcd-9"},
				wantLogLines: 1,
				wantSummary:  []string{"unready etcd pods at +", "(1 of 3 desired pods listed; namespace clusters-etcd-chaos, selector app=etcd)"},
				notSummary:   []string{"other-1", "etcd-9"},
			}},
		},
		{
			name: "When the StatefulSet selector changes, it should list pods with the live selector",
			steps: []step{
				{pods: []corev1.Pod{etcdTestUnschedulablePod("etcd-1"), newSelector}, wantLogs: []string{"etcd-1 unready"}, notLogged: []string{"etcd-new-1"}, wantLogLines: 1},
				{
					pods:         []corev1.Pod{etcdTestUnschedulablePod("etcd-1"), newSelector},
					selector:     map[string]string{"app": "etcd-new"},
					wantLogs:     []string{"etcd-new-1 unready", "etcd pod clusters-etcd-chaos/etcd-1 is no longer listed"},
					wantLogLines: 2,
					wantSummary:  []string{"selector app=etcd-new", "- etcd-new-1"},
				},
			},
		},
		{
			name: "When pod listing fails, it should log each distinct error once and keep the last successful observation without nesting errors",
			steps: []step{
				{pods: []corev1.Pod{etcdTestUnschedulablePod("etcd-1")}, wantLogLines: 1},
				{
					listErr:      errEtcdTestListUnavailable,
					wantLogs:     []string{"etcd pod diagnostics unavailable: " + errEtcdTestListUnavailable.Error()},
					wantLogLines: 1,
					wantSummary:  []string{errEtcdTestListUnavailable.Error(), "last successful observation: unready etcd pods at +", "PodScheduled=False reason=Unschedulable"},
				},
				{listErr: errEtcdTestListUnavailable},
				{
					listErr:       listTimeout,
					wantLogs:      []string{listTimeout.Error()},
					wantLogLines:  1,
					wantSummary:   []string{listTimeout.Error(), "PodScheduled=False reason=Unschedulable"},
					notSummary:    []string{errEtcdTestListUnavailable.Error()},
					summaryCounts: map[string]int{"diagnostics unavailable": 1, "last successful observation": 1},
				},
				{
					pods:        []corev1.Pod{etcdTestUnschedulablePod("etcd-1")},
					wantSummary: []string{"PodScheduled=False reason=Unschedulable"},
					notSummary:  []string{"diagnostics unavailable", "last successful observation"},
				},
			},
		},
		{
			name: "When pod listing fails because the context is canceled, it should not log the error",
			steps: []step{{
				listErr:     context.Canceled,
				canceled:    true,
				wantSummary: []string{"etcd pod diagnostics unavailable at +", "context canceled", "last successful observation: no successful etcd pod observation yet"},
			}},
		},
		{
			name: "When the StatefulSet has no selector, it should report the diagnostics as unavailable",
			steps: []step{{
				pods:         []corev1.Pod{etcdTestUnschedulablePod("etcd-1")},
				noSelector:   true,
				wantLogs:     []string{"etcd StatefulSet has no pod selector"},
				wantLogLines: 1,
				wantSummary:  []string{"etcd StatefulSet has no pod selector"},
			}},
		},
		{
			name: "When an unready pod reports no conditions, it should say so",
			steps: []step{{
				pods:         []corev1.Pod{etcdTestPod("etcd-1", corev1.PodPending)},
				wantLogs:     []string{"etcd-1 unready (uid=etcd-1-uid): phase=Pending; no conditions reported"},
				wantLogLines: 1,
			}},
		},
		{
			name: "When no pods are unready, it should report how many pods matched without logging",
			steps: []step{{
				pods:        []corev1.Pod{etcdTestReadyPod("etcd-0"), etcdTestReadyPod("etcd-1"), etcdTestReadyPod("etcd-2")},
				wantSummary: []string{"no unready etcd pods at +", "3 of 3 desired pods listed"},
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			scheme := etcdTestScheme(t)
			out := &bytes.Buffer{}
			diagnostics := &etcdPodDiagnostics{out: out, start: time.Now()}
			for i, s := range tc.steps {
				out.Reset()
				sts := etcdTestStatefulSet(2)
				if s.selector != nil {
					sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: s.selector}
				}
				if s.noSelector {
					sts.Spec.Selector = nil
				}
				stepCtx, cancel := context.WithCancel(t.Context())
				if s.canceled {
					cancel()
				}
				diagnostics.observe(stepCtx, etcdTestPodClient(scheme, s.pods, s.listErr), sts)
				cancel()

				logged := out.String()
				g.Expect(etcdTestLines(logged)).To(HaveLen(s.wantLogLines), "step %d log lines: %q", i, logged)
				for _, want := range s.wantLogs {
					g.Expect(logged).To(ContainSubstring(want), "step %d log", i)
				}
				for _, unwanted := range s.notLogged {
					g.Expect(logged).NotTo(ContainSubstring(unwanted), "step %d log", i)
				}
				summary := diagnostics.summary()
				for _, want := range s.wantSummary {
					g.Expect(summary).To(ContainSubstring(want), "step %d summary", i)
				}
				for _, unwanted := range s.notSummary {
					g.Expect(summary).NotTo(ContainSubstring(unwanted), "step %d summary", i)
				}
				for substring, count := range s.summaryCounts {
					g.Expect(strings.Count(summary, substring)).To(Equal(count), "step %d occurrences of %q in summary:\n%s", i, substring, summary)
				}
			}
		})
	}
}
