package hostedcluster

import (
	"context"
	"fmt"
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
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
)

func TestReconcileKarpenterUserDataSecret(t *testing.T) {
	g := NewWithT(t)

	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret"},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: nil,
		},
	}
	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
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

	controlplaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

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
	ignitionServerCACert.Data = map[string][]byte{
		corev1.TLSCertKey: []byte("test-ignition-ca-cert"),
	}

	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(pullSecret, hostedCluster, coreConfig1, coreConfig2, ignitionServerCACert).Build()
	r := &HostedClusterReconciler{
		Client:                  fakeClient,
		HypershiftOperatorImage: "test-image",
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
	releaseProvider := &fakereleaseprovider.FakeReleaseProvider{
		ImageVersion: map[string]string{
			"release-4.18": "4.18.0",
		},
		Version: "4.18.0",
		Components: map[string]string{
			haproxy.HAProxyRouterImageName: "test-image",
		},
	}

	releaseImage, err := releaseProvider.Lookup(t.Context(), "release-4.18", nil)
	g.Expect(err).ToNot(HaveOccurred())

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "karpenter",
		},
		Spec: hyperv1.NodePoolSpec{
			Arch: hyperv1.ArchitectureAMD64,
		},
	}

	err = r.reconcileKarpenterUserDataSecret(t.Context(), hostedCluster, releaseImage, nodePool, releaseProvider, imageMetadataProvider)
	g.Expect(err).ToNot(HaveOccurred())

	userData, err := getUserDataSecret(t.Context(), fakeClient, nodePool, controlplaneNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(userData.Data).NotTo(BeNil())
	g.Expect(userData.Labels).To(HaveKey("hypershift.openshift.io/ami"))

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
		return nil, fmt.Errorf("expected 1 secret, got 0")
	}
	return &secretList.Items[0], err
}
