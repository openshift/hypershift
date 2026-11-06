package karpenterignition

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	fakeimagemetadataprovider "github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	"github.com/openshift/api/image/docker10"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type recordingCPOImageProvider struct {
	fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider
	image string
}

func (p *recordingCPOImageProvider) ImageMetadata(ctx context.Context, image string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	p.image = image
	return p.FakeRegistryClientImageMetadataProvider.ImageMetadata(ctx, image, pullSecret)
}

func TestDetectCPOCapabilities(t *testing.T) {
	const runningImage = "quay.io/rhn-support-alosingh/hypershift-operator:rfe8751-review-fixes-20261013"
	for _, tc := range []struct {
		name                                                                     string
		missingImage, missingSecret, metadataError, unsupported, staleAnnotation bool
	}{
		{name: "When the HCP lacks the HC override, it should inspect the running CPO image"},
		{name: "When an inherited annotation is stale, it should prefer the running CPO image", staleAnnotation: true},
		{name: "When the running image lacks validation, it should leave the capability disabled", unsupported: true},
		{name: "When the running image is empty, it should fail rather than resolve the release CPO", missingImage: true},
		{name: "When the pull secret is missing, it should return an error", missingSecret: true},
		{name: "When image metadata lookup fails, it should return an error", metadataError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters-test"},
				Spec: hyperv1.HostedControlPlaneSpec{
					ReleaseImage: "quay.io/openshift-release-dev/ocp-release:5.0.0-rc.6-x86_64",
					PullSecret:   corev1.LocalObjectReference{Name: "pull-secret"},
				},
			}
			if tc.staleAnnotation {
				hcp.Annotations = map[string]string{hyperv1.ControlPlaneOperatorImageAnnotation: "quay.io/hypershift/control-plane-operator:old"}
			}
			hc, err := hostedClusterFromHCP(hcp, "ignition.test")
			if err != nil {
				t.Fatal(err)
			}
			originalHC, originalHCP := hc.DeepCopy(), hcp.DeepCopy()
			var objects []client.Object
			if !tc.missingSecret {
				objects = append(objects, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: hcp.Namespace}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)}})
			}
			labels := map[string]string{}
			if !tc.unsupported {
				labels["io.openshift.hypershift.control-plane-operator-manages.rendered-trust-bundle-validation"] = "true"
			}
			provider := &recordingCPOImageProvider{FakeRegistryClientImageMetadataProvider: fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{Labels: labels}}}}
			if tc.metadataError {
				provider.Err = fmt.Errorf("metadata unavailable")
			}
			r := &KarpenterIgnitionReconciler{ManagementClient: fake.NewClientBuilder().WithObjects(objects...).Build(), ImageMetadataProvider: provider, ControlPlaneOperatorImage: runningImage}
			if tc.missingImage {
				r.ControlPlaneOperatorImage = ""
			}
			// No release provider: resolving the release instead of the running image must fail this test.
			caps, err := r.detectCPOCapabilities(t.Context(), hc)
			wantError := tc.missingImage || tc.missingSecret || tc.metadataError
			if (err != nil) != wantError {
				t.Fatalf("error=%v, wantError=%v", err, wantError)
			}
			if !reflect.DeepEqual(hc, originalHC) || !reflect.DeepEqual(hcp, originalHCP) {
				t.Fatal("capability detection mutated the source HC or HCP")
			}
			if !tc.missingImage && !tc.missingSecret && provider.image != runningImage {
				t.Fatalf("inspected %q, want %q", provider.image, runningImage)
			}
			if !wantError && caps.RenderedTrustBundleValidation == tc.unsupported {
				t.Fatal("incorrect validation capability")
			}
		})
	}
}
