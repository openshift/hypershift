//go:build e2ev2

package tests

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func RegisterHostedClusterCustomConfigTests(getTestCtx internal.TestContextGetter) {
	It("should configure the resolved etcd KMS key and propagate its encryption configuration", func() {
		testContext := getTestCtx()
		testContext.SkipIfNotPlatform(hyperv1.AWSPlatform)
		cluster, err := testContext.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		expectedKey, err := configuredHostedEtcdKMSKeyARN(testContext.Context, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(expectedKey).NotTo(BeEmpty(), "custom-config run must supply its etcd KMS ARN or alias")
		Expect(cluster.Spec.SecretEncryption).NotTo(BeNil())
		Expect(cluster.Spec.SecretEncryption.Type).To(Equal(hyperv1.KMS))
		Expect(cluster.Spec.SecretEncryption.KMS).NotTo(BeNil())
		Expect(cluster.Spec.SecretEncryption.KMS.Provider).To(Equal(hyperv1.AWS))
		Expect(cluster.Spec.SecretEncryption.KMS.AWS).NotTo(BeNil())
		Expect(cluster.Spec.SecretEncryption.KMS.AWS.ActiveKey.ARN).To(Equal(expectedKey))
		Expect(cluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN).NotTo(BeEmpty())
		controlPlane := &hyperv1.HostedControlPlane{}
		Expect(testContext.MgmtClient.Get(testContext.Context, crclient.ObjectKey{Namespace: testContext.ControlPlaneNamespace, Name: cluster.Name}, controlPlane)).To(Succeed())
		Expect(controlPlane.Spec.SecretEncryption).To(Equal(cluster.Spec.SecretEncryption))
	})
	It("should propagate the explicitly configured blocked image registry to the hosted cluster", func() {
		testContext := getTestCtx()
		testContext.SkipIfNotPlatform(hyperv1.AWSPlatform)
		cluster, err := testContext.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		Expect(cluster.Spec.Configuration).NotTo(BeNil(), "custom-config fixture must configure image registry sources before the suite starts")
		Expect(cluster.Spec.Configuration.Image).NotTo(BeNil())
		Expect(cluster.Spec.Configuration.Image.RegistrySources.BlockedRegistries).To(Equal([]string{"badregistry.io"}))
		client, err := testContext.GetHostedClusterClient(cluster)
		Expect(err).NotTo(HaveOccurred())
		configuration := &configv1.Image{}
		Expect(client.Get(testContext.Context, crclient.ObjectKey{Name: "cluster"}, configuration)).To(Succeed())
		Expect(configuration.Spec.RegistrySources.BlockedRegistries).To(Equal(cluster.Spec.Configuration.Image.RegistrySources.BlockedRegistries))
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:CustomConfiguration] Hosted Cluster Custom Configuration", Label("hosted-cluster-custom-config"), func() {
	RegisterHostedClusterCustomConfigTests(internal.GetTestContext)
})
