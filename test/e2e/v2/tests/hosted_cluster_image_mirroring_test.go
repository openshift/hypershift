//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// imageMirroringSetupOrSkip skips when the HC has no imageContentSources or the
// mirror fixture Route does not exist. Once both are present, the setup must be
// valid: malformed, duplicate, or non-fixture mappings fail the spec.
func imageMirroringSetupOrSkip(tc *internal.TestContext) (*hyperv1.HostedCluster, []hyperv1.ImageContentSource) {
	GinkgoHelper()
	Expect(tc).NotTo(BeNil(), "test context must be initialized")
	hc, err := tc.GetHostedCluster()
	Expect(err).NotTo(HaveOccurred(), "read HC %s/%s", tc.ClusterNamespace, tc.ClusterName)
	route := &routev1.Route{}
	err = tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: mirrorregistry.DefaultNamespace, Name: mirrorregistry.DefaultName}, route)
	if err != nil && !apierrors.IsNotFound(err) {
		Expect(err).NotTo(HaveOccurred(), "read mirror fixture Route %s/%s", mirrorregistry.DefaultNamespace, mirrorregistry.DefaultName)
	}
	if !imageMirroringConfigured(hc, err == nil) {
		Skip("image mirroring is not configured on this hosted cluster")
	}
	expected, err := imageMirroringExpected(hc, route)
	Expect(err).NotTo(HaveOccurred(), "HC %s/%s image-mirroring setup", hc.Namespace, hc.Name)
	return hc, expected
}

// RegisterHostedClusterImageMirroringTests registers independent, read-only
// lifecycle checks for the HCCO IDMS and CPO ignition propagation paths.
func RegisterHostedClusterImageMirroringTests(getTestCtx internal.TestContextGetter) {
	It("should propagate imageContentSources to the hosted cluster IDMS with the worker role", func() {
		tc := getTestCtx()
		hc, expected := imageMirroringSetupOrSkip(tc)

		hostedClient, err := tc.GetHostedClusterClientViaPortForward(hc)
		Expect(err).NotTo(HaveOccurred(), "open hosted client for HC %s/%s", hc.Namespace, hc.Name)
		DeferCleanup(hostedClient.Close)
		e2eutil.EventuallyObjects(GinkgoTB(), tc.Context,
			fmt.Sprintf("HC %s/%s hosted ImageDigestMirrorSet/cluster mappings and worker role", hc.Namespace, hc.Name),
			func(ctx context.Context) ([]*configv1.ImageDigestMirrorSet, error) {
				return imageMirroringGetIDMS(ctx, hostedClient)
			},
			[]e2eutil.Predicate[[]*configv1.ImageDigestMirrorSet]{func(objects []*configv1.ImageDigestMirrorSet) (bool, string, error) {
				if len(objects) != 1 || objects[0] == nil || objects[0].Name != "cluster" || objects[0].Namespace != "" {
					return false, "expected exactly one nonnil cluster-scoped ImageDigestMirrorSet/cluster", nil
				}
				return true, "", nil
			}}, []e2eutil.Predicate[*configv1.ImageDigestMirrorSet]{func(object *configv1.ImageDigestMirrorSet) (bool, string, error) {
				return imageMirroringCheckIDMS([]*configv1.ImageDigestMirrorSet{object}, expected)
			}},
			e2eutil.WithTimeout(10*time.Minute), e2eutil.WithInterval(3*time.Second), e2eutil.WithoutConditionDump())
	})

	It("should propagate imageContentSources independently to the control plane ignition ConfigMap", func() {
		tc := getTestCtx()
		hc, expected := imageMirroringSetupOrSkip(tc)
		Expect(tc.ControlPlaneNamespace).NotTo(BeEmpty(), "HC %s/%s must have a control plane namespace", hc.Namespace, hc.Name)

		e2eutil.EventuallyObjects(GinkgoTB(), tc.Context,
			fmt.Sprintf("HC %s/%s ConfigMap %s/%s decoded IDMS mappings and labels", hc.Namespace, hc.Name, tc.ControlPlaneNamespace, imageMirroringIgnitionName),
			func(ctx context.Context) ([]*corev1.ConfigMap, error) {
				return imageMirroringGetConfigMap(ctx, tc.MgmtClient, tc.ControlPlaneNamespace)
			},
			[]e2eutil.Predicate[[]*corev1.ConfigMap]{func(objects []*corev1.ConfigMap) (bool, string, error) {
				if len(objects) != 1 || objects[0] == nil || objects[0].Name != imageMirroringIgnitionName || objects[0].Namespace != tc.ControlPlaneNamespace {
					return false, fmt.Sprintf("expected exactly one nonnil ConfigMap %s/%s", tc.ControlPlaneNamespace, imageMirroringIgnitionName), nil
				}
				return true, "", nil
			}}, []e2eutil.Predicate[*corev1.ConfigMap]{func(object *corev1.ConfigMap) (bool, string, error) {
				return imageMirroringCheckConfigMap([]*corev1.ConfigMap{object}, tc.ControlPlaneNamespace, expected)
			}},
			e2eutil.WithTimeout(10*time.Minute), e2eutil.WithInterval(3*time.Second), e2eutil.WithoutConditionDump())
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ImageMirroring] Hosted Cluster Image Mirroring", Label("lifecycle", "image-mirroring"), func() {
	RegisterHostedClusterImageMirroringTests(internal.GetTestContext)
})
