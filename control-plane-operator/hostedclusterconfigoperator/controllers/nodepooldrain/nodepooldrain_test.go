package nodepooldrain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/pdbdrain"
	"github.com/openshift/hypershift/support/statuspatching"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr/funcr"
)

func node(name, pool string) *corev1.Node {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if pool != "" {
		n.Labels = map[string]string{hyperv1.NodePoolLabel: pool}
		n.UID = types.UID("uid-" + name)
		n.Annotations = map[string]string{
			capiv1.MachineAnnotation:          "machine-" + name,
			capiv1.ClusterNamespaceAnnotation: "clusters-foo",
		}
	}
	return n
}

func testMachine(nodeName, pool, poolUID, namespace string) *capiv1.Machine {
	return &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "machine-" + nodeName, Namespace: namespace,
			Annotations: map[string]string{
				hyperv1.NodePoolLabel:         "clusters/" + pool,
				hyperv1.NodePoolUIDAnnotation: poolUID,
			},
		},
		Status: capiv1.MachineStatus{NodeRef: capiv1.MachineNodeReference{Name: nodeName}},
	}
}

func computeDrainWithMachines(t *testing.T, r *reconciler, ctx context.Context) ([]hyperv1.NodePoolDrain, map[string]blockInfo, error) {
	t.Helper()
	machineScheme := runtime.NewScheme()
	if err := capiv1.AddToScheme(machineScheme); err != nil {
		t.Fatal(err)
	}
	var nodes corev1.NodeList
	var machines []client.Object
	if err := r.guestClient.List(ctx, &nodes); err == nil {
		for _, n := range nodes.Items {
			if pool := n.Labels[hyperv1.NodePoolLabel]; pool != "" {
				machines = append(machines, testMachine(n.Name, pool, "uid-"+pool, "clusters-foo"))
			}
		}
	}
	r.hcpNamespace = "clusters-foo"
	r.mgtClusterClient = fake.NewClientBuilder().WithScheme(machineScheme).WithObjects(machines...).Build()
	return r.computeDrain(ctx)
}

func structuralPDB(ns, name string, sel map[string]string) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       policyv1.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: sel}},
		Status:     policyv1.PodDisruptionBudgetStatus{ExpectedPods: 2, DesiredHealthy: 2}, // structurally blocking
	}
}

func pod(ns, name, nodeName string, labels map[string]string, ownerKind string) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels},
		Spec:       corev1.PodSpec{NodeName: nodeName},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if ownerKind != "" {
		p.OwnerReferences = []metav1.OwnerReference{{Kind: ownerKind, Name: "owner", Controller: ptr.To(true)}}
	}
	return p
}

//nolint:gocyclo // table-driven test aggregating many computeDrain scenarios.
func TestComputeDrain(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = policyv1.AddToScheme(scheme)
	_ = capiv1.AddToScheme(scheme)

	webSel := map[string]string{"app": "web"}
	objs := []runtime.Object{
		node("node-a1", "pool-a"),
		node("node-b1", "pool-b"),
		node("node-unlabeled", ""),

		// customer, structurally blocking, pod on pool-a -> blocks pool-a
		structuralPDB("team-x", "web", webSel),
		pod("team-x", "web-0", "node-a1", webSel, "ReplicaSet"),

		// platform namespace -> ignored even though structurally blocking with a pod on pool-b
		structuralPDB("openshift-monitoring", "prom", webSel),
		pod("openshift-monitoring", "prom-0", "node-b1", webSel, "StatefulSet"),

		// customer, structurally blocking, but only a DaemonSet pod matches -> not a drain candidate
		structuralPDB("team-y", "agent", map[string]string{"app": "agent"}),
		pod("team-y", "agent-0", "node-b1", map[string]string{"app": "agent"}, "DaemonSet"),
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
	r := &reconciler{
		guestClient:        c,
		guestAPIReader:     c,
		platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes,
	}

	drain, blocking, err := computeDrainWithMachines(t, r, context.Background())
	if err != nil {
		t.Fatalf("computeDrain: %v", err)
	}

	got := map[string]hyperv1.NodePoolDrain{}
	for _, e := range drain {
		got[e.NodePoolName] = e
	}

	if a, ok := got["pool-a"]; !ok || a.State != hyperv1.NodePoolDrainStateBlocked || ptr.Deref(a.BlockingCount, 0) != 1 {
		t.Errorf("pool-a: want blocked=true count=1, got %+v", a)
	}
	if got["pool-a"].NodePoolUID != "uid-pool-a" || got["pool-b"].NodePoolUID != "uid-pool-b" {
		t.Errorf("drain results did not include pool identities: %+v", got)
	}
	if b, ok := got["pool-b"]; !ok || b.State != hyperv1.NodePoolDrainStateDrainable {
		t.Errorf("pool-b: want blocked=false (platform + daemonset ignored), got %+v", b)
	}
	if _, ok := got["node-unlabeled"]; ok {
		t.Errorf("unlabeled node should not produce a pool entry")
	}
	if len(blocking) != 1 {
		t.Errorf("want exactly 1 blocking PDB (team-x/web), got %d: %v", len(blocking), blocking)
	}
	if _, ok := blocking["team-x/web"]; !ok {
		t.Errorf("expected team-x/web in blocking set, got %v", blocking)
	}

	for _, tc := range []struct {
		name        string
		namespace   string
		additional  string
		wantBlocked bool
	}{
		{
			name:      "When a customer PDB is in the default namespace, it should report the blocked pool",
			namespace: "default", wantBlocked: true,
		},
		{
			name:      "When a PDB is in a configured managed namespace, it should exclude the PDB",
			namespace: "redhat-services", additional: "redhat-",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
				node("node-a1", "pool-a"), structuralPDB(tc.namespace, "web", webSel),
				pod(tc.namespace, "web-0", "node-a1", webSel, "ReplicaSet"),
			).Build()
			r := &reconciler{
				guestClient: guest, guestAPIReader: guest,
				platformNSPrefixes: pdbdrain.PlatformNamespacePrefixes(tc.additional),
			}
			drain, _, err := computeDrainWithMachines(t, r, t.Context())
			if err != nil {
				t.Fatal(err)
			}
			wantState := hyperv1.NodePoolDrainStateDrainable
			if tc.wantBlocked {
				wantState = hyperv1.NodePoolDrainStateBlocked
			}
			if len(drain) != 1 || drain[0].State != wantState {
				t.Errorf("drain = %+v, want blocked=%t", drain, tc.wantBlocked)
			}
		})
	}

	t.Run("When the pod list has a continuation token, it should reject the incomplete probe", func(t *testing.T) {
		guest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
			node("node-a1", "pool-a"), structuralPDB("team-x", "web", webSel),
		).Build()
		reader := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(_ context.Context, _ client.WithWatch, list client.ObjectList, _ ...client.ListOption) error {
				pods, ok := list.(*corev1.PodList)
				if !ok {
					return fmt.Errorf("unexpected list type %T", list)
				}
				pods.Items = []corev1.Pod{*pod("team-x", "web-0", "node-a1", webSel, "ReplicaSet")}
				pods.Continue = "next-page"
				return nil
			},
		}).Build()
		r := &reconciler{guestClient: guest, guestAPIReader: reader, platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes}
		if _, _, err := computeDrainWithMachines(t, r, t.Context()); !errors.Is(err, errPodScanIncomplete) {
			t.Fatalf("computeDrain error = %v, want incomplete scan", err)
		}
	})

	t.Run("When aggregate pod budget is exhausted, it should reject the incomplete probe", func(t *testing.T) {
		objs := []runtime.Object{node("node-a1", "pool-a")}
		for i := 0; i < maxPodsPerProbe/maxPodsPerPDB+1; i++ {
			objs = append(objs, structuralPDB("team-x", fmt.Sprintf("web-%d", i), webSel))
		}
		guest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
		podLists := 0
		reader := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(_ context.Context, _ client.WithWatch, list client.ObjectList, _ ...client.ListOption) error {
				pods, ok := list.(*corev1.PodList)
				if !ok {
					return fmt.Errorf("unexpected list type %T", list)
				}
				podLists++
				pods.Items = make([]corev1.Pod, maxPodsPerPDB)
				return nil
			},
		}).Build()
		r := &reconciler{guestClient: guest, guestAPIReader: reader, platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes}
		if _, _, err := computeDrainWithMachines(t, r, t.Context()); !errors.Is(err, errPodScanIncomplete) {
			t.Fatalf("computeDrain error = %v, want incomplete scan", err)
		}
		if podLists != maxPodsPerProbe/maxPodsPerPDB {
			t.Errorf("pod lists = %d, want %d before reaching the shared budget", podLists, maxPodsPerProbe/maxPodsPerPDB)
		}
	})

	t.Run("When the pod list request budget is exhausted, it should reject the incomplete probe", func(t *testing.T) {
		objs := []runtime.Object{node("node-a1", "pool-a")}
		for i := 0; i <= maxPodListRequestsPerProbe; i++ {
			objs = append(objs, structuralPDB("team-x", fmt.Sprintf("web-%d", i), webSel))
		}
		guest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
		podLists := 0
		reader := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(_ context.Context, _ client.WithWatch, list client.ObjectList, _ ...client.ListOption) error {
				if _, ok := list.(*corev1.PodList); !ok {
					return fmt.Errorf("unexpected list type %T", list)
				}
				podLists++
				return nil
			},
		}).Build()
		r := &reconciler{guestClient: guest, guestAPIReader: reader, platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes}
		if _, _, err := computeDrainWithMachines(t, r, t.Context()); !errors.Is(err, errPodScanIncomplete) {
			t.Fatalf("computeDrain error = %v, want incomplete scan", err)
		}
		if podLists != maxPodListRequestsPerProbe {
			t.Errorf("pod lists = %d, want %d", podLists, maxPodListRequestsPerProbe)
		}
	})

	t.Run("When listing matching pods, it should pass a deadline-bound context", func(t *testing.T) {
		guest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
			node("node-a1", "pool-a"), structuralPDB("team-x", "web", webSel),
		).Build()
		deadlineSeen := false
		reader := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, _ client.WithWatch, list client.ObjectList, _ ...client.ListOption) error {
				if _, ok := list.(*corev1.PodList); ok {
					_, deadlineSeen = ctx.Deadline()
				}
				return nil
			},
		}).Build()
		r := &reconciler{guestClient: guest, guestAPIReader: reader, platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes}
		if _, _, err := computeDrainWithMachines(t, r, t.Context()); err != nil {
			t.Fatalf("computeDrain: %v", err)
		}
		if !deadlineSeen {
			t.Error("pod list context has no deadline")
		}
	})

	t.Run("When an uncached pod list times out, it should reject the incomplete probe", func(t *testing.T) {
		guest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
			node("node-a1", "pool-a"), structuralPDB("team-x", "web", webSel),
		).Build()
		reader := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(_ context.Context, _ client.WithWatch, list client.ObjectList, _ ...client.ListOption) error {
				if _, ok := list.(*corev1.PodList); ok {
					return context.DeadlineExceeded
				}
				return nil
			},
		}).Build()
		r := &reconciler{guestClient: guest, guestAPIReader: reader, platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes}
		if _, _, err := computeDrainWithMachines(t, r, t.Context()); !errors.Is(err, errPodScanIncomplete) {
			t.Fatalf("computeDrain error = %v, want incomplete scan", err)
		}
	})

	t.Run("When a guest list hits the total scan deadline, it should reject the incomplete probe", func(t *testing.T) {
		guest := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(_ context.Context, _ client.WithWatch, list client.ObjectList, _ ...client.ListOption) error {
				if _, ok := list.(*corev1.NodeList); ok {
					return context.DeadlineExceeded
				}
				return nil
			},
		}).Build()
		r := &reconciler{guestClient: guest, platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes}
		if _, _, err := computeDrainWithMachines(t, r, t.Context()); !errors.Is(err, errPodScanIncomplete) {
			t.Fatalf("computeDrain error = %v, want incomplete scan", err)
		}
	})

	t.Run("When a PDB status has not observed its current spec, it should reject the scan", func(t *testing.T) {
		pdb := structuralPDB("team-x", "web", webSel)
		pdb.Generation = 2
		pdb.Status.ObservedGeneration = 1
		guest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
			node("node-a1", "pool-a"), pdb,
		).Build()
		r := &reconciler{guestClient: guest, guestAPIReader: guest, platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes}
		if _, _, err := computeDrainWithMachines(t, r, t.Context()); !errors.Is(err, errPDBStatusUnobserved) {
			t.Fatalf("computeDrain error = %v, want unobserved PDB status", err)
		}
	})

	t.Run("When an unobserved PDB does not look structurally blocking, it should skip it instead of blanking the scan", func(t *testing.T) {
		// An unrelated customer PDB mid-edit whose reported status is non-structural must not
		// mark every pool Unknown; it is skipped and re-evaluated once its status settles.
		pdb := structuralPDB("team-x", "web", webSel)
		pdb.Generation = 2
		pdb.Status.ObservedGeneration = 1
		pdb.Status.DesiredHealthy = 1 // reported headroom -> not structurally blocking
		guest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
			node("node-a1", "pool-a"), pdb,
		).Build()
		r := &reconciler{guestClient: guest, guestAPIReader: guest, platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes}
		drain, blocking, err := computeDrainWithMachines(t, r, t.Context())
		if err != nil {
			t.Fatalf("computeDrain error = %v, want no error", err)
		}
		if len(blocking) != 0 {
			t.Errorf("unobserved non-structural PDB must not block, got %v", blocking)
		}
		if len(drain) != 1 || drain[0].State != hyperv1.NodePoolDrainStateDrainable {
			t.Errorf("pool should remain drainable, got %+v", drain)
		}
	})

	t.Run("When a PDB selector is nil, it should ignore the PDB", func(t *testing.T) {
		nilSelPDB := &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-x", Name: "nilsel", Generation: 2},
			Spec:       policyv1.PodDisruptionBudgetSpec{Selector: nil},
			Status:     policyv1.PodDisruptionBudgetStatus{ExpectedPods: 1, DesiredHealthy: 1, ObservedGeneration: 1},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(node("node-a1", "pool-a"), nilSelPDB).Build()
		r := &reconciler{guestClient: c, guestAPIReader: c, platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes}
		drain, blocking, err := computeDrainWithMachines(t, r, context.Background())
		if err != nil {
			t.Fatalf("computeDrain: %v", err)
		}
		if len(blocking) != 0 {
			t.Errorf("nil-selector PDB must not block, got %v", blocking)
		}
		if len(drain) != 1 || drain[0].State != hyperv1.NodePoolDrainStateDrainable {
			t.Errorf("nil-selector PDB must leave the pool drainable, got %+v", drain)
		}
	})

	t.Run("When a pool has mixed NodePool UIDs or an orphan Node, it should be unverified", func(t *testing.T) {
		rscheme := reconcileScheme(t)
		guest := fake.NewClientBuilder().WithScheme(rscheme).WithObjects(
			node("old-node", "pool-a"), node("new-node", "pool-a"),
		).Build()
		mgmt := fake.NewClientBuilder().WithScheme(rscheme).WithObjects(
			testMachine("old-node", "pool-a", "old-uid", "clusters-foo"),
			testMachine("new-node", "pool-a", "new-uid", "clusters-foo"),
		).Build()
		r := &reconciler{mgtClusterClient: mgmt, guestClient: guest, guestAPIReader: guest,
			hcpNamespace: "clusters-foo", platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes}
		drain, _, err := r.computeDrain(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(drain) != 0 {
			t.Fatalf("mixed NodePool UIDs must be unverified, got %+v", drain)
		}

		// Even a lone orphan with no current CAPI Machine must not report Drainable.
		if err := mgmt.Delete(t.Context(), testMachine("old-node", "pool-a", "old-uid", "clusters-foo")); err != nil {
			t.Fatal(err)
		}
		drain, _, err = r.computeDrain(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(drain) != 0 {
			t.Fatalf("orphan Node must leave the pool unverified, got %+v", drain)
		}
	})
}

func reconcileScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := policyv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := hyperv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := capiv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

type capturedEvent struct {
	object      runtime.Object
	eventType   string
	reason      string
	message     string
	annotations map[string]string
}

type capturingRecorder struct{ events []capturedEvent }

func (r *capturingRecorder) Event(object runtime.Object, eventType, reason, message string) {
	r.events = append(r.events, capturedEvent{object: object.DeepCopyObject(), eventType: eventType, reason: reason, message: message})
}

func (r *capturingRecorder) Eventf(object runtime.Object, eventType, reason, messageFmt string, args ...interface{}) {
	r.Event(object, eventType, reason, fmt.Sprintf(messageFmt, args...))
}

func (r *capturingRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventType, reason, messageFmt string, args ...interface{}) {
	r.Eventf(object, eventType, reason, messageFmt, args...)
	r.events[len(r.events)-1].annotations = annotations
}

//nolint:gocyclo // table-driven test aggregating many Reconcile scenarios.
//nolint:hypershiftlinter // reconciler.Reconcile has several focused scenario tests; kept separate for readability rather than one giant canonical test.
func TestReconcile_PatchesStatusAndEmitsEventOnce(t *testing.T) {
	scheme := reconcileScheme(t)
	webSel := map[string]string{"app": "web"}
	guest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
		node("node-a1", "pool-a"), node("node-b1", "pool-b"),
		structuralPDB("team-x", "web", webSel), pod("team-x", "web-0", "node-a1", webSel, "ReplicaSet"),
	).Build()

	hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-foo", Name: "foo"}}
	mgmt := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp,
		testMachine("node-a1", "pool-a", "uid-pool-a", "clusters-foo"),
		testMachine("node-b1", "pool-b", "uid-pool-b", "clusters-foo")).
		WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()

	rec := &capturingRecorder{}
	var logText strings.Builder
	ctx := ctrl.LoggerInto(t.Context(), funcr.New(func(prefix, args string) {
		logText.WriteString(prefix)
		logText.WriteString(args)
	}, funcr.Options{}))
	r := &reconciler{
		mgtClusterClient: mgmt, guestClient: guest, guestAPIReader: guest, recorder: rec,
		hcpNamespace: "clusters-foo", hcpName: "foo",
		platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes,
		lastSuccess:        time.Now(), lastBlocking: map[string]map[string]bool{},
	}

	res, err := r.Reconcile(ctx, reconcile.Request{})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter != resyncInterval {
		t.Errorf("requeue=%v want %v", res.RequeueAfter, resyncInterval)
	}

	got := &hyperv1.HostedControlPlane{}
	if err := mgmt.Get(context.Background(), client.ObjectKeyFromObject(hcp), got); err != nil {
		t.Fatal(err)
	}
	byPool := map[string]hyperv1.NodePoolDrain{}
	for _, e := range got.Status.NodePoolDrain {
		byPool[e.NodePoolName] = e
	}
	if a := byPool["pool-a"]; a.State != hyperv1.NodePoolDrainStateBlocked || ptr.Deref(a.BlockingCount, 0) != 1 {
		t.Errorf("pool-a: want blocked=true count=1, got %+v", a)
	}
	if b := byPool["pool-b"]; b.State != hyperv1.NodePoolDrainStateDrainable {
		t.Errorf("pool-b: want blocked=false, got %+v", b)
	}
	if c := meta.FindStatusCondition(got.Status.Conditions, hyperv1.HostedControlPlaneNodePoolDrainUpToDate); c == nil || c.Status != metav1.ConditionTrue {
		t.Errorf("UpToDate: want True, got %+v", c)
	}
	if got.Status.NodePoolDrainLastProbeTime == nil || time.Since(got.Status.NodePoolDrainLastProbeTime.Time) > time.Minute {
		t.Errorf("last probe time was not refreshed: %v", got.Status.NodePoolDrainLastProbeTime)
	}
	// Force an old persisted timestamp to prove a second successful probe refreshes it.
	oldProbeTime := metav1.NewTime(time.Now().Add(-time.Hour))
	if err := statuspatching.PatchStatus(context.Background(), mgmt, got, func() error {
		got.Status.NodePoolDrainLastProbeTime = &oldProbeTime
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Inspect the involved object as well as event text; FakeRecorder's channel
	// omits object metadata and cannot detect a PDB reference leak.
	if len(rec.events) != 1 {
		t.Fatalf("events = %d, want one transition event", len(rec.events))
	}
	if ns, ok := rec.events[0].object.(*corev1.Namespace); !ok || ns.Name != metav1.NamespaceDefault || ns.Namespace != "" {
		t.Errorf("event involved object = %#v, want generic default Namespace", rec.events[0].object)
	}
	if rec.events[0].reason != "DrainBlocked" || rec.events[0].eventType != corev1.EventTypeWarning {
		t.Errorf("unexpected event fields: %+v", rec.events[0])
	}
	for _, identifier := range []string{"team-x", "web-0", "pool-a"} {
		if strings.Contains(fmt.Sprintf("%#v", rec.events[0]), identifier) || strings.Contains(logText.String(), identifier) {
			t.Errorf("event or log leaked %q", identifier)
		}
	}

	// Second reconcile with unchanged state: no new event (transition dedup).
	if _, err := r.Reconcile(ctx, reconcile.Request{}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if err := mgmt.Get(context.Background(), client.ObjectKeyFromObject(hcp), got); err != nil {
		t.Fatal(err)
	}
	if got.Status.NodePoolDrainLastProbeTime == nil || !got.Status.NodePoolDrainLastProbeTime.After(oldProbeTime.Time) {
		t.Errorf("last probe time did not advance: old=%v second=%v", oldProbeTime, got.Status.NodePoolDrainLastProbeTime)
	}
	if len(rec.events) != 1 {
		t.Errorf("unchanged state emitted %d events, want one", len(rec.events))
	}

	// Clearing and then reblocking the same PDB should emit a new transition event.
	pdb := &policyv1.PodDisruptionBudget{}
	pdbKey := client.ObjectKey{Namespace: "team-x", Name: "web"}
	if err := guest.Get(context.Background(), pdbKey, pdb); err != nil {
		t.Fatal(err)
	}
	pdb.Status.DesiredHealthy = 0
	if err := guest.Status().Update(ctx, pdb); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, reconcile.Request{}); err != nil {
		t.Fatalf("drainable reconcile: %v", err)
	}
	if err := mgmt.Get(ctx, client.ObjectKeyFromObject(hcp), got); err != nil {
		t.Fatal(err)
	}
	if len(got.Status.NodePoolDrain) != 2 || byPoolState(got.Status.NodePoolDrain, "pool-a") != hyperv1.NodePoolDrainStateDrainable {
		t.Errorf("pool-a did not clear after PDB status update: %+v", got.Status.NodePoolDrain)
	}
	if len(rec.events) != 1 {
		t.Errorf("PDB clearing emitted %d events, want one", len(rec.events))
	}

	if err := guest.Get(ctx, pdbKey, pdb); err != nil {
		t.Fatal(err)
	}
	pdb.Status.DesiredHealthy = pdb.Status.ExpectedPods
	if err := guest.Status().Update(ctx, pdb); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, reconcile.Request{}); err != nil {
		t.Fatalf("reblocked reconcile: %v", err)
	}
	if len(rec.events) != 2 {
		t.Fatalf("reblock events = %d, want two", len(rec.events))
	}
	for _, identifier := range []string{"team-x", "web-0", "pool-a"} {
		if strings.Contains(fmt.Sprintf("%#v", rec.events[1]), identifier) || strings.Contains(logText.String(), identifier) {
			t.Errorf("reblock event or log leaked %q", identifier)
		}
	}
}

func byPoolState(entries []hyperv1.NodePoolDrain, pool string) hyperv1.NodePoolDrainState {
	for _, entry := range entries {
		if entry.NodePoolName == pool {
			return entry.State
		}
	}
	return ""
}

//nolint:gocyclo // table-driven test aggregating many Reconcile staleness scenarios.
//nolint:hypershiftlinter // reconciler.Reconcile has several focused scenario tests; kept separate for readability rather than one giant canonical test.
func TestReconcile_StaleMarksUpToDateFalse(t *testing.T) {
	scheme := reconcileScheme(t)
	var logText strings.Builder
	ctx := ctrl.LoggerInto(t.Context(), funcr.New(func(prefix, args string) {
		logText.WriteString(prefix)
		logText.WriteString(args)
	}, funcr.Options{}))
	guest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
		node("node-a1", "pool-a"), structuralPDB("team-sensitive", "private-pdb", map[string]string{"app": "web"}),
	).Build()
	// Force the pod list to fail so the internal error contains a customer namespace.
	reader := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return fmt.Errorf("request for team-sensitive/private-pdb and pod web-0 was denied")
		},
	}).Build()

	hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-foo", Name: "foo"}}
	mgmt := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp,
		testMachine("node-a1", "pool-a", "uid-pool-a", "clusters-foo")).
		WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()

	r := &reconciler{
		mgtClusterClient: mgmt, guestClient: guest, guestAPIReader: reader,
		recorder:     record.NewFakeRecorder(10),
		hcpNamespace: "clusters-foo", hcpName: "foo",
		platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes,
		lastSuccess:        time.Now().Add(-30 * time.Minute), // past the grace window
		lastBlocking:       map[string]map[string]bool{},
	}

	if _, err := r.Reconcile(ctx, reconcile.Request{}); err == nil {
		t.Fatal("expected an error when the guest is unreachable")
	} else {
		for _, identifier := range []string{"team-sensitive", "private-pdb", "web-0"} {
			if strings.Contains(err.Error(), identifier) {
				t.Errorf("returned reconciliation error leaked %q: %v", identifier, err)
			}
			if strings.Contains(logText.String(), identifier) {
				t.Errorf("reconciliation log leaked %q", identifier)
			}
		}
	}

	got := &hyperv1.HostedControlPlane{}
	if err := mgmt.Get(context.Background(), client.ObjectKeyFromObject(hcp), got); err != nil {
		t.Fatal(err)
	}
	c := meta.FindStatusCondition(got.Status.Conditions, hyperv1.HostedControlPlaneNodePoolDrainUpToDate)
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "GuestUnreachable" {
		t.Errorf("UpToDate: want False/GuestUnreachable, got %+v", c)
	}
	if c != nil && (strings.Contains(c.Message, "team-sensitive") || strings.Contains(c.Message, "private-pdb")) {
		t.Errorf("UpToDate message leaked customer identifiers: %q", c.Message)
	}

	t.Run("When the pod list is incomplete, it should immediately mark readiness unverified", func(t *testing.T) {
		incompleteReader := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(_ context.Context, _ client.WithWatch, list client.ObjectList, _ ...client.ListOption) error {
				pods, ok := list.(*corev1.PodList)
				if !ok {
					return fmt.Errorf("unexpected list type %T", list)
				}
				pods.Continue = "next-page"
				return nil
			},
		}).Build()
		freshHCP := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-bar", Name: "bar"}}
		freshMgmt := fake.NewClientBuilder().WithScheme(scheme).WithObjects(freshHCP).
			WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
		incomplete := &reconciler{
			mgtClusterClient: freshMgmt, guestClient: guest, guestAPIReader: incompleteReader,
			recorder: record.NewFakeRecorder(10), hcpNamespace: freshHCP.Namespace, hcpName: freshHCP.Name,
			platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes,
			lastSuccess:        time.Now(), lastBlocking: map[string]map[string]bool{},
		}
		result, err := incomplete.Reconcile(t.Context(), reconcile.Request{})
		if err != nil || result.RequeueAfter != resyncInterval {
			t.Fatalf("Reconcile = (%v, %v), want scheduled retry without error", result, err)
		}
		got := &hyperv1.HostedControlPlane{}
		if err := freshMgmt.Get(t.Context(), client.ObjectKeyFromObject(freshHCP), got); err != nil {
			t.Fatal(err)
		}
		condition := meta.FindStatusCondition(got.Status.Conditions, hyperv1.HostedControlPlaneNodePoolDrainUpToDate)
		if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "PodScanIncomplete" {
			t.Errorf("UpToDate: want False/PodScanIncomplete, got %+v", condition)
		}
		if got.Status.NodePoolDrainLastProbeTime != nil {
			t.Errorf("incomplete scan must not refresh probe time: %v", got.Status.NodePoolDrainLastProbeTime)
		}
	})

	t.Run("When a PDB status has not observed its current spec, it should publish no drainable result", func(t *testing.T) {
		unobservedPDB := structuralPDB("team-private", "pdb-private", map[string]string{"app": "web"})
		unobservedPDB.Generation = 2
		unobservedPDB.Status.ObservedGeneration = 1
		unobservedGuest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
			node("node-a1", "pool-a"), unobservedPDB,
		).Build()
		unobservedHCP := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-unobserved", Name: "unobserved"}}
		unobservedMgmt := fake.NewClientBuilder().WithScheme(scheme).WithObjects(unobservedHCP).
			WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
		unobserved := &reconciler{
			mgtClusterClient: unobservedMgmt, guestClient: unobservedGuest, guestAPIReader: unobservedGuest,
			recorder: record.NewFakeRecorder(10), hcpNamespace: unobservedHCP.Namespace, hcpName: unobservedHCP.Name,
			platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes,
			lastSuccess:        time.Now(), lastBlocking: map[string]map[string]bool{},
		}
		result, err := unobserved.Reconcile(t.Context(), reconcile.Request{})
		if err != nil || result.RequeueAfter != resyncInterval {
			t.Fatalf("Reconcile = (%v, %v), want scheduled retry without error", result, err)
		}
		got := &hyperv1.HostedControlPlane{}
		if err := unobservedMgmt.Get(t.Context(), client.ObjectKeyFromObject(unobservedHCP), got); err != nil {
			t.Fatal(err)
		}
		condition := meta.FindStatusCondition(got.Status.Conditions, hyperv1.HostedControlPlaneNodePoolDrainUpToDate)
		if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "PDBStatusUnobserved" {
			t.Errorf("UpToDate: want False/PDBStatusUnobserved, got %+v", condition)
		}
		if len(got.Status.NodePoolDrain) != 0 || got.Status.NodePoolDrainLastProbeTime != nil {
			t.Errorf("unverified scan published drain results: status=%+v", got.Status)
		}
	})

	t.Run("When publishing scan status fails, it should mark the previous result unverified", func(t *testing.T) {
		healthyGuest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(node("node-a1", "pool-a")).Build()
		oldProbe := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
		staleHCP := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-write-failure", Name: "write-failure"}}
		staleHCP.Status.NodePoolDrain = []hyperv1.NodePoolDrain{{
			NodePoolName: "pool-a", State: hyperv1.NodePoolDrainStateBlocked,
		}}
		staleHCP.Status.NodePoolDrainLastProbeTime = &oldProbe
		staleHCP.Status.Conditions = []metav1.Condition{{
			Type: hyperv1.HostedControlPlaneNodePoolDrainUpToDate, Status: metav1.ConditionTrue,
			Reason: hyperv1.AsExpectedReason, LastTransitionTime: metav1.Now(),
		}}
		statusPatches := 0
		mgmtWithWriteFailure := fake.NewClientBuilder().WithScheme(scheme).WithObjects(staleHCP).
			WithStatusSubresource(&hyperv1.HostedControlPlane{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					if subResource == "status" {
						statusPatches++
						if statusPatches == 1 {
							return fmt.Errorf("simulated status write failure")
						}
					}
					return c.SubResource(subResource).Patch(ctx, obj, patch, opts...)
				},
			}).Build()
		writer := &reconciler{
			mgtClusterClient: mgmtWithWriteFailure, guestClient: healthyGuest, guestAPIReader: healthyGuest,
			recorder: record.NewFakeRecorder(10), hcpNamespace: staleHCP.Namespace, hcpName: staleHCP.Name,
			platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes,
			lastSuccess:        time.Now(), lastBlocking: map[string]map[string]bool{},
		}
		if _, err := writer.Reconcile(t.Context(), reconcile.Request{}); err == nil {
			t.Fatal("expected the failed status write to be returned")
		}

		got := &hyperv1.HostedControlPlane{}
		if err := mgmtWithWriteFailure.Get(t.Context(), client.ObjectKeyFromObject(staleHCP), got); err != nil {
			t.Fatal(err)
		}
		condition := meta.FindStatusCondition(got.Status.Conditions, hyperv1.HostedControlPlaneNodePoolDrainUpToDate)
		if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "StatusWriteFailed" {
			t.Errorf("UpToDate: want False/StatusWriteFailed, got %+v", condition)
		}
		if got.Status.NodePoolDrainLastProbeTime == nil || !got.Status.NodePoolDrainLastProbeTime.Equal(&oldProbe) {
			t.Errorf("failed status write changed the previous probe time: %v", got.Status.NodePoolDrainLastProbeTime)
		}
		if statusPatches != 2 {
			t.Errorf("status patches = %d, want failed data write followed by freshness update", statusPatches)
		}
	})
}
