//go:build envtest

package envtest

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	ignitionpayloadserver "github.com/openshift/hypershift/ignition-payload-server"
	hyperapi "github.com/openshift/hypershift/support/api"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"
	"github.com/openshift/hypershift/support/statuspatching"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// ignitionReachedFieldManager mirrors the serving tier's SSA field manager.
const ignitionReachedFieldManager = "ignition-payload-server"

// racingReader wraps the serving tier's uncached reader and runs race once, immediately after the
// first IgnitionPayload read returns. That lands a concurrent write between SetIgnitionReached's gate
// read and its resourceVersion-pinned apply — the window the pin exists to protect.
type racingReader struct {
	crclient.Reader
	race  func()
	once  sync.Once
	mu    sync.Mutex
	reads int
}

func (r *racingReader) Get(ctx context.Context, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if _, ok := obj.(*hyperv1.IgnitionPayload); ok {
		r.mu.Lock()
		r.reads++
		r.mu.Unlock()
		r.once.Do(r.race)
	}
	return nil
}

func (r *racingReader) readCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

func payloadRef(gen int64, configHash, rolloutHash, userData string) hyperv1.PayloadReference {
	return hyperv1.PayloadReference{
		ConfigHash:        configHash,
		RolloutHash:       rolloutHash,
		UserDataSecret:    hyperv1.SecretReference{Name: userData},
		RolloutGeneration: gen,
	}
}

// ignitionReachedFields returns the status fieldsV1 the serving tier's field manager owns.
func ignitionReachedFields(obj *hyperv1.IgnitionPayload) string {
	for _, mf := range obj.ManagedFields {
		if mf.Manager == ignitionReachedFieldManager && mf.Subresource == "status" && mf.FieldsV1 != nil {
			return mf.FieldsV1.GetRawString()
		}
	}
	return ""
}

var _ = Describe("IgnitionPayload IgnitionReached recording", Label("ignition-payload"), Ordered, func() {
	var c crclient.Client

	BeforeAll(func() {
		_, thisFile, _, _ := runtime.Caller(0)
		crdPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "cmd", "install", "assets", "crds",
			"hypershift-operator", "zz_generated.crd-manifests", "ignitionpayloads-TechPreviewNoUpgrade.crd.yaml")
		opts := envtest.CRDInstallOptions{Paths: []string{crdPath}, ErrorIfPathMissing: true}
		crds, err := envtest.InstallCRDs(cfg, opts)
		Expect(err).NotTo(HaveOccurred(), "failed to install the IgnitionPayload CRD")
		Expect(envtest.WaitForCRDs(cfg, crds, opts)).To(Succeed())
		DeferCleanup(func() {
			Expect(envtest.UninstallCRDs(cfg, envtest.CRDInstallOptions{CRDs: crds})).To(Succeed())
		})

		c, err = crclient.New(cfg, crclient.Options{Scheme: hyperapi.Scheme})
		Expect(err).NotTo(HaveOccurred())
	})

	// newCR creates an IgnitionPayload whose generator-owned status is mid-rollout: current gen 2,
	// draining previous gen 1, IgnitionReached False, PayloadGenerated True.
	newCR := func() *hyperv1.IgnitionPayload {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "ignition-reached-"}}
		Expect(c.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			_ = c.Delete(cleanupCtx, ns)
		})

		cr := &hyperv1.IgnitionPayload{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns.Name, Name: "np-1"},
			Spec: hyperv1.IgnitionPayloadSpec{
				ReleaseImage: "quay.io/ocp@sha256:abc",
				PullSecret:   hyperv1.SecretReference{Name: "pull-secret"},
			},
		}
		Expect(c.Create(ctx, cr)).To(Succeed())

		Expect(statuspatching.PatchStatus(ctx, c, cr, func() error {
			cr.Status.Current = payloadRef(2, "a2", "b2", "np-1-2")
			cr.Status.Previous = payloadRef(1, "a1", "b1", "np-1-1")
			cr.Status.RolloutHashVersion = 1
			apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
				Type: hyperv1.IgnitionPayloadIgnitionReachedCondition, Status: metav1.ConditionFalse,
				Reason: "NewGeneration", Message: "a new payload generation has not yet been reached by a node",
			})
			apimeta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
				Type: hyperv1.IgnitionPayloadPayloadGeneratedCondition, Status: metav1.ConditionTrue,
				Reason: "AsExpected", Message: "payload generated for the current config",
			})
			return nil
		})).To(Succeed())
		return cr
	}

	It("When the generation advances between the gate read and the apply, it should not mark the new generation reached or overwrite generator-owned status", func() {
		cr := newCR()
		owner := payloadstore.OwnerRef{Namespace: cr.Namespace, Name: cr.Name}

		// The generator rolls out gen 3 in the window: gen 2 moves to previous, IgnitionReached resets.
		reader := &racingReader{Reader: c, race: func() {
			defer GinkgoRecover()
			rollout := &hyperv1.IgnitionPayload{ObjectMeta: metav1.ObjectMeta{Namespace: cr.Namespace, Name: cr.Name}}
			Expect(statuspatching.PatchStatus(ctx, c, rollout, func() error {
				rollout.Status.Previous = rollout.Status.Current
				rollout.Status.Current = payloadRef(3, "a3", "b3", "np-1-3")
				apimeta.SetStatusCondition(&rollout.Status.Conditions, metav1.Condition{
					Type: hyperv1.IgnitionPayloadIgnitionReachedCondition, Status: metav1.ConditionFalse,
					Reason: "NewGeneration", Message: "gen 3 has not yet been reached by a node",
				})
				apimeta.SetStatusCondition(&rollout.Status.Conditions, metav1.Condition{
					Type: hyperv1.IgnitionPayloadPayloadGeneratedCondition, Status: metav1.ConditionTrue,
					Reason: "AsExpected", Message: "gen 3 payload generated",
				})
				return nil
			})).To(Succeed())
		}}

		// A node finishes fetching gen 2 (read before the rollout) and records IgnitionReached.
		Expect(ignitionpayloadserver.SetIgnitionReached(ctx, c, reader, owner, 2)).To(Succeed())

		Expect(reader.readCount()).To(Equal(2), "the stale apply must conflict and re-read the CR")

		got := &hyperv1.IgnitionPayload{}
		Expect(c.Get(ctx, crclient.ObjectKeyFromObject(cr), got)).To(Succeed())
		Expect(got.Status.Current).To(Equal(payloadRef(3, "a3", "b3", "np-1-3")))
		Expect(got.Status.Previous).To(Equal(payloadRef(2, "a2", "b2", "np-1-2")))
		reached := apimeta.FindStatusCondition(got.Status.Conditions, hyperv1.IgnitionPayloadIgnitionReachedCondition)
		Expect(reached).NotTo(BeNil())
		Expect(reached.Status).To(Equal(metav1.ConditionFalse), "a gen-2 serve must not mark gen 3 reached")
		Expect(reached.Message).To(Equal("gen 3 has not yet been reached by a node"))
		generated := apimeta.FindStatusCondition(got.Status.Conditions, hyperv1.IgnitionPayloadPayloadGeneratedCondition)
		Expect(generated).NotTo(BeNil())
		Expect(generated.Status).To(Equal(metav1.ConditionTrue))
		Expect(generated.Message).To(Equal("gen 3 payload generated"), "PayloadGenerated must not be overwritten")
		Expect(ignitionReachedFields(got)).To(BeEmpty(), "the serving tier must not have applied anything")
	})

	It("When an unrelated same-generation status change lands between the gate read and the apply, it should retry and record IgnitionReached without clobbering it", func() {
		cr := newCR()
		owner := payloadstore.OwnerRef{Namespace: cr.Namespace, Name: cr.Name}

		// A management-side refresh (Policy A) updates only configHash on the same generation.
		reader := &racingReader{Reader: c, race: func() {
			defer GinkgoRecover()
			refresh := &hyperv1.IgnitionPayload{ObjectMeta: metav1.ObjectMeta{Namespace: cr.Namespace, Name: cr.Name}}
			Expect(statuspatching.PatchStatus(ctx, c, refresh, func() error {
				refresh.Status.Current.ConfigHash = "c2"
				return nil
			})).To(Succeed())
		}}

		Expect(ignitionpayloadserver.SetIgnitionReached(ctx, c, reader, owner, 2)).To(Succeed())

		Expect(reader.readCount()).To(Equal(2), "the apply must conflict once and be retried")

		got := &hyperv1.IgnitionPayload{}
		Expect(c.Get(ctx, crclient.ObjectKeyFromObject(cr), got)).To(Succeed())
		Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, hyperv1.IgnitionPayloadIgnitionReachedCondition)).To(BeTrue())
		Expect(got.Status.Current).To(Equal(payloadRef(2, "c2", "b2", "np-1-2")), "the concurrent refresh must survive")
		Expect(got.Status.Previous).To(Equal(payloadRef(1, "a1", "b1", "np-1-1")))
		Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, hyperv1.IgnitionPayloadPayloadGeneratedCondition)).To(BeTrue())

		// Field ownership: the serving tier owns only the IgnitionReached condition.
		owned := ignitionReachedFields(got)
		Expect(owned).To(ContainSubstring(`k:{\"type\":\"IgnitionReached\"}`))
		Expect(owned).NotTo(ContainSubstring(`PayloadGenerated`))
		Expect(strings.Contains(owned, `"f:current"`) || strings.Contains(owned, `"f:previous"`)).To(BeFalse())
	})
})
