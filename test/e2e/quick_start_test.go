// +build e2e

package e2e

import (
	"context"
	"io/ioutil"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	clientcmd "k8s.io/client-go/tools/clientcmd"

	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "openshift.io/hypershift/api/v1alpha1"
	"openshift.io/hypershift/test/e2e/internal/log"
)

var _ = Describe("When following the HyperShift quick-start [PR-Blocking]", func() {

	QuickStartSpec(context.TODO(), func() QuickStartSpecInput {
		return QuickStartSpecInput{
			Client:  client,
			DataDir: dataDir,
		}
	})

})

// QuickStartSpecInput is the input for QuickStartSpec.
type QuickStartSpecInput struct {
	Client  ctrl.Client
	DataDir string
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

		cluster *hyperv1.OpenShiftCluster
	)

	BeforeEach(func() {
		Expect(ctx).NotTo(BeNil(), "ctx is required for %s spec", specName)
		input = inputGetter()
		Expect(input.Client).ToNot(BeNil(), "Invalid argument. input.Client can't be nil when calling %s spec", specName)
		Expect(input.DataDir).ToNot(BeEmpty(), "Invalid argument. input.DataDir can't be empty when calling %s spec", specName)
	})

	It("Should create a functional guest cluster", func() {

		By("Applying the example cluster resources")

		// Load the example cluster resources
		exampleClusterPath := filepath.Join(input.DataDir, "example-cluster.yaml")
		resourceData, err := ioutil.ReadFile(exampleClusterPath)
		Expect(err).NotTo(HaveOccurred(), "couldn't read example cluster data from %s", exampleClusterPath)

		// Apply each resource into the hypershift namespace individually using
		// a server-side apply
		resources := strings.Split(string(resourceData), "---\n")
		for _, resource := range resources {
			obj := &unstructured.Unstructured{}
			Expect(yaml.NewYAMLOrJSONDecoder(strings.NewReader(resource), 100).Decode(obj)).To(Succeed(), "couldn't read resource")
			obj.SetNamespace("hypershift")
			err := input.Client.Patch(ctx, obj, ctrl.RawPatch(types.ApplyPatchType, []byte(resource)), ctrl.ForceOwnership, ctrl.FieldOwner("hypershift"))
			Expect(err).NotTo(HaveOccurred(), "couldn't apply resource")
		}

		// Get the actual OpenShiftCluster that was created
		log.Logf("Waiting for cluster resource to exist")
		cluster = &hyperv1.OpenShiftCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "hypershift",
				Name:      "example",
			},
		}
		Eventually(func() bool {
			key := ctrl.ObjectKey{
				Namespace: cluster.Namespace,
				Name:      cluster.Name,
			}
			if err := input.Client.Get(ctx, key, cluster); err != nil {
				log.Logf("error getting cluster: %s", err)
				return false
			}
			return true
		}, 30*time.Second, 1*time.Second).Should(BeTrue(), "couldn't find example cluster")

		// Perform some very basic assertions about the guest cluster

		By("Ensuring the guest cluster exposes a valid kubeconfig")

		log.Logf("Waiting for guest kubeconfig to become available")
		guestKubeConfigSecret := &corev1.Secret{}
		Eventually(func() bool {
			key := ctrl.ObjectKey{
				Namespace: cluster.GetNamespace(),
				Name:      cluster.Name + "-kubeconfig",
			}
			if err := input.Client.Get(ctx, key, guestKubeConfigSecret); err != nil {
				return false
			}
			return true
		}, 5*time.Minute, 1*time.Second).Should(BeTrue(), "couldn't find guest kubeconfig secret")

		guestKubeConfigSecretData, hasData := guestKubeConfigSecret.Data["value"]
		Expect(hasData).To(BeTrue(), "guest guest kubeconfig secret is missing value key")

		guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
		Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")

		By("Establishing a connection to the guest apiserver")
		var guestClient ctrl.Client
		Eventually(func() bool {
			kubeClient, err := ctrl.New(guestConfig, ctrl.Options{Scheme: scheme})
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
			log.Logf("found %d nodes", len(nodes.Items))
			return true
		}, 10*time.Minute, 1*time.Second).Should(BeTrue(), "guest nodes never became ready")
	})

	AfterEach(func() {
		if cluster != nil {
			By("Deleting the example cluster")

			Expect(input.Client.Delete(ctx, cluster, &ctrl.DeleteOptions{})).To(Succeed(), "couldn't clean up test cluster")

			By("Ensuring the example cluster resources are deleted")

			log.Logf("Waiting for guest cluster namespace to be deleted")
			Eventually(func() bool {
				namespace := &corev1.Namespace{}
				key := ctrl.ObjectKey{
					Name: cluster.Name,
				}
				if err := input.Client.Get(ctx, key, namespace); err != nil {
					if errors.IsNotFound(err) {
						return true
					}
					log.Logf("error getting namespace: %s", err)
					return false
				}
				return false
			}, 5*time.Minute, 1*time.Second).Should(BeTrue(), "couldn't clean up example cluster namespace")

			log.Logf("Waiting for the cluster resource to be deleted")
			Eventually(func() bool {
				key := ctrl.ObjectKey{
					Namespace: cluster.Namespace,
					Name:      cluster.Name,
				}
				if err := input.Client.Get(ctx, key, cluster); err != nil {
					if errors.IsNotFound(err) {
						return true
					}
					log.Logf("error getting cluster: %s", err)
					return false
				}
				return false
			}, 5*time.Minute, 1*time.Second).Should(BeTrue(), "couldn't clean up example cluster")
		}
	})
}
