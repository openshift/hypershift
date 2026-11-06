package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	supportutil "github.com/openshift/hypershift/support/util"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHashAtVersion(t *testing.T) {
	t.Run("When references have identical content, it should retain the v2 hash", testHashAtVersionContentEquivalentReferences)
	mcoRawConfig := "mco-config"
	pullSecretName := "pull-secret"
	atbName := "user-ca"
	atbContentHash := supportutil.HashSimple("bundle-content")
	proxyContentHash := supportutil.HashSimple("proxy-ca-content")
	globalConfig := "global-config"
	rhelStream := ""
	version := "4.18.0"

	cg := &ConfigGenerator{
		hostedCluster: &hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				AdditionalTrustBundle: &corev1.LocalObjectReference{Name: atbName},
				PullSecret:            corev1.LocalObjectReference{Name: pullSecretName},
			},
		},
		rolloutConfig: &rolloutConfig{
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: version},
				},
			},
			pullSecretName:            pullSecretName,
			additionalTrustBundleHash: atbContentHash,
			proxyTrustedCAHash:        proxyContentHash,
			globalConfig:              globalConfig,
			mcoRawConfig:              mcoRawConfig,
			rhelStream:                rhelStream,
		},
	}

	g := NewWithT(t)
	g.Expect(cg.hashWithoutVersionAtVersion(ConfigHashVersionV1)).To(Equal(
		supportutil.HashSimple(mcoRawConfig + pullSecretName + atbName + rhelStream)))
	g.Expect(cg.hashAtVersion(ConfigHashVersionV1)).To(Equal(
		supportutil.HashSimple(mcoRawConfig + version + pullSecretName + atbName + globalConfig + rhelStream)))
	g.Expect(cg.hashWithoutVersionAtVersion(ConfigHashVersionV2)).To(Equal(cg.HashWithoutVersion()))
	g.Expect(cg.hashAtVersion(ConfigHashVersionV2)).To(Equal(cg.Hash()))
}
