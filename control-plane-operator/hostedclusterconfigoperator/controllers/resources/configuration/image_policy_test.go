package configuration

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/support/globalconfig"

	configv1 "github.com/openshift/api/config/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestReconcileImagePolicy(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                  string
		hcp                   *hyperv1.HostedControlPlane
		removeICSAndReconcile bool
	}{
		{
			name: "When ICS has content, it should return an IDMS with the same content",
			hcp:  withICS(configurationHCP()),
		},
		{
			name: "When ICS is empty, it should return an empty IDMS",
			hcp:  configurationHCP(),
		},
		{
			name:                  "When ICS is removed after reconcile, it should sync IDMS to match",
			hcp:                   withICS(configurationHCP()),
			removeICSAndReconcile: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.hcp).Build()
			r := &configurationTestClients{
				client:                 fakeClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}
			err := ReconcileImagePolicy(t.Context(), r.hosted(), tc.hcp.Spec.ImageContentSources)
			g.Expect(err).ToNot(HaveOccurred())

			idms := globalconfig.ImageDigestMirrorSet()
			err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(idms), idms)
			g.Expect(err).ToNot(HaveOccurred(), "error getting IDMS")

			g.Expect(len(tc.hcp.Spec.ImageContentSources)).To(Equal(len(idms.Spec.ImageDigestMirrors)), "expecting equal values between IDMS and ICS")

			if tc.hcp.Spec.ImageContentSources != nil {
				compareICSAndIDMS(g, tc.hcp.Spec.ImageContentSources, idms)
			}

			if tc.removeICSAndReconcile {
				origHCP := tc.hcp.DeepCopy()
				origHCP.Spec.ImageContentSources = nil

				err = ReconcileImagePolicy(t.Context(), r.hosted(), origHCP.Spec.ImageContentSources)
				g.Expect(err).ToNot(HaveOccurred())
				idms := globalconfig.ImageDigestMirrorSet()
				err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(idms), idms)
				g.Expect(err).ToNot(HaveOccurred(), "error getting IDMS")
				g.Expect(len(origHCP.Spec.ImageContentSources)).To(Equal(len(idms.Spec.ImageDigestMirrors)), "expecting equal values between IDMS and ICS")
				compareICSAndIDMS(g, origHCP.Spec.ImageContentSources, idms)
			}
		})
	}

	t.Run("When ICSP and stale IDMS exist, it should delete ICSP before replacing mirrors and remain idempotent", func(t *testing.T) {
		assert := NewWithT(t)
		hcp := populatedConfigurationHCP(t)
		idms := globalconfig.ImageDigestMirrorSet()
		idms.Spec.ImageDigestMirrors = []configv1.ImageDigestMirrors{{Source: "stale.example.com", Mirrors: []configv1.ImageMirror{"stale-mirror.example.com"}}}
		var operations []string
		guest := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(globalconfig.ImageContentSourcePolicy(), idms).WithInterceptorFuncs(interceptor.Funcs{Delete: func(ctx context.Context, target client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
			operations = append(operations, fmt.Sprintf("delete %T %s/%s", object, object.GetNamespace(), object.GetName()))
			return target.Delete(ctx, object, opts...)
		}}).Build()
		root := &configurationTestClients{client: guest, CreateOrUpdateProvider: configurationUpserter{func(ctx context.Context, target client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
			operations = append(operations, fmt.Sprintf("upsert %T %s/%s", object, object.GetNamespace(), object.GetName()))
			assert.Expect(apierrors.IsNotFound(guest.Get(ctx, client.ObjectKeyFromObject(globalconfig.ImageContentSourcePolicy()), globalconfig.ImageContentSourcePolicy()))).To(BeTrue())
			return controllerutil.CreateOrUpdate(ctx, target, object, mutate)
		}}}
		assert.Expect(ReconcileImagePolicy(t.Context(), root.hosted(), hcp.Spec.ImageContentSources)).To(Succeed())
		assert.Expect(operations).To(Equal([]string{fmt.Sprintf("delete %T /%s", globalconfig.ImageContentSourcePolicy(), globalconfig.ImageContentSourcePolicy().Name), fmt.Sprintf("upsert %T /%s", idms, idms.Name)}))
		assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(idms), idms)).To(Succeed())
		assert.Expect(idms.Spec.ImageDigestMirrors).To(Equal([]configv1.ImageDigestMirrors{{Source: "quay.io/openshift-release-dev/ocp-release", Mirrors: []configv1.ImageMirror{"mirror.example.com/release"}}}))
		assert.Expect(idms.Labels["machineconfiguration.openshift.io/role"]).To(Equal("worker"))
		before := idms.DeepCopy()
		operations = nil
		assert.Expect(ReconcileImagePolicy(t.Context(), root.hosted(), hcp.Spec.ImageContentSources)).To(Succeed())
		assert.Expect(operations).To(Equal([]string{fmt.Sprintf("upsert %T /%s", idms, idms.Name)}))
		assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(idms), idms)).To(Succeed())
		assert.Expect(idms).To(Equal(before))
	})
}

func compareICSAndIDMS(g *WithT, ics []hyperv1.ImageContentSource, idms *configv1.ImageDigestMirrorSet) {
	g.Expect(len(ics)).To(Equal(len(idms.Spec.ImageDigestMirrors)), "expecting equal values between IDMS and ICS")
	for i, ics := range ics {
		g.Expect(ics.Source).To(Equal(idms.Spec.ImageDigestMirrors[i].Source))
		for j, mirrorics := range ics.Mirrors {
			g.Expect(mirrorics).To(Equal(string(idms.Spec.ImageDigestMirrors[i].Mirrors[j])))
		}
	}
}

func withICS(hcp *hyperv1.HostedControlPlane) *hyperv1.HostedControlPlane {
	hcpOriginal := hcp.DeepCopy()
	hcpOriginal.Spec.ImageContentSources = []hyperv1.ImageContentSource{
		{
			Source: "example.com/test",
			Mirrors: []string{
				"mirror1.example.com/test1",
				"mirror2.example.com/test2",
			},
		},
		{
			Source: "sample.com/test",
			Mirrors: []string{
				"mirror1.sample.com/test1",
				"mirror2.sample.com/test2",
			},
		},
		{
			Source: "quay.io/test",
			Mirrors: []string{
				"mirror1.quay.io/test1",
				"mirror2.quay.io/test2",
			},
		},
	}

	return hcpOriginal
}
