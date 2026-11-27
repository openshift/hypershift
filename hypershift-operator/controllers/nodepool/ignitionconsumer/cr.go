package ignitionconsumer

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/manifests"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// nodePoolAnnotation is the back-reference annotation (namespace/name) from an IgnitionPayload CR
// to its NodePool. It matches the NodePool controller's nodePoolAnnotation.
const nodePoolAnnotation = "hypershift.openshift.io/nodePool"

// consumerFinalizer guards the NodePool controller's IgnitionPayload CR so the controller can tear
// down its projected ConfigMaps before the CR is removed. The generator holds a separate
// store-cleanup finalizer; the CR and its owned objects are reclaimed once both clear.
const consumerFinalizer = "hypershift.openshift.io/ignition-payload-consumer"

// ignitionPayloadSpec assembles the consumer inputs into an IgnitionPayloadSpec. The consumer only
// declares inputs and classifies config sources — it never opens a ConfigMap's contents and
// computes no hash. additionalRolloutInputs carries the API-server endpoint identity (the
// endpointConfig term that fed today's NodePool RolloutHash), so an endpoint change rolls running
// nodes even though the HAProxy config stays in managementConfigMaps (where an image-only bump does
// not roll). retiredRolloutGeneration is owned by AdvanceRetiredRolloutGeneration and preserved
// across spec reconciles.
func ignitionPayloadSpec(nodePool *hyperv1.NodePool, hc *hyperv1.HostedCluster,
	rolloutRefs, mgmtRefs []hyperv1.ConfigMapReference, rolloutGlobalConfigName, osStream, additionalRolloutInputs string) hyperv1.IgnitionPayloadSpec {
	spec := hyperv1.IgnitionPayloadSpec{
		ReleaseImage: nodePool.Spec.Release.Image,
		// The CR lives in the HCP namespace, and the generator resolves its references there. The
		// HostedCluster names its pull secret and trust bundle in its own namespace; the control plane
		// copies them into the HCP namespace under fixed names, so reference those copies. pullSecret is
		// immutable, so it must be right on first write.
		PullSecret:              hyperv1.SecretReference{Name: manifests.PullSecret("").Name},
		RolloutGlobalConfig:     hyperv1.ConfigMapReference{Name: rolloutGlobalConfigName},
		AdditionalRolloutInputs: additionalRolloutInputs,
		RolloutConfigMaps:       rolloutRefs,
		ManagementConfigMaps:    mgmtRefs,
	}
	// osImageStream is optional and CEL-validated to an enum, so set it only when the consumer
	// supplies a value; an empty value lets the generator default it by release version.
	if osStream != "" {
		spec.OSImageStream = hyperv1.OSImageStreamReference{Name: osStream}
	}
	if hc.Spec.AdditionalTrustBundle != nil {
		spec.AdditionalTrustBundle = hyperv1.ConfigMapReference{Name: controlplaneoperator.UserCABundle("").Name}
	}
	return spec
}

// reconcileIgnitionPayloadCR creates or updates the per-NodePool IgnitionPayload CR in
// hcpNamespace. The CR is named after the NodePool, carries the back-reference annotation (ns/name)
// used by the reverse watch and the NodePool label, and holds the consumer finalizer.
// retiredRolloutGeneration is preserved across updates (owned by AdvanceRetiredRolloutGeneration).
func reconcileIgnitionPayloadCR(ctx context.Context, c client.Client, hcpNamespace string,
	nodePool *hyperv1.NodePool, spec hyperv1.IgnitionPayloadSpec) (*hyperv1.IgnitionPayload, error) {
	cr := &hyperv1.IgnitionPayload{
		ObjectMeta: metav1.ObjectMeta{Name: nodePool.GetName(), Namespace: hcpNamespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, c, cr, func() error {
		if cr.Annotations == nil {
			cr.Annotations = map[string]string{}
		}
		cr.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
		if cr.Labels == nil {
			cr.Labels = map[string]string{}
		}
		cr.Labels[hyperv1.NodePoolLabel] = nodePool.GetName()

		if !sets.New(cr.Finalizers...).Has(consumerFinalizer) {
			cr.Finalizers = append(cr.Finalizers, consumerFinalizer)
		}

		// retiredRolloutGeneration is owned by AdvanceRetiredRolloutGeneration; carry the existing
		// value so a spec reconcile never resets it.
		retired := cr.Spec.RetiredRolloutGeneration
		cr.Spec = spec
		cr.Spec.RetiredRolloutGeneration = retired
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile IgnitionPayload %s/%s: %w", hcpNamespace, nodePool.GetName(), err)
	}
	return cr, nil
}
