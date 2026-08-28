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

func TestReconcileConfigHashAnnotations(t *testing.T) {
	mcoRawConfig := "mco-config"
	pullSecretName := "pull-secret"
	atbName := "user-ca"
	atbContentHash := supportutil.HashSimple("bundle-content")
	proxyContentHash := supportutil.HashSimple("proxy-ca-content")
	globalConfig := "global-config"
	rhelStream := ""
	version := "4.18.0"

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

	v1CG := newCG(atbContentHash, "")
	legacyHWV := v1CG.hashWithoutVersionAtVersion(ConfigHashVersionV1)
	legacyH := v1CG.hashAtVersion(ConfigHashVersionV1)

	testCases := []struct {
		name                       string
		annotations                map[string]string
		cg                         *ConfigGenerator
		expectUpdated              bool
		expectVersionMigrated      bool
		expectConfigChanged        bool
		expectedCurrent            string
		expectedVersion            string
		expectedHashVersion        string
		expectAnnotationsUnchanged map[string]string
	}{
		{
			name: "When annotations match the v1 hash formula, it should migrate to the current version without a config change",
			annotations: map[string]string{
				nodePoolAnnotationCurrentConfig:        legacyHWV,
				nodePoolAnnotationCurrentConfigVersion: legacyH,
			},
			cg:                    v1CG,
			expectUpdated:         true,
			expectVersionMigrated: true,
			expectedCurrent:       supportutil.HashSimple(mcoRawConfig + pullSecretName + atbContentHash + "" + rhelStream),
			expectedVersion:       supportutil.HashSimple(mcoRawConfig + version + pullSecretName + atbContentHash + "" + globalConfig + rhelStream),
			expectedHashVersion:   CurrentConfigHashVersion,
		},
		{
			name: "When annotations already use the current hash version, it should not update again",
			annotations: map[string]string{
				nodePoolAnnotationCurrentConfig:        supportutil.HashSimple(mcoRawConfig + pullSecretName + atbContentHash + "" + rhelStream),
				nodePoolAnnotationCurrentConfigVersion: supportutil.HashSimple(mcoRawConfig + version + pullSecretName + atbContentHash + "" + globalConfig + rhelStream),
				nodePoolAnnotationConfigHashVersion:    CurrentConfigHashVersion,
			},
			cg:            newCG(atbContentHash, ""),
			expectUpdated: false,
		},
		{
			name: "When annotations reflect a different config change in progress, it should signal a real config change without rewriting annotations",
			annotations: map[string]string{
				nodePoolAnnotationCurrentConfig:        "other-config-hash",
				nodePoolAnnotationCurrentConfigVersion: "other-version-hash",
			},
			cg:                  newCG(atbContentHash, ""),
			expectUpdated:       false,
			expectConfigChanged: true,
			expectAnnotationsUnchanged: map[string]string{
				nodePoolAnnotationCurrentConfig:        "other-config-hash",
				nodePoolAnnotationCurrentConfigVersion: "other-version-hash",
			},
		},
		{
			name: "When no trust bundles are configured and hashes match across versions, it should only bump the hash version",
			annotations: map[string]string{
				nodePoolAnnotationCurrentConfig: func() string {
					cg := &ConfigGenerator{
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
					}
					return cg.hashWithoutVersionAtVersion(ConfigHashVersionV1)
				}(),
				nodePoolAnnotationCurrentConfigVersion: func() string {
					cg := &ConfigGenerator{
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
					}
					return cg.hashAtVersion(ConfigHashVersionV1)
				}(),
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
			expectUpdated:         true,
			expectVersionMigrated: true,
			expectedCurrent: func() string {
				cg := &ConfigGenerator{
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
				}
				return cg.HashWithoutVersion()
			}(),
			expectedVersion: func() string {
				cg := &ConfigGenerator{
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
				}
				return cg.Hash()
			}(),
			expectedHashVersion: CurrentConfigHashVersion,
		},
		{
			name: "When annotations match the v1 hash and only proxy.trustedCA content differs, it should migrate to the current version",
			annotations: map[string]string{
				nodePoolAnnotationCurrentConfig: func() string {
					cg := &ConfigGenerator{
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
					}
					return cg.hashWithoutVersionAtVersion(ConfigHashVersionV1)
				}(),
				nodePoolAnnotationCurrentConfigVersion: func() string {
					cg := &ConfigGenerator{
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
					}
					return cg.hashAtVersion(ConfigHashVersionV1)
				}(),
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
			expectUpdated:         true,
			expectVersionMigrated: true,
			expectedCurrent:       supportutil.HashSimple(mcoRawConfig + pullSecretName + "" + proxyContentHash + rhelStream),
			expectedVersion:       supportutil.HashSimple(mcoRawConfig + version + pullSecretName + "" + proxyContentHash + globalConfig + rhelStream),
			expectedHashVersion:   CurrentConfigHashVersion,
		},
		{
			name: "When both additionalTrustBundle and proxy.trustedCA are configured, it should migrate the combined content hash",
			annotations: map[string]string{
				nodePoolAnnotationCurrentConfig:        legacyHWV,
				nodePoolAnnotationCurrentConfigVersion: legacyH,
			},
			cg:                    newCG(atbContentHash, proxyContentHash),
			expectUpdated:         true,
			expectVersionMigrated: true,
			expectedCurrent:       supportutil.HashSimple(mcoRawConfig + pullSecretName + atbContentHash + proxyContentHash + rhelStream),
			expectedVersion:       supportutil.HashSimple(mcoRawConfig + version + pullSecretName + atbContentHash + proxyContentHash + globalConfig + rhelStream),
			expectedHashVersion:   CurrentConfigHashVersion,
		},
		{
			name:          "When annotations are missing, it should not update",
			annotations:   nil,
			cg:            newCG(atbContentHash, ""),
			expectUpdated: false,
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
				nodePool.Annotations = make(map[string]string, len(tc.annotations))
				for k, v := range tc.annotations {
					nodePool.Annotations[k] = v
				}
			}

			outcome := reconcileConfigHashAnnotations(nodePool, tc.cg)
			g.Expect(outcome.AnnotationsUpdated).To(Equal(tc.expectUpdated))
			g.Expect(outcome.VersionMigrated).To(Equal(tc.expectVersionMigrated))
			g.Expect(outcome.ConfigActuallyChanged).To(Equal(tc.expectConfigChanged))
			if tc.expectUpdated {
				g.Expect(nodePool.Annotations[nodePoolAnnotationCurrentConfig]).To(Equal(tc.expectedCurrent))
				g.Expect(nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(tc.expectedVersion))
				g.Expect(nodePool.Annotations[nodePoolAnnotationConfigHashVersion]).To(Equal(tc.expectedHashVersion))
				if tc.expectVersionMigrated {
					g.Expect(isUpdatingConfig(nodePool, tc.cg.HashWithoutVersion())).To(BeFalse())
				}
			}
			if tc.expectAnnotationsUnchanged != nil {
				g.Expect(nodePool.Annotations).To(Equal(tc.expectAnnotationsUnchanged))
			}
		})
	}
}

func TestShouldSkipUserDataSecretPropagation(t *testing.T) {
	testCases := []struct {
		name                   string
		outcome                configHashReconcileOutcome
		currentConfig          string
		targetConfigHash       string
		targetVersion          string
		currentTemplateVersion string
		expectSkip             bool
	}{
		{
			name:                   "When config hash version migrated this reconcile, it should skip user-data propagation",
			outcome:                configHashReconcileOutcome{VersionMigrated: true},
			currentConfig:          "cfg-old",
			targetConfigHash:       "cfg-new",
			targetVersion:          "4.18.0",
			currentTemplateVersion: "4.17.0",
			expectSkip:             true,
		},
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
			g.Expect(shouldSkipUserDataSecretPropagation(nodePool, tc.outcome, tc.targetConfigHash, tc.targetVersion, tc.currentTemplateVersion)).
				To(Equal(tc.expectSkip))
		})
	}
}
