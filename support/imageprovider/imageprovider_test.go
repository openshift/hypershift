package imageprovider

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/releaseinfo"

	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func fakeReleaseImage(images map[string]string) *releaseinfo.ReleaseImage {
	tags := make([]imagev1.TagReference, 0, len(images))
	for name, uri := range images {
		tags = append(tags, imagev1.TagReference{
			Name: name,
			From: &corev1.ObjectReference{Name: uri},
		})
	}
	return &releaseinfo.ReleaseImage{
		ImageStream: &imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "test-release"},
			Spec:       imagev1.ImageStreamSpec{Tags: tags},
		},
	}
}

func TestNew(t *testing.T) {
	g := NewGomegaWithT(t)
	ri := fakeReleaseImage(map[string]string{
		"kube-rbac-proxy": "quay.io/openshift-release-dev/ocp-v5.0-art-dev@sha256:abc123",
	})

	p := New(ri)
	g.Expect(p.GetImage("kube-rbac-proxy")).To(Equal("quay.io/openshift-release-dev/ocp-v5.0-art-dev@sha256:abc123"))
}

func TestNewWithRegistryOverrides(t *testing.T) {
	ecr := "235494791733.dkr.ecr.us-east-1.amazonaws.com/openshift-release-dev/ocp-v5.0-art-dev"
	quay := "quay.io/openshift-release-dev/ocp-v5.0-art-dev"
	overrides := map[string]string{quay: ecr}

	testCases := []struct {
		name      string
		overrides map[string]string
		wantProxy string
		wantOVN   string
	}{
		{
			name:      "nil overrides behave identically to New()",
			overrides: nil,
			wantProxy: quay + "@sha256:abc123",
			wantOVN:   quay + "@sha256:def456",
		},
		{
			name:      "empty map behaves identically to New()",
			overrides: map[string]string{},
			wantProxy: quay + "@sha256:abc123",
			wantOVN:   quay + "@sha256:def456",
		},
		{
			name:      "matching override substitutes registry host on all images",
			overrides: overrides,
			wantProxy: ecr + "@sha256:abc123",
			wantOVN:   ecr + "@sha256:def456",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ri := fakeReleaseImage(map[string]string{
				"kube-rbac-proxy": quay + "@sha256:abc123",
				"ovn-kubernetes":  quay + "@sha256:def456",
			})

			p := NewWithRegistryOverrides(ri, tc.overrides)
			g.Expect(p.GetImage("kube-rbac-proxy")).To(Equal(tc.wantProxy))
			g.Expect(p.GetImage("ovn-kubernetes")).To(Equal(tc.wantOVN))

			// original ReleaseImage must never be mutated
			g.Expect(ri.ComponentImages()["kube-rbac-proxy"]).To(Equal(quay + "@sha256:abc123"))
		})
	}
}
