package hostedcluster

import (
	"context"
	"fmt"
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	haproxy "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/apiserver-haproxy"
	"github.com/openshift/hypershift/support/api"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	"github.com/openshift/api/image/docker10"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr/testr"
)

func TestReconcileKarpenterUserDataSecret(t *testing.T) {
	// we can't compute the configGenerator.Hash() before creating it, so replace it during the test
	const TMP_HASH = "tmp"
	const oldTokenHash = "old-token-hash"
	testCases := []struct {
		name                     string
		hostedClusterAnnotations map[string]string
		tokenOutdated            bool
	}{
		{
			name:                     "should set the annotation on the hosted cluster",
			hostedClusterAnnotations: map[string]string{},
			tokenOutdated:            false,
		},
		{
			name:                     "sets expiration timestamp on token secret if outdated",
			hostedClusterAnnotations: map[string]string{karpenterNodePoolAnnotationCurrentConfigVersion: oldTokenHash},
			tokenOutdated:            true,
		},
		{
			name:                     "skips updating the annotation if secret is not outdated",
			hostedClusterAnnotations: map[string]string{karpenterNodePoolAnnotationCurrentConfigVersion: TMP_HASH},
			tokenOutdated:            false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := log.IntoContext(t.Context(), testr.New(t))
			hClusterName := "hcluster"
			hClusterNamespace := "hcluster-ns"
			controlplaneNamespace := manifests.HostedControlPlaneNamespace(hClusterNamespace, hClusterName)

			pullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-secret",
					Namespace: hClusterNamespace,
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte("{}"),
				},
			}
			hostedCluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        hClusterName,
					Namespace:   hClusterNamespace,
					Annotations: tc.hostedClusterAnnotations,
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{
						Name: pullSecret.Name,
					},
					Release: hyperv1.Release{
						Image: "release-4.18",
					},
					AutoNode: &hyperv1.AutoNode{
						Provisioner: &hyperv1.ProvisionerConfig{
							Name: hyperv1.ProvisionerKarpenter,
							Karpenter: &hyperv1.KarpenterConfig{
								Platform: hyperv1.AWSPlatform,
							},
						},
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.Private,
							Region:         "us-east-1",
						},
					},
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{
								CIDR: *ipnet.MustParseCIDR("0.0.0.0/32"),
							},
						},
					},
				},
				Status: hyperv1.HostedClusterStatus{
					IgnitionEndpoint: "ignition-endpoint",
				},
			}

			coreConfig1 := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "core-config-1",
					Namespace: controlplaneNamespace,
					Labels: map[string]string{
						"hypershift.openshift.io/core-ignition-config": "true",
					},
				},
			}
			coreConfig2 := coreConfig1.DeepCopy()
			coreConfig2.Name = "core-config-2"

			ignitionServerCACert := ignitionserver.IgnitionCACertSecret(controlplaneNamespace)
			ignitionServerCACert.Type = corev1.SecretTypeTLS
			ignitionServerCACert.Data = map[string][]byte{
				corev1.TLSCertKey: []byte("test-ignition-ca-cert"),
			}

			imageMetadataProvider := &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
				Result: &dockerv1client.DockerImageConfig{
					Config: &docker10.DockerConfig{
						Labels: map[string]string{
							haproxy.ControlPlaneOperatorSkipsHAProxyConfigGenerationLabel: "true",
						},
					},
				},
			}
			haproxyImage := "test-image"
			releaseProvider := &fakereleaseprovider.FakeReleaseProvider{
				ImageVersion: map[string]string{
					"release-4.18": "4.18.0",
				},
				Version: "4.18.0",
				Components: map[string]string{
					haproxy.HAProxyRouterImageName: haproxyImage,
				},
			}

			releaseImage, err := releaseProvider.Lookup(ctx, "release-4.18", nil)
			g.Expect(err).ToNot(HaveOccurred())

			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:        hyperkarpenterv1.KarpenterNodePool,
					Namespace:   hClusterNamespace,
					Annotations: map[string]string{},
				},
				Spec: hyperv1.NodePoolSpec{
					Arch:        hyperv1.ArchitectureAMD64,
					ClusterName: hostedCluster.Name,
				},
			}

			oldTokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-%s-%s", "token", nodePool.GetName(), oldTokenHash),
					Namespace: controlplaneNamespace,
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(pullSecret, hostedCluster, coreConfig1, coreConfig2, ignitionServerCACert, oldTokenSecret).Build()
			r := &HostedClusterReconciler{
				Client:                  fakeClient,
				HypershiftOperatorImage: "test-image",
			}

			// create fake objects to emulate the ones created in reconcileKarpenterUserDataSecret to get the hash for comparison
			tmpHAProxy := haproxy.HAProxy{
				Client:                  r.Client,
				HAProxyImage:            haproxyImage,
				HypershiftOperatorImage: r.HypershiftOperatorImage,
				ReleaseProvider:         releaseProvider,
				ImageMetadataProvider:   imageMetadataProvider,
			}
			tmpHARawConfig, err := tmpHAProxy.GenerateHAProxyRawConfig(ctx, hostedCluster)
			g.Expect(err).ToNot(HaveOccurred())

			tmpConfigGenerator, err := nodepool.NewConfigGenerator(ctx, r.Client, hostedCluster, nodePool, releaseImage, tmpHARawConfig)
			g.Expect(err).ToNot(HaveOccurred())
			if tc.hostedClusterAnnotations[karpenterNodePoolAnnotationCurrentConfigVersion] == TMP_HASH {
				tc.hostedClusterAnnotations[karpenterNodePoolAnnotationCurrentConfigVersion] = tmpConfigGenerator.Hash()
			}

			err = r.reconcileKarpenterUserDataSecret(ctx, hostedCluster, releaseImage, nodePool, releaseProvider, imageMetadataProvider)
			g.Expect(err).ToNot(HaveOccurred())

			userData, err := getUserDataSecret(ctx, fakeClient, nodePool, controlplaneNamespace)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(userData.Data).NotTo(BeNil())
			g.Expect(userData.Labels).To(HaveKey(hyperkarpenterv1.UserDataAMILabel))

			token, err := getOldTokenSecret(ctx, fakeClient, nodePool, controlplaneNamespace, oldTokenHash)
			g.Expect(err).ToNot(HaveOccurred())
			if tc.tokenOutdated {
				g.Expect(token.Annotations).To(HaveKey(hyperv1.IgnitionServerTokenExpirationTimestampAnnotation))
			} else {
				g.Expect(token.Annotations).ToNot(HaveKey(hyperv1.IgnitionServerTokenExpirationTimestampAnnotation))
			}
			// make sure that karpenterNodePoolAnnotationCurrentConfigVersion is always set and equals the correct hash after reconcile
			g.Expect(hostedCluster.Annotations).To(HaveKey(karpenterNodePoolAnnotationCurrentConfigVersion))
			g.Expect(hostedCluster.Annotations[karpenterNodePoolAnnotationCurrentConfigVersion]).To(Equal(tmpConfigGenerator.Hash()))
		})
	}
}

func getUserDataSecret(ctx context.Context, client crclient.Client, nodePool *hyperv1.NodePool, hcpNamespace string) (*corev1.Secret, error) {
	labelSelector := labels.SelectorFromSet(labels.Set{hyperv1.NodePoolLabel: fmt.Sprintf("%s-%s", nodePool.Spec.ClusterName, nodePool.GetName())})
	listOptions := &crclient.ListOptions{
		LabelSelector: labelSelector,
		Namespace:     hcpNamespace,
	}
	secretList := &corev1.SecretList{}
	err := client.List(ctx, secretList, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	sort.Slice(secretList.Items, func(i, j int) bool {
		return secretList.Items[i].CreationTimestamp.After(secretList.Items[j].CreationTimestamp.Time)
	})
	if len(secretList.Items) < 1 {
		return nil, fmt.Errorf("expected at least 1 secret, got 0")
	}
	return &secretList.Items[0], err
}

func getOldTokenSecret(ctx context.Context, client crclient.Client, nodePool *hyperv1.NodePool, hcpNamespace, hash string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	err := client.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: fmt.Sprintf("%s-%s-%s", "token", nodePool.GetName(), hash)}, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get token secret: %w", err)
	}

	return secret, nil
}
