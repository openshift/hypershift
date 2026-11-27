package ignitionconsumer

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	pkgmanifests "github.com/openshift/hypershift/pkg/manifests"
	"github.com/openshift/hypershift/support/api"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestEnqueueNodePool(t *testing.T) {
	g := NewWithT(t)

	withAnno := &hyperv1.IgnitionPayload{
		ObjectMeta: metav1.ObjectMeta{Name: "np-1", Namespace: "hcp", Annotations: map[string]string{nodePoolAnnotation: "clusters/np-1"}},
	}
	reqs := EnqueueNodePool(context.Background(), withAnno)
	g.Expect(reqs).To(HaveLen(1))
	g.Expect(reqs[0].Namespace).To(Equal("clusters"))
	g.Expect(reqs[0].Name).To(Equal("np-1"))

	// A CR without the back-reference annotation is ignored (foreign/unlabeled).
	noAnno := &hyperv1.IgnitionPayload{ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "hcp"}}
	g.Expect(EnqueueNodePool(context.Background(), noAnno)).To(BeEmpty())

	// A malformed annotation is ignored rather than producing a bogus request.
	bad := &hyperv1.IgnitionPayload{ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "hcp", Annotations: map[string]string{nodePoolAnnotation: "no-slash"}}}
	g.Expect(EnqueueNodePool(context.Background(), bad)).To(BeEmpty())
}

func TestAdvanceRetiredRolloutGeneration(t *testing.T) {
	ctx := context.Background()

	newCR := func() *hyperv1.IgnitionPayload {
		return &hyperv1.IgnitionPayload{
			ObjectMeta: metav1.ObjectMeta{Name: "np-1", Namespace: "hcp"},
			Spec:       hyperv1.IgnitionPayloadSpec{ReleaseImage: "img", PullSecret: hyperv1.SecretReference{Name: "ps"}, RetiredRolloutGeneration: 2},
		}
	}

	t.Run("advances forward but never backward", func(t *testing.T) {
		g := NewWithT(t)
		c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		cr := newCR()
		g.Expect(c.Create(ctx, cr)).To(Succeed())

		g.Expect(AdvanceRetiredRolloutGeneration(ctx, c, cr, 5)).To(Succeed())
		g.Expect(cr.Spec.RetiredRolloutGeneration).To(Equal(int64(5)))

		g.Expect(AdvanceRetiredRolloutGeneration(ctx, c, cr, 3)).To(Succeed())
		fetched := &hyperv1.IgnitionPayload{}
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(cr), fetched)).To(Succeed())
		g.Expect(fetched.Spec.RetiredRolloutGeneration).To(Equal(int64(5)))
	})

	// The monotonic contract must be enforced against the server value, not a possibly-stale
	// in-memory object, so a stale caller cannot regress retiredRolloutGeneration (which would
	// resurrect an already-freed payload).
	t.Run("a stale caller cannot regress the server value", func(t *testing.T) {
		g := NewWithT(t)
		c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		cr := newCR()
		g.Expect(c.Create(ctx, cr)).To(Succeed())
		g.Expect(AdvanceRetiredRolloutGeneration(ctx, c, cr, 5)).To(Succeed())

		stale := newCR() // old view at retiredRolloutGeneration=2
		g.Expect(AdvanceRetiredRolloutGeneration(ctx, c, stale, 3)).To(Succeed())

		fetched := &hyperv1.IgnitionPayload{}
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(cr), fetched)).To(Succeed())
		g.Expect(fetched.Spec.RetiredRolloutGeneration).To(Equal(int64(5)))
	})
}

// generationTrackingClient increments an IgnitionPayload's metadata.generation when its spec
// changes, as the API server does. The controller-runtime fake client never changes it, which would
// hide a gate that compares observedGeneration against the generation from before a spec write.
type generationTrackingClient struct {
	client.Client
}

func (c generationTrackingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if cr, ok := obj.(*hyperv1.IgnitionPayload); ok && cr.Generation == 0 {
		cr.Generation = 1
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c generationTrackingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if cr, ok := obj.(*hyperv1.IgnitionPayload); ok {
		stored := &hyperv1.IgnitionPayload{}
		if err := c.Client.Get(ctx, client.ObjectKeyFromObject(cr), stored); err != nil {
			return err
		}
		cr.Generation = stored.Generation
		if !equality.Semantic.DeepEqual(stored.Spec, cr.Spec) {
			cr.Generation++
		}
	}
	return c.Client.Update(ctx, obj, opts...)
}

// consumerSpec is the spec Reconcile authors for nodePool, hc, and in.
func consumerSpec(nodePool *hyperv1.NodePool, hc *hyperv1.HostedCluster, in Inputs) hyperv1.IgnitionPayloadSpec {
	rolloutRefs, mgmtRefs, globalName := classifyConfigs(nodePool.GetName(), in.UserConfigs, in.CoreConfigs, in.NTOConfigs)
	return ignitionPayloadSpec(nodePool, hc, rolloutRefs, mgmtRefs, globalName, in.OSStream, in.AdditionalRolloutInputs)
}

// seedConsumerCR creates a CR with spec at metadata.generation 2 whose generator has already
// published status.current, plus the matching store entry, so the consumer end-to-end path has a
// payload to reference. observedGeneration is the spec generation the generator reports on
// PayloadGenerated: 2 means status.current reflects spec, 1 means the generator has not processed it.
func seedConsumerCR(ctx context.Context, g *WithT, hcpNS string, spec hyperv1.IgnitionPayloadSpec, observedGeneration int64) (client.Client, payloadstore.PayloadStore) {
	seedCR := &hyperv1.IgnitionPayload{
		ObjectMeta: metav1.ObjectMeta{Name: "np-1", Namespace: hcpNS, Generation: 2},
		Spec:       spec,
	}
	c := generationTrackingClient{fake.NewClientBuilder().WithScheme(api.Scheme).
		WithStatusSubresource(&hyperv1.IgnitionPayload{}).
		WithObjects(seedCR).Build()}
	seedCR.Status.Current = hyperv1.PayloadReference{ConfigHash: "cfg1", RolloutHash: "roll1", UserDataSecret: hyperv1.SecretReference{Name: "np-1-1"}, RolloutGeneration: 1}
	seedCR.Status.Conditions = []metav1.Condition{{
		Type: hyperv1.IgnitionPayloadPayloadGeneratedCondition, Status: metav1.ConditionTrue, Reason: "AsExpected",
		ObservedGeneration: observedGeneration, LastTransitionTime: metav1.Now(),
	}}
	g.Expect(c.Status().Update(ctx, seedCR)).To(Succeed())

	store := payloadstore.NewMemStore()
	g.Expect(store.Put(ctx, payloadstore.OwnerRef{Namespace: hcpNS, Name: "np-1"}, 1, "cfg1", []byte("PAYLOAD"))).To(Succeed())
	return c, store
}

func TestReconcile(t *testing.T) {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"},
		Spec:       hyperv1.HostedClusterSpec{PullSecret: corev1.LocalObjectReference{Name: "ps"}},
	}
	hcpNS := pkgmanifests.HostedControlPlaneNamespace("clusters", "hc")

	t.Run("references the generator-authored userData Secret and writes the InPlace compat Secret", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()
		nodePool := testNodePool("np-1")
		nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace
		in := Inputs{
			ReleaseVersion:          "4.23.0",
			OSStream:                "rhel-9",
			AdditionalRolloutInputs: "private=false,port=6443",
			UserConfigs:             []corev1.ConfigMap{{ObjectMeta: metav1.ObjectMeta{Name: "user-mc", Namespace: "clusters"}, Data: map[string]string{configDataKey: "u"}}},
			HAProxyRaw:              "haproxy-raw",
			MachineSetName:          "np-1",
		}
		// The generator has processed exactly the spec the consumer writes, so the write leaves
		// metadata.generation (and the gate) unchanged.
		c, store := seedConsumerCR(ctx, g, hcpNS, consumerSpec(nodePool, hc, in), 2)

		// The consumer returns the generator-authored userData Secret name from status.current; it
		// never mints userdata itself.
		userDataName, err := Reconcile(ctx, c, store, nodePool, hc, in)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(userDataName).To(Equal("np-1-1"))

		// CR spec authored with the classified refs + endpoint rollout input + finalizer; status
		// preserved.
		cr := &hyperv1.IgnitionPayload{}
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: "np-1"}, cr)).To(Succeed())
		g.Expect(sets.New(cr.Finalizers...).Has(consumerFinalizer)).To(BeTrue())
		g.Expect(refNames(cr.Spec.RolloutConfigMaps)).To(ContainElement(userConfigCopyName("np-1", "user-mc")))
		g.Expect(refNames(cr.Spec.ManagementConfigMaps)).To(Equal([]string{haproxyConfigMapName("np-1")}))
		g.Expect(cr.Spec.RolloutGlobalConfig.Name).To(Equal(rolloutGlobalConfigMapName("np-1")))
		g.Expect(cr.Spec.AdditionalRolloutInputs).To(Equal("private=false,port=6443"))
		g.Expect(cr.Status.Current.UserDataSecret.Name).To(Equal("np-1-1"))

		// InPlace: legacy compat Secret written for the HCCO (name keyed by configHash).
		legacy := &corev1.Secret{}
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: "token-np-1-cfg1"}, legacy)).To(Succeed())
		g.Expect(string(legacy.Data["release-version"])).To(Equal("4.23.0"))
	})

	// A steady-state reconcile must not rewrite the CR (which would churn its resourceVersion and,
	// once wired, thrash fleet rollouts by persisting intermediate empty-ref specs).
	t.Run("a steady-state reconcile does not churn the CR", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()
		nodePool := testNodePool("np-1")
		nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace
		in := Inputs{
			ReleaseVersion: "4.23.0",
			UserConfigs:    []corev1.ConfigMap{{ObjectMeta: metav1.ObjectMeta{Name: "user-mc", Namespace: "clusters"}, Data: map[string]string{configDataKey: "u"}}},
			HAProxyRaw:     "haproxy-raw",
			MachineSetName: "np-1",
		}
		c, store := seedConsumerCR(ctx, g, hcpNS, consumerSpec(nodePool, hc, in), 2)

		_, err := Reconcile(ctx, c, store, nodePool, hc, in)
		g.Expect(err).ToNot(HaveOccurred())
		cr1 := &hyperv1.IgnitionPayload{}
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: "np-1"}, cr1)).To(Succeed())

		_, err = Reconcile(ctx, c, store, nodePool, hc, in)
		g.Expect(err).ToNot(HaveOccurred())
		cr2 := &hyperv1.IgnitionPayload{}
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: "np-1"}, cr2)).To(Succeed())

		g.Expect(cr2.ResourceVersion).To(Equal(cr1.ResourceVersion))
		// The persisted spec always carries the full rollout refs (never an empty intermediate).
		g.Expect(cr2.Spec.RolloutConfigMaps).ToNot(BeEmpty())
	})

	// Until the generator has processed the spec the consumer just wrote (e.g. a release change),
	// status.current is still the previous payload: keep machines on its userData Secret, but do not
	// write the InPlace compat Secret, which would pair the new release version with the old payload.
	t.Run("no InPlace compat Secret until the generator has processed the latest spec", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()
		nodePool := testNodePool("np-1")
		nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace
		in := Inputs{ReleaseVersion: "4.24.0", HAProxyRaw: "h", MachineSetName: "np-1"}
		c, store := seedConsumerCR(ctx, g, hcpNS, consumerSpec(nodePool, hc, in), 1)

		userDataName, err := Reconcile(ctx, c, store, nodePool, hc, in)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(userDataName).To(Equal("np-1-1"))

		err = c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: "token-np-1-cfg1"}, &corev1.Secret{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "compat Secret must not be written for a stale status.current")
	})

	// The generator processed the previous spec (observedGeneration == generation before the write),
	// then the consumer's own write of a new release changes the spec and bumps the generation. The gate
	// must compare against the generation after that write.
	t.Run("no InPlace compat Secret when the consumer's own write changes the spec", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()
		previous := testNodePool("np-1")
		previous.Spec.Release.Image = "quay.io/openshift/release:4.22"
		nodePool := testNodePool("np-1")
		nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace
		in := Inputs{ReleaseVersion: "4.23.0", HAProxyRaw: "h", MachineSetName: "np-1"}
		c, store := seedConsumerCR(ctx, g, hcpNS, consumerSpec(previous, hc, in), 2)

		userDataName, err := Reconcile(ctx, c, store, nodePool, hc, in)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(userDataName).To(Equal("np-1-1"))

		cr := &hyperv1.IgnitionPayload{}
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: "np-1"}, cr)).To(Succeed())
		g.Expect(cr.Generation).To(Equal(int64(3)), "the release change must bump the generation")
		err = c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: "token-np-1-cfg1"}, &corev1.Secret{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "compat Secret must not be written for the previous spec's payload")
	})

	t.Run("no userData Secret name until the generator publishes status.current", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()
		nodePool := testNodePool("np-1")
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(&hyperv1.IgnitionPayload{}).Build()
		store := payloadstore.NewMemStore()

		in := Inputs{ReleaseVersion: "4.23.0", HAProxyRaw: "h", MachineSetName: "np-1"}
		// No status.current yet: the consumer authors the CR but returns no userData Secret name.
		userDataName, err := Reconcile(ctx, c, store, nodePool, hc, in)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(userDataName).To(BeEmpty())
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: "np-1"}, &hyperv1.IgnitionPayload{})).To(Succeed())
	})
}

func TestFinalize(t *testing.T) {
	ctx := context.Background()
	hcpNS := "clusters-hc"

	t.Run("removes only the consumer finalizer", func(t *testing.T) {
		g := NewWithT(t)
		cr := &hyperv1.IgnitionPayload{
			ObjectMeta: metav1.ObjectMeta{Name: "np-1", Namespace: hcpNS, Finalizers: []string{consumerFinalizer, "other/keep"}},
			Spec:       hyperv1.IgnitionPayloadSpec{ReleaseImage: "img", PullSecret: hyperv1.SecretReference{Name: "ps"}},
		}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cr).Build()

		g.Expect(Finalize(ctx, c, hcpNS, "np-1")).To(Succeed())

		// The consumer finalizer is removed; other controllers' finalizers are left intact so their own
		// cleanup can still run.
		fetched := &hyperv1.IgnitionPayload{}
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: "np-1"}, fetched)).To(Succeed())
		g.Expect(sets.New(fetched.Finalizers...).Has(consumerFinalizer)).To(BeFalse())
		g.Expect(sets.New(fetched.Finalizers...).Has("other/keep")).To(BeTrue())

		// Absent CR is a no-op.
		g.Expect(Finalize(ctx, c, hcpNS, "missing")).To(Succeed())
	})

	// A finalizer another controller adds between the consumer's read and its patch must survive: the
	// patch conflicts instead of overwriting the list.
	t.Run("keeps a finalizer added concurrently", func(t *testing.T) {
		g := NewWithT(t)
		const generatorFinalizer = "hypershift.openshift.io/ignition-payload-store-cleanup"
		cr := &hyperv1.IgnitionPayload{
			ObjectMeta: metav1.ObjectMeta{Name: "np-1", Namespace: hcpNS, Finalizers: []string{consumerFinalizer}},
			Spec:       hyperv1.IgnitionPayloadSpec{ReleaseImage: "img", PullSecret: hyperv1.SecretReference{Name: "ps"}},
		}
		raced := false
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cr).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if !raced {
						raced = true
						concurrent := &hyperv1.IgnitionPayload{}
						if err := cl.Get(ctx, client.ObjectKeyFromObject(obj), concurrent); err != nil {
							return err
						}
						concurrent.Finalizers = append(concurrent.Finalizers, generatorFinalizer)
						if err := cl.Update(ctx, concurrent); err != nil {
							return err
						}
					}
					return cl.Patch(ctx, obj, patch, opts...)
				},
			}).Build()

		err := Finalize(ctx, c, hcpNS, "np-1")
		g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected a conflict, got %v", err)

		fetched := &hyperv1.IgnitionPayload{}
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: "np-1"}, fetched)).To(Succeed())
		g.Expect(fetched.Finalizers).To(ContainElement(generatorFinalizer))

		// The retry re-reads and removes only the consumer finalizer.
		g.Expect(Finalize(ctx, c, hcpNS, "np-1")).To(Succeed())
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: "np-1"}, fetched)).To(Succeed())
		g.Expect(fetched.Finalizers).To(Equal([]string{generatorFinalizer}))
	})
}
