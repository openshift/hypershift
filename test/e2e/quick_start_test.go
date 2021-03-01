// +build e2e

package e2e

import (
	"context"
	"io/ioutil"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"

	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
	apifixtures "github.com/openshift/hypershift/api/fixtures"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/test/e2e/internal/log"
	"github.com/openshift/hypershift/version"
)

var _ = Describe("When following the HyperShift quick-start [PR-Blocking]", func() {

	QuickStartSpec(context.TODO(), func() QuickStartSpecInput {
		input := QuickStartSpecInput{
			Client: client,
		}
		var err error
		input.PullSecret, err = ioutil.ReadFile(quickStartSpecOptions.PullSecretFile)
		Expect(err).NotTo(HaveOccurred(), "couldn't read pull secret file %q", quickStartSpecOptions.PullSecretFile)

		input.AWSCredentials, err = ioutil.ReadFile(quickStartSpecOptions.AWSCredentialsFile)
		Expect(err).NotTo(HaveOccurred(), "couldn't read aws credentials file %q", quickStartSpecOptions.AWSCredentialsFile)

		input.SSHKey, err = ioutil.ReadFile(quickStartSpecOptions.SSHKeyFile)
		Expect(err).NotTo(HaveOccurred(), "couldn't read SSH key file %q", quickStartSpecOptions.SSHKeyFile)

		if len(quickStartSpecOptions.ReleaseImage) == 0 {
			defaultVersion, err := version.LookupDefaultOCPVersion()
			Expect(err).NotTo(HaveOccurred(), "couldn't look up default OCP version")
			input.ReleaseImage = defaultVersion.PullSpec
		}

		return input
	})

})

// QuickStartSpecInput is the input for QuickStartSpec.
type QuickStartSpecInput struct {
	Client         ctrl.Client
	ReleaseImage   string
	AWSCredentials []byte
	PullSecret     []byte
	SSHKey         []byte
}

// QuickStartSpec implements a spec that mimics the operation described in the
// HyperShift quick start (creating a basic guest cluster).
//
// This test is meant to provide a first, fast signal to detect regression; it
// is recommended to use it as a PR blocker test.
func QuickStartSpec(ctx context.Context, inputGetter func() QuickStartSpecInput) {
	var (
		specName = "quick-start"
		input    QuickStartSpecInput

		namespace *corev1.Namespace
	)

	BeforeEach(func() {
		Expect(ctx).NotTo(BeNil(), "ctx is required for %s spec", specName)
		input = inputGetter()
		Expect(input.Client).ToNot(BeNil(), "Invalid argument. input.Client can't be nil when calling %s spec", specName)
		Expect(input.ReleaseImage).ToNot(BeEmpty(), "Invalid argument. input.ReleaseImage can't be empty when calling %s spec", specName)
		Expect(input.AWSCredentials).ToNot(BeEmpty(), "Invalid argument. input.AWSCredentials can't be empty when calling %s spec", specName)
		Expect(input.PullSecret).ToNot(BeEmpty(), "Invalid argument. input.PullSecret can't be empty when calling %s spec", specName)
		Expect(input.SSHKey).ToNot(BeEmpty(), "Invalid argument. input.SSHKey can't be empty when calling %s spec", specName)
	})

	It("Should create a functional guest cluster", func() {

		By("Applying the example cluster resources")

		log.Logf("Testing OCP release image %s", input.ReleaseImage)

		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "e2e-",
			},
		}
		err := input.Client.Create(ctx, namespace)
		Expect(err).NotTo(HaveOccurred(), "couldn't create namespace")
		Expect(namespace.Name).NotTo(BeEmpty(), "generated namespace has no name")
		log.Logf("Created test namespace %s", namespace.Name)

		example := apifixtures.ExampleOptions{
			Namespace:        namespace.Name,
			Name:             "example",
			ReleaseImage:     input.ReleaseImage,
			PullSecret:       input.PullSecret,
			AWSCredentials:   input.AWSCredentials,
			SSHKey:           input.SSHKey,
			NodePoolReplicas: 2,
		}.Resources()

		err = input.Client.Create(ctx, example.PullSecret)
		Expect(err).NotTo(HaveOccurred(), "couldn't create pull secret")
		log.Logf("Created test pull secret %s", example.PullSecret.Name)

		err = input.Client.Create(ctx, example.AWSCredentials)
		Expect(err).NotTo(HaveOccurred(), "couldn't create aws credentials secret")
		log.Logf("Created test aws credentials secret %s", example.AWSCredentials.Name)

		err = input.Client.Create(ctx, example.SSHKey)
		Expect(err).NotTo(HaveOccurred(), "couldn't create ssh key secret")
		log.Logf("Created test ssh key secret %s", example.SSHKey.Name)

		err = input.Client.Create(ctx, example.Cluster)
		Expect(err).NotTo(HaveOccurred(), "couldn't create cluster")
		log.Logf("Created test hostedcluster %s", example.Cluster.Name)

		// Perform some very basic assertions about the guest cluster
		By("Ensuring the guest cluster exposes a valid kubeconfig")

		log.Logf("Waiting for guest kubeconfig to become available")
		var guestKubeConfigSecret corev1.Secret
		Eventually(func() bool {
			var currentCluster hyperv1.HostedCluster
			err := input.Client.Get(ctx, ctrl.ObjectKeyFromObject(example.Cluster), &currentCluster)
			if err != nil {
				log.Logf("error getting cluster: %w", err)
				return false
			}
			if currentCluster.Status.KubeConfig == nil {
				return false
			}
			key := ctrl.ObjectKey{
				Namespace: currentCluster.Namespace,
				Name:      currentCluster.Status.KubeConfig.Name,
			}
			if err := input.Client.Get(ctx, key, &guestKubeConfigSecret); err != nil {
				log.Logf("error getting guest kubeconfig secret %s: %w", key, err)
				return false
			}
			return true
		}, 5*time.Minute, 1*time.Second).Should(BeTrue(), "couldn't find guest kubeconfig secret")

		// TODO: this key should probably be published or an API constant
		guestKubeConfigSecretData, hasData := guestKubeConfigSecret.Data["kubeconfig"]
		Expect(hasData).To(BeTrue(), "guest kubeconfig secret is missing kubeconfig key")

		guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
		Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")

		By("Establishing a connection to the guest apiserver")
		var guestClient ctrl.Client
		Eventually(func() bool {
			kubeClient, err := ctrl.New(guestConfig, ctrl.Options{Scheme: hyperapi.Scheme})
			if err != nil {
				return false
			}
			guestClient = kubeClient
			return true
		}, 5*time.Minute, 5*time.Second).Should(BeTrue(), "couldn't create guest kube client")

		By("Ensuring guest nodes become ready")
		nodes := &corev1.NodeList{}
		Eventually(func() bool {
			err := guestClient.List(ctx, nodes)
			if err != nil {
				log.Logf("failed to list nodes: %w", err)
				return false
			}
			if len(nodes.Items) == 0 {
				return false
			}
			var readyNodes []string
			for _, node := range nodes.Items {
				for _, cond := range node.Status.Conditions {
					if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
						readyNodes = append(readyNodes, node.Name)
					}
				}
			}
			if len(readyNodes) != example.Cluster.Spec.InitialComputeReplicas {
				return false
			}
			log.Logf("found %d ready nodes", len(nodes.Items))
			return true
		}, 10*time.Minute, 1*time.Second).Should(BeTrue(), "guest nodes never became ready")
	})

	AfterEach(func() {
		if namespace == nil {
			return
		}
		By("Deleting the example cluster namespace")

		Expect(input.Client.Delete(ctx, namespace, &ctrl.DeleteOptions{})).To(Succeed(), "couldn't clean up test cluster")

		By("Ensuring the example cluster resources are deleted")

		log.Logf("Waiting for the test namespace %q to be deleted", namespace.Name)
		Eventually(func() bool {
			latestNamespace := &corev1.Namespace{}
			key := ctrl.ObjectKey{
				Name: namespace.Name,
			}
			if err := input.Client.Get(ctx, key, latestNamespace); err != nil {
				if errors.IsNotFound(err) {
					return true
				}
				log.Logf("error getting namespace %q: %s", latestNamespace.Name, err)
				return false
			}
			return false
		}, 10*time.Minute, 1*time.Second).Should(BeTrue(), "couldn't clean up example cluster namespace")
	})
}
