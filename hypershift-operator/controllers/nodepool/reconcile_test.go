package nodepool

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/api"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	fakeimagemetadataprovider "github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	docker10 "github.com/openshift/api/image/docker10"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcile(t *testing.T) {
	t.Run("When AWS supplies native capacity and the legacy provider is absent, it should persist scheduling metadata through the full reconcile path", func(t *testing.T) {
		g := NewWithT(t)
		const releaseImage = "quay.io/openshift-release-dev/ocp-release:4.22.15-x86_64"
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "zone-check", Namespace: "clusters"},
			Spec: hyperv1.HostedClusterSpec{
				InfraID: "zone-check-infra", Release: hyperv1.Release{Image: releaseImage},
				PullSecret:   corev1.LocalObjectReference{Name: "pull-secret"},
				Platform:     hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{Region: "eu-central-1"}},
				Capabilities: &hyperv1.Capabilities{Disabled: []hyperv1.OptionalCapability{hyperv1.NodeTuningCapability}},
			},
			Status: hyperv1.HostedClusterStatus{
				PayloadArch: hyperv1.AMD64, IgnitionEndpoint: "ignition.zone-check.example:443",
				KubeConfig: &corev1.LocalObjectReference{Name: "guest-kubeconfig"},
			},
		}
		np := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: hc.Namespace},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: hc.Name, Release: hyperv1.Release{Image: releaseImage}, Arch: "amd64",
				AutoScaling: &hyperv1.NodePoolAutoScaling{Min: ptr.To[int32](0), Max: 1},
				Platform: hyperv1.NodePoolPlatform{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSNodePoolPlatform{
					InstanceType: "m5.large", AMI: "ami-0a1b2c3d4e5f67890", Subnet: hyperv1.AWSResourceReference{ID: ptr.To("subnet-0a1b2c3d4e5f67890")},
				}},
				Management: hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace, Replace: &hyperv1.ReplaceUpgrade{
					Strategy: hyperv1.UpgradeStrategyOnDelete,
				}},
				NodeLabels: map[string]string{"workload": "envoy"},
				Taints:     []hyperv1.Taint{{Key: "dedicated", Value: "envoy", Effect: corev1.TaintEffectNoSchedule}},
			},
		}
		ca := ignitionserver.IgnitionCACertSecret("clusters-zone-check")
		ca.Data = map[string][]byte{corev1.TLSCertKey: []byte("unit-test-ca")}
		pullSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: hc.Spec.PullSecret.Name, Namespace: hc.Namespace}, Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
		}}
		objects := []client.Object{hc, np, ca, pullSecret}
		for _, name := range []string{"fips", "ssh", "apiserver-haproxy"} {
			objects = append(objects, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ca.Namespace, Labels: map[string]string{nodePoolCoreIgnitionConfigLabel: "true"}},
				Data:       map[string]string{TokenSecretConfigKey: fmt.Sprintf("apiVersion: machineconfiguration.openshift.io/v1\nkind: MachineConfig\nmetadata:\n  name: %s\nspec:\n  config:\n    ignition:\n      version: 3.2.0\n", name)},
			})
		}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).WithStatusSubresource(np, &capiaws.AWSMachineTemplate{}).Build()
		r := &NodePoolReconciler{
			Client: c, recorder: record.NewFakeRecorder(10),
			ReleaseProvider:         &fakereleaseprovider.FakeReleaseProvider{Version: "4.22.15"},
			HypershiftOperatorImage: "quay.io/openshift/hypershift-operator:latest",
			ImageMetadataProvider:   &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{}}},
		}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(np)}
		_, err := r.Reconcile(t.Context(), request)
		g.Expect(err).ToNot(HaveOccurred())
		md := &capiv1.MachineDeployment{}
		g.Expect(c.Get(t.Context(), client.ObjectKey{Name: np.Name, Namespace: ca.Namespace}, md)).To(Succeed(), "the full controller must reach CAPI reconciliation")
		template := &capiaws.AWSMachineTemplate{}
		g.Expect(c.Get(t.Context(), client.ObjectKey{Name: md.Spec.Template.Spec.InfrastructureRef.Name, Namespace: md.Namespace}, template)).To(Succeed())
		template.Status.Capacity = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("8Gi")}
		g.Expect(c.Status().Update(t.Context(), template)).To(Succeed())
		// Remove any annotations from the first reconcile so this assertion cannot
		// pass merely by retaining metadata written before native capacity existed.
		delete(md.Annotations, labelsKey)
		delete(md.Annotations, taintsKey)
		g.Expect(c.Update(t.Context(), md)).To(Succeed())
		_, err = r.Reconcile(t.Context(), request)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(md), md)).To(Succeed())
		g.Expect(md.Annotations).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=amd64,node.kubernetes.io/instance-type=m5.large,workload=envoy"))
		g.Expect(md.Annotations).To(HaveKeyWithValue(taintsKey, "dedicated=envoy:NoSchedule"))
	})
}
