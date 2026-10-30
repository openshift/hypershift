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
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func node(name, pool string) *corev1.Node {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if pool != "" {
		n.Labels = map[string]string{hyperv1.NodePoolLabel: pool}
	}
	return n
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

func TestComputeDrain(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = policyv1.AddToScheme(scheme)

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

	drain, blocking, err := r.computeDrain(context.Background())
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
			drain, _, err := r.computeDrain(t.Context())
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
		if _, _, err := r.computeDrain(t.Context()); !errors.Is(err, errPodScanIncomplete) {
			t.Fatalf("computeDrain error = %v, want incomplete scan", err)
		}
	})
}

func TestComputeDrain_NilSelectorIgnored(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = policyv1.AddToScheme(scheme)

	nilSelPDB := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-x", Name: "nilsel"},
		Spec:       policyv1.PodDisruptionBudgetSpec{Selector: nil},
		Status:     policyv1.PodDisruptionBudgetStatus{ExpectedPods: 1, DesiredHealthy: 1},
	}
	objs := []runtime.Object{node("node-a1", "pool-a"), nilSelPDB}

	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
	r := &reconciler{guestClient: c, guestAPIReader: c, platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes}

	drain, blocking, err := r.computeDrain(context.Background())
	if err != nil {
		t.Fatalf("computeDrain: %v", err)
	}
	if len(blocking) != 0 {
		t.Errorf("nil-selector PDB must not block, got %v", blocking)
	}
	for _, e := range drain {
		if e.State != hyperv1.NodePoolDrainStateDrainable {
			t.Errorf("no pool should be blocked, got %+v", e)
		}
	}
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
	return s
}

func TestReconcile_PatchesStatusAndEmitsEventOnce(t *testing.T) {
	scheme := reconcileScheme(t)
	webSel := map[string]string{"app": "web"}
	guest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
		node("node-a1", "pool-a"), node("node-b1", "pool-b"),
		structuralPDB("team-x", "web", webSel), pod("team-x", "web-0", "node-a1", webSel, "ReplicaSet"),
	).Build()

	hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-foo", Name: "foo"}}
	mgmt := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp).
		WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()

	rec := record.NewFakeRecorder(10)
	r := &reconciler{
		mgtClusterClient: mgmt, guestClient: guest, guestAPIReader: guest, recorder: rec,
		hcpNamespace: "clusters-foo", hcpName: "foo",
		platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes,
		lastSuccess:        time.Now(), lastBlocking: map[string]map[string]bool{},
	}

	res, err := r.Reconcile(context.Background(), reconcile.Request{})
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

	// Exactly one transition event on the first reconcile.
	select {
	case <-rec.Events:
	default:
		t.Errorf("expected a DrainBlocked event on first reconcile")
	}

	// Second reconcile with unchanged state: no new event (transition dedup).
	if _, err := r.Reconcile(context.Background(), reconcile.Request{}); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if err := mgmt.Get(context.Background(), client.ObjectKeyFromObject(hcp), got); err != nil {
		t.Fatal(err)
	}
	if got.Status.NodePoolDrainLastProbeTime == nil || !got.Status.NodePoolDrainLastProbeTime.After(oldProbeTime.Time) {
		t.Errorf("last probe time did not advance: old=%v second=%v", oldProbeTime, got.Status.NodePoolDrainLastProbeTime)
	}
	select {
	case e := <-rec.Events:
		t.Errorf("unexpected second event: %s", e)
	default:
	}
}

func TestReconcile_StaleMarksUpToDateFalse(t *testing.T) {
	scheme := reconcileScheme(t)
	guest := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
		node("node-a1", "pool-a"), structuralPDB("team-sensitive", "private-pdb", map[string]string{"app": "web"}),
	).Build()
	// Force the pod list to fail so the internal error contains a customer namespace.
	reader := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return fmt.Errorf("guest unreachable")
		},
	}).Build()

	hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-foo", Name: "foo"}}
	mgmt := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp).
		WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()

	r := &reconciler{
		mgtClusterClient: mgmt, guestClient: guest, guestAPIReader: reader,
		recorder:     record.NewFakeRecorder(10),
		hcpNamespace: "clusters-foo", hcpName: "foo",
		platformNSPrefixes: pdbdrain.DefaultPlatformNamespacePrefixes,
		lastSuccess:        time.Now().Add(-30 * time.Minute), // past the grace window
		lastBlocking:       map[string]map[string]bool{},
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{}); err == nil {
		t.Fatal("expected an error when the guest is unreachable")
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
}
