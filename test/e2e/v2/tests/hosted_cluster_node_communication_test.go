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
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

func RegisterNodeCommunicationTests(getTestCtx internal.TestContextGetter) {
	EnsureNodeCommunicationTest(getTestCtx)
}

func EnsureNodeCommunicationTest(getTestCtx internal.TestContextGetter) {
	When("hosted cluster has konnectivity tunnel configured", func() {
		It("should have konnectivity-agent pods with retrievable logs", func() {
			tc := getTestCtx()
			hc, err := tc.GetHostedCluster()
			Expect(err).NotTo(HaveOccurred())
			restConfig, err := tc.GetHostedClusterRESTConfig(hc)
			Expect(err).NotTo(HaveOccurred())

			clientset, err := kubernetes.NewForConfig(restConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create hosted cluster kubernetes clientset")
			required, err := nodeCommunicationRequired(tc.Context, clientset, tc.ExpectedNodePools)
			Expect(err).NotTo(HaveOccurred(), "failed to determine whether the zero-worker fixture has acquired workers")
			if !required {
				Skip("node communication requires workers; this fixture explicitly configures zero workers and has no nodes")
			}

			Expect(waitForNodeCommunication(tc.Context, clientset)).To(Succeed(), "konnectivity-agent logs must be retrievable")
		})
	})
}

func nodeCommunicationRequired(ctx context.Context, clientset kubernetes.Interface, expectedNodePools map[string]int32) (bool, error) {
	if expectedNodePools == nil {
		return true, nil
	}
	for _, replicas := range expectedNodePools {
		if replicas > 0 {
			return true, nil
		}
	}
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("listing nodes for explicitly zero-worker fixture: %w", err)
	}
	return len(nodes.Items) > 0, nil
}

func waitForNodeCommunication(ctx context.Context, clientset kubernetes.Interface) error {
	var lastError error
	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 5*time.Minute, true, func(pollContext context.Context) (bool, error) {
		lastError = nodeCommunicationProbe(pollContext, clientset)
		return lastError == nil, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for konnectivity-agent logs: %w", errors.Join(err, lastError))
	}
	return nil
}

func nodeCommunicationProbe(ctx context.Context, clientset kubernetes.Interface) error {
	pods, err := clientset.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{LabelSelector: "app=konnectivity-agent"})
	if err != nil {
		return fmt.Errorf("listing konnectivity-agent pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no konnectivity-agent pods in kube-system")
	}
	var logErrors []error
	for _, pod := range pods.Items {
		_, err := clientset.CoreV1().Pods("kube-system").GetLogs(pod.Name, &corev1.PodLogOptions{Container: "konnectivity-agent"}).DoRaw(ctx)
		if err == nil {
			return nil
		}
		logErrors = append(logErrors, fmt.Errorf("pod %s: %w", pod.Name, err))
	}
	return fmt.Errorf("no konnectivity-agent logs retrievable: %w", errors.Join(logErrors...))
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodeCommunication] Hosted Cluster Node Communication", Label("hosted-cluster-node-communication"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterNodeCommunicationTests(func() *internal.TestContext { return testCtx })
})
