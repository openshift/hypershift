//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func RegisterNodeCommunicationTests(getTestCtx internal.TestContextGetter) {
	EnsureNodeCommunicationTest(getTestCtx)
}

func EnsureNodeCommunicationTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster has konnectivity tunnel configured", func() {
		It("should have konnectivity-agent pods with retrievable logs", func() {
			tc := getTestCtx()
			tc.ValidateHostedClusterClient()
			restConfig := tc.GetHostedClusterRESTConfig()
			Expect(restConfig).NotTo(BeNil(), "hosted cluster REST config should be available")

			clientset, err := kubernetes.NewForConfig(restConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create hosted cluster kubernetes clientset")

			Eventually(func(g Gomega) {
				podList, err := clientset.CoreV1().Pods("kube-system").List(tc.Context, metav1.ListOptions{
					LabelSelector: "app=konnectivity-agent",
				})
				g.Expect(err).NotTo(HaveOccurred(), "failed to list konnectivity-agent pods")
				g.Expect(podList.Items).NotTo(BeEmpty(), "expected at least one konnectivity-agent pod in kube-system")

				_, err = clientset.CoreV1().Pods("kube-system").GetLogs(podList.Items[0].Name, &corev1.PodLogOptions{
					Container: "konnectivity-agent",
				}).DoRaw(tc.Context)
				g.Expect(err).NotTo(HaveOccurred(), "failed to retrieve logs from konnectivity-agent pod %s", podList.Items[0].Name)
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
	})
}

var _ = Describe("Hosted Cluster Node Communication", Label("hosted-cluster-node-communication"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterNodeCommunicationTests(func() *internal.TestContext { return testCtx })
})
