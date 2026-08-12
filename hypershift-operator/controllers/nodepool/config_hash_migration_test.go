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

func TestMaybeSeedTrustBundleContentHashBaseline(t *testing.T) {
	mcoRawConfig := "mco-config"
	pullSecretName := "pull-secret"
	atbName := "user-ca"
	atbContentHash := supportutil.HashSimple("bundle-content")
	proxyContentHash := supportutil.HashSimple("proxy-ca-content")
	globalConfig := "global-config"
	rhelStream := ""
	version := "4.18.0"

	legacyHWV := legacyHashWithoutVersion(mcoRawConfig, pullSecretName, atbName, rhelStream)
	legacyH := legacyHash(mcoRawConfig, version, pullSecretName, atbName, globalConfig, rhelStream)

	newCG := func(atbHash, proxyHash string) *ConfigGenerator {
		return &ConfigGenerator{
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
				additionalTrustBundleHash: atbHash,
				proxyTrustedCAHash:        proxyHash,
				globalConfig:              globalConfig,
				mcoRawConfig:              mcoRawConfig,
				rhelStream:                rhelStream,
			},
		}
	}

	testCases := []struct {
		name              string
		annotations       map[string]string
		cg                *ConfigGenerator
		expectSeeded      bool
		expectedCurrent   string
		expectedVersion   string
		expectAnnotations map[string]string
	}{
		{
			name: "When annotations match the legacy name-based hash, it should seed the content-based baseline",
			annotations: map[string]string{
				nodePoolAnnotationCurrentConfig:        legacyHWV,
				nodePoolAnnotationCurrentConfigVersion: legacyH,
			},
			cg:              newCG(atbContentHash, ""),
			expectSeeded:    true,
			expectedCurrent: supportutil.HashSimple(mcoRawConfig + pullSecretName + atbContentHash + "" + rhelStream),
			expectedVersion: supportutil.HashSimple(mcoRawConfig + version + pullSecretName + atbContentHash + "" + globalConfig + rhelStream),
		},
		{
			name: "When annotations already use the content-based hash, it should not seed again",
			annotations: map[string]string{
				nodePoolAnnotationCurrentConfig:        supportutil.HashSimple(mcoRawConfig + pullSecretName + atbContentHash + "" + rhelStream),
				nodePoolAnnotationCurrentConfigVersion: supportutil.HashSimple(mcoRawConfig + version + pullSecretName + atbContentHash + "" + globalConfig + rhelStream),
			},
			cg:           newCG(atbContentHash, ""),
			expectSeeded: false,
		},
		{
			name: "When annotations reflect a different config change in progress, it should not seed",
			annotations: map[string]string{
				nodePoolAnnotationCurrentConfig:        "other-config-hash",
				nodePoolAnnotationCurrentConfigVersion: "other-version-hash",
			},
			cg:           newCG(atbContentHash, ""),
			expectSeeded: false,
			expectAnnotations: map[string]string{
				nodePoolAnnotationCurrentConfig:        "other-config-hash",
				nodePoolAnnotationCurrentConfigVersion: "other-version-hash",
			},
		},
		{
			name: "When no trust bundles are configured and hashes match, it should not seed",
			annotations: map[string]string{
				nodePoolAnnotationCurrentConfig:        legacyHashWithoutVersion(mcoRawConfig, pullSecretName, "", rhelStream),
				nodePoolAnnotationCurrentConfigVersion: legacyHash(mcoRawConfig, version, pullSecretName, "", globalConfig, rhelStream),
			},
			cg: &ConfigGenerator{
				hostedCluster: &hyperv1.HostedCluster{
					Spec: hyperv1.HostedClusterSpec{
						PullSecret: corev1.LocalObjectReference{Name: pullSecretName},
					},
				},
				rolloutConfig: &rolloutConfig{
					releaseImage: &releaseinfo.ReleaseImage{
						ImageStream: &imageapi.ImageStream{
							ObjectMeta: metav1.ObjectMeta{Name: version},
						},
					},
					pullSecretName: pullSecretName,
					globalConfig:   globalConfig,
					mcoRawConfig:   mcoRawConfig,
					rhelStream:     rhelStream,
				},
			},
			expectSeeded: false,
		},
		{
			name: "When annotations match the legacy hash and only proxy.trustedCA content differs, it should seed",
			annotations: map[string]string{
				nodePoolAnnotationCurrentConfig:        legacyHashWithoutVersion(mcoRawConfig, pullSecretName, "", rhelStream),
				nodePoolAnnotationCurrentConfigVersion: legacyHash(mcoRawConfig, version, pullSecretName, "", globalConfig, rhelStream),
			},
			cg: &ConfigGenerator{
				hostedCluster: &hyperv1.HostedCluster{
					Spec: hyperv1.HostedClusterSpec{
						PullSecret: corev1.LocalObjectReference{Name: pullSecretName},
					},
				},
				rolloutConfig: &rolloutConfig{
					releaseImage: &releaseinfo.ReleaseImage{
						ImageStream: &imageapi.ImageStream{
							ObjectMeta: metav1.ObjectMeta{Name: version},
						},
					},
					pullSecretName:     pullSecretName,
					proxyTrustedCAHash: proxyContentHash,
					globalConfig:       globalConfig,
					mcoRawConfig:       mcoRawConfig,
					rhelStream:         rhelStream,
				},
			},
			expectSeeded:    true,
			expectedCurrent: supportutil.HashSimple(mcoRawConfig + pullSecretName + "" + proxyContentHash + rhelStream),
			expectedVersion: supportutil.HashSimple(mcoRawConfig + version + pullSecretName + "" + proxyContentHash + globalConfig + rhelStream),
		},
		{
			name: "When both additionalTrustBundle and proxy.trustedCA are configured, it should seed the combined content hash",
			annotations: map[string]string{
				nodePoolAnnotationCurrentConfig:        legacyHWV,
				nodePoolAnnotationCurrentConfigVersion: legacyH,
			},
			cg:              newCG(atbContentHash, proxyContentHash),
			expectSeeded:    true,
			expectedCurrent: supportutil.HashSimple(mcoRawConfig + pullSecretName + atbContentHash + proxyContentHash + rhelStream),
			expectedVersion: supportutil.HashSimple(mcoRawConfig + version + pullSecretName + atbContentHash + proxyContentHash + globalConfig + rhelStream),
		},
		{
			name:         "When annotations are missing, it should not seed",
			annotations:  nil,
			cg:           newCG(atbContentHash, ""),
			expectSeeded: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "workers",
					Namespace:   "clusters",
					Annotations: tc.annotations,
				},
			}
			if tc.annotations != nil {
				// Copy so mutations do not affect the table entry unexpectedly across runs.
				nodePool.Annotations = make(map[string]string, len(tc.annotations))
				for k, v := range tc.annotations {
					nodePool.Annotations[k] = v
				}
			}

			seeded := maybeSeedTrustBundleContentHashBaseline(nodePool, tc.cg)
			g.Expect(seeded).To(Equal(tc.expectSeeded))
			if tc.expectSeeded {
				g.Expect(nodePool.Annotations[nodePoolAnnotationCurrentConfig]).To(Equal(tc.expectedCurrent))
				g.Expect(nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(tc.expectedVersion))
				g.Expect(isUpdatingConfig(nodePool, tc.cg.HashWithoutVersion())).To(BeFalse())
			}
			if tc.expectAnnotations != nil {
				g.Expect(nodePool.Annotations).To(Equal(tc.expectAnnotations))
			}
		})
	}
}

func TestShouldSkipUserDataSecretPropagation(t *testing.T) {
	testCases := []struct {
		name                   string
		currentConfig          string
		targetConfigHash       string
		targetVersion          string
		currentTemplateVersion string
		expectSkip             bool
	}{
		{
			name:                   "When config and version already match the baseline, it should skip user-data propagation",
			currentConfig:          "cfg-a",
			targetConfigHash:       "cfg-a",
			targetVersion:          "4.18.0",
			currentTemplateVersion: "4.18.0",
			expectSkip:             true,
		},
		{
			name:                   "When config hash differs, it should not skip user-data propagation",
			currentConfig:          "cfg-old",
			targetConfigHash:       "cfg-new",
			targetVersion:          "4.18.0",
			currentTemplateVersion: "4.18.0",
			expectSkip:             false,
		},
		{
			name:                   "When version differs, it should not skip user-data propagation",
			currentConfig:          "cfg-a",
			targetConfigHash:       "cfg-a",
			targetVersion:          "4.19.0",
			currentTemplateVersion: "4.18.0",
			expectSkip:             false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						nodePoolAnnotationCurrentConfig: tc.currentConfig,
					},
				},
			}
			g.Expect(shouldSkipUserDataSecretPropagation(nodePool, tc.targetConfigHash, tc.targetVersion, tc.currentTemplateVersion)).
				To(Equal(tc.expectSkip))
		})
	}
}
