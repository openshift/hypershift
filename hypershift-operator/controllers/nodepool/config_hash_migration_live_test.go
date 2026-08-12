//go:build live

package nodepool

import (
	"context"
	"fmt"
	"os"
	"testing"

	docker10 "github.com/openshift/api/image/docker10"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	haproxy "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/apiserver-haproxy"
	"github.com/openshift/hypershift/support/api"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	fakeimagemetadataprovider "github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// TestLiveComputeLegacyHash prints legacy and content-based hashes for a live cluster.
// Run: KUBECONFIG=... go test -tags live ./hypershift-operator/controllers/nodepool/ -run TestLiveComputeLegacyHash -v
func TestLiveComputeLegacyHash(t *testing.T) {
	g := NewWithT(t)

	ns := envOr("LIVE_NAMESPACE", "clusters")
	hcName := envOr("LIVE_HC", "rfe8751-migrate")
	npName := envOr("LIVE_NP", "rfe8751-migrate-ap-south-1a")

	cfg, err := config.GetConfig()
	g.Expect(err).NotTo(HaveOccurred())

	c, err := client.New(cfg, client.Options{Scheme: api.Scheme})
	g.Expect(err).NotTo(HaveOccurred())

	ctx := context.Background()
	hc := &hyperv1.HostedCluster{}
	g.Expect(c.Get(ctx, types.NamespacedName{Namespace: ns, Name: hcName}, hc)).To(Succeed())
	np := &hyperv1.NodePool{}
	g.Expect(c.Get(ctx, types.NamespacedName{Namespace: ns, Name: npName}, np)).To(Succeed())

	version := np.Status.Version
	if version == "" {
		version = "4.21.0"
	}
	releaseImage, err := (&fakereleaseprovider.FakeReleaseProvider{Version: version}).Lookup(ctx, hc.Spec.Release.Image, nil)
	g.Expect(err).NotTo(HaveOccurred())

	osStreamsEnabled := false
	resolvedRHELStream, err := GetRHELStreamForBootImage(ctx, c, np, releaseImage, osStreamsEnabled)
	g.Expect(err).NotTo(HaveOccurred())

	hoImage := os.Getenv("HO_IMAGE")
	if hoImage == "" {
		deploy := &appsv1.Deployment{}
		g.Expect(c.Get(ctx, types.NamespacedName{Namespace: "hypershift", Name: "operator"}, deploy)).To(Succeed())
		hoImage = deploy.Spec.Template.Spec.Containers[0].Image
	}

	r := &NodePoolReconciler{
		Client:                  c,
		HypershiftOperatorImage: hoImage,
		ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{
			Version:    version,
			Components: map[string]string{"hypershift": hoImage},
		},
		ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
			Result: &dockerv1client.DockerImageConfig{
				Config: &docker10.DockerConfig{
					Labels: map[string]string{
						haproxy.ControlPlaneOperatorSkipsHAProxyConfigGenerationLabel: "true",
					},
				},
			},
		},
	}
	haproxyRawConfig, err := r.generateHAProxyRawConfig(ctx, np, hc, releaseImage)
	g.Expect(err).NotTo(HaveOccurred())

	cg, err := NewConfigGenerator(ctx, c, hc, np, releaseImage, haproxyRawConfig, manifests.HostedControlPlaneNamespace(ns, hcName), resolvedRHELStream)
	g.Expect(err).NotTo(HaveOccurred())

	atbName := additionalTrustBundleName(hc)
	legacyHWV := legacyHashWithoutVersion(cg.mcoRawConfig, cg.pullSecretName, atbName, cg.rhelStream)
	legacyH := legacyHash(cg.mcoRawConfig, cg.releaseImage.Version(), cg.pullSecretName, atbName, cg.globalConfig, cg.rhelStream)
	newHWV := cg.HashWithoutVersion()
	newH := cg.Hash()

	fmt.Printf("LIVE_LEGACY_HWV=%s\n", legacyHWV)
	fmt.Printf("LIVE_LEGACY_H=%s\n", legacyH)
	fmt.Printf("LIVE_NEW_HWV=%s\n", newHWV)
	fmt.Printf("LIVE_NEW_H=%s\n", newH)
	fmt.Printf("LIVE_CURRENT=%s\n", np.Annotations[nodePoolAnnotationCurrentConfig])
	fmt.Printf("LIVE_CURRENT_VERSION=%s\n", np.Annotations[nodePoolAnnotationCurrentConfigVersion])
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
