package util

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	routev1 "github.com/openshift/api/route/v1"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	promconfig "github.com/prometheus/common/config"
	prommodel "github.com/prometheus/common/model"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
)

// DeleteNamespace deletes and finalizes the given namespace, logging any failures
// along the way.
func DeleteNamespace(t *testing.T, ctx context.Context, client crclient.Client, namespace string) error {
	t.Logf("Deleting namespace: %s", namespace)
	err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		err := client.Delete(ctx, ns, &crclient.DeleteOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			t.Logf("Failed to delete namespace: %s, will retry: %v", namespace, err)
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	if err != nil {
		return fmt.Errorf("failed to delete namespace: %w", err)
	}

	t.Logf("Waiting for namespace to be finalized. Namespace: %s", namespace)
	err = wait.PollImmediateUntil(5*time.Second, func() (done bool, err error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(ns), ns); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			t.Logf("Failed to get namespace: %s. %v", namespace, err)
			return false, nil
		}
		return false, nil
	}, ctx.Done())
	if err != nil {
		return fmt.Errorf("namespace still exists after deletion timeout: %v", err)
	}
	t.Logf("Deleted namespace: %s", namespace)
	return nil
}

func WaitForGuestKubeConfig(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) ([]byte, error) {
	start := time.Now()
	t.Logf("Waiting for hostedcluster kubeconfig to be published. Namespace: %s, name: %s", hostedCluster.Namespace, hostedCluster.Name)
	var guestKubeConfigSecret corev1.Secret
	err := wait.PollUntil(1*time.Second, func() (done bool, err error) {
		err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		if err != nil {
			return false, nil
		}
		if hostedCluster.Status.KubeConfig == nil {
			return false, nil
		}
		key := crclient.ObjectKey{
			Namespace: hostedCluster.Namespace,
			Name:      hostedCluster.Status.KubeConfig.Name,
		}
		if err := client.Get(ctx, key, &guestKubeConfigSecret); err != nil {
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	if err != nil {
		return nil, fmt.Errorf("kubeconfig didn't become available: %w", err)
	}
	t.Logf("Found kubeconfig for cluster in %s. Namespace: %s, name: %s", time.Since(start).Round(time.Second), hostedCluster.Namespace, hostedCluster.Name)

	// TODO: this key should probably be published or an API constant
	data, hasData := guestKubeConfigSecret.Data["kubeconfig"]
	if !hasData {
		return nil, fmt.Errorf("kubeconfig secret is missing kubeconfig key")
	}
	return data, nil
}

func WaitForGuestClient(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) crclient.Client {
	g := NewWithT(t)
	start := time.Now()

	guestKubeConfigSecretData, err := WaitForGuestKubeConfig(t, ctx, client, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "couldn't get kubeconfig")

	guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
	g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")

	t.Logf("Waiting for a successful connection to the guest apiserver")
	var guestClient crclient.Client
	waitForGuestClientCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	err = wait.PollUntil(5*time.Second, func() (done bool, err error) {
		kubeClient, err := crclient.New(guestConfig, crclient.Options{Scheme: scheme})
		if err != nil {
			return false, nil
		}
		guestClient = kubeClient
		return true, nil
	}, waitForGuestClientCtx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed to establish a connection to the guest apiserver")

	t.Logf("Successfully connected to the guest apiserver in %s", time.Since(start).Round(time.Second))
	return guestClient
}

func WaitForNReadyNodes(t *testing.T, ctx context.Context, client crclient.Client, n int32) []corev1.Node {
	g := NewWithT(t)

	t.Logf("Waiting for nodes to become ready. Want: %v", n)
	nodes := &corev1.NodeList{}
	readyNodeCount := 0
	err := wait.PollUntil(5*time.Second, func() (done bool, err error) {
		// TODO (alberto): have ability to filter nodes by NodePool. NodePool.Status.Nodes?
		err = client.List(ctx, nodes)
		if err != nil {
			return false, nil
		}
		if len(nodes.Items) == 0 {
			return false, nil
		}
		var readyNodes []string
		for _, node := range nodes.Items {
			for _, cond := range node.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					readyNodes = append(readyNodes, node.Name)
				}
			}
		}
		if len(readyNodes) != int(n) {
			readyNodeCount = len(readyNodes)
			return false, nil
		}
		t.Logf("All nodes are ready. Count: %v", len(nodes.Items))
		return true, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed to ensure guest nodes became ready, ready: (%d/%d): ", readyNodeCount, n))

	t.Logf("All nodes for nodepool appear to be ready. Count: %v", n)
	return nodes.Items
}

func WaitForImageRollout(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, image string) {
	g := NewWithT(t)

	t.Logf("Waiting for hostedcluster to rollout image. Namespace: %s, name: %s, image: %s", hostedCluster.Namespace, hostedCluster.Name, image)
	err := wait.PollUntil(10*time.Second, func() (done bool, err error) {
		latest := hostedCluster.DeepCopy()
		err = client.Get(ctx, crclient.ObjectKeyFromObject(latest), latest)
		if err != nil {
			t.Errorf("Failed to get hostedcluster: %v", err)
			return false, nil
		}

		isAvailable := meta.IsStatusConditionTrue(latest.Status.Conditions, string(hyperv1.HostedClusterAvailable))

		rolloutComplete := latest.Status.Version != nil &&
			latest.Status.Version.Desired.Image == image &&
			len(latest.Status.Version.History) > 0 &&
			latest.Status.Version.History[0].Image == latest.Status.Version.Desired.Image &&
			latest.Status.Version.History[0].State == configv1.CompletedUpdate

		if isAvailable && rolloutComplete {
			t.Logf("Waiting for hostedcluster rollout. Image: %s, isAvailable: %v, rolloutComplete: %v", image, isAvailable, rolloutComplete)
			return true, nil
		}
		return false, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed waiting for image rollout")

	t.Logf("Observed hostedcluster to have successfully rolled out image. Namespace: %s, name: %s, image: %s", hostedCluster.Namespace, hostedCluster.Name, image)
}

func WaitForConditionsOnHostedControlPlane(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, image string) {
	g := NewWithT(t)

	t.Logf("Waiting for hostedcluster to rollout image. Namespace: %s, name: %s, image: %s", hostedCluster.Namespace, hostedCluster.Name, image)
	err := wait.PollUntil(10*time.Second, func() (done bool, err error) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name
		cp := &hyperv1.HostedControlPlane{}
		err = client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: hostedCluster.Name}, cp)
		if err != nil {
			t.Errorf("Failed to get hostedcontrolplane: %v", err)
			return false, nil
		}

		conditions := map[hyperv1.ConditionType]bool{
			hyperv1.HostedControlPlaneAvailable:          false,
			hyperv1.EtcdAvailable:                        false,
			hyperv1.KubeAPIServerAvailable:               false,
			hyperv1.InfrastructureReady:                  false,
			hyperv1.ValidHostedControlPlaneConfiguration: false,
		}

		isAvailable := true
		for condition := range conditions {
			conditionReady := meta.IsStatusConditionTrue(cp.Status.Conditions, string(condition))
			conditions[condition] = conditionReady
			if !conditionReady {
				isAvailable = false
			}
		}

		if isAvailable {
			t.Logf("Waiting for all conditions to be ready: Image: %s, conditions: %v", image, conditions)
			return true, nil
		}
		return false, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed waiting for image rollout")

	t.Logf("Observed hostedcluster to have successfully rolled out image. Namespace: %s, name: %s, image: %s", hostedCluster.Namespace, hostedCluster.Name, image)
}

func EnsureNoCrashingPods(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureNoCrashingPods", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

		var podList corev1.PodList
		if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
		}
		for _, pod := range podList.Items {
			// TODO: This is needed because of an upstream NPD, see e.G. here: https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/origin-ci-test/pr-logs/pull/openshift_hypershift/486/pull-ci-openshift-hypershift-main-e2e-aws-pooled/1445408206435127296/artifacts/e2e-aws-pooled/test-e2e/artifacts/namespaces/e2e-clusters-slgzn-example-f748r/core/pods/logs/capa-controller-manager-f66fd8977-knt6h-manager-previous.log
			// remove this exception once upstream is fixed and we have the fix
			if strings.HasPrefix(pod.Name, "capa-controller-manager") {
				continue
			}

			// TODO: Autoscaler is restarting because it times out accessing the kube apiserver for leader election.
			// Investigate a fix.
			if strings.HasPrefix(pod.Name, "cluster-autoscaler") {
				continue
			}

			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.RestartCount > 0 {
					t.Errorf("Container %s in pod %s has a restartCount > 0 (%d)", containerStatus.Name, pod.Name, containerStatus.RestartCount)
				}
			}
		}
	})
}

func EnsureNodeCountMatchesNodePoolReplicas(t *testing.T, ctx context.Context, hostClient, guestClient crclient.Client, nodePoolNamespace string) {
	t.Run("EnsureNodeCountMatchesNodePoolReplicas", func(t *testing.T) {
		var nodePoolList hyperv1.NodePoolList
		if err := hostClient.List(ctx, &nodePoolList, &crclient.ListOptions{Namespace: nodePoolNamespace}); err != nil {
			t.Fatalf("failed to list nodepools: %v", err)
		}
		nodeCount := 0
		for _, nodePool := range nodePoolList.Items {
			nodeCount = nodeCount + int(*nodePool.Spec.NodeCount)
		}

		var nodes corev1.NodeList
		if err := guestClient.List(ctx, &nodes); err != nil {
			t.Fatalf("failed to list nodes in guest cluster: %v", err)
		}

		if nodeCount != len(nodes.Items) {
			t.Errorf("nodepool replicas %d does not match number of nodes in cluster %d", nodeCount, len(nodes.Items))
		}
	})
}

func EnsureAPIBudget(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureAPIBudget", func(t *testing.T) {

		// Get hypershift-operator token
		operatorServiceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "operator",
				Namespace: "hypershift",
			},
		}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(operatorServiceAccount), operatorServiceAccount); err != nil {
			t.Fatalf("failed to get hypershift operator service account: %v", err)
		}
		var secretName string
		for _, secret := range operatorServiceAccount.Secrets {
			if strings.HasPrefix(secret.Name, "operator-token-") {
				secretName = secret.Name
				break
			}
		}
		if secretName == "" {
			t.Fatal("failed to determine hypershift operator token secret name")
		}
		tokenSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: "hypershift",
			},
		}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(tokenSecret), tokenSecret); err != nil {
			t.Fatalf("failed to get hypershift operator token secret: %v", err)
		}
		token, ok := tokenSecret.Data["token"]
		if !ok {
			t.Fatal("token secret did not contain a token value")
		}

		// Get thanos-querier endpoint
		promRoute := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "thanos-querier",
				Namespace: "openshift-monitoring",
			},
		}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(promRoute), promRoute); err != nil {
			t.Skip("unable to get prometheus route, skipping")
		}
		if len(promRoute.Status.Ingress) == 0 {
			t.Skip("unable to get prometheus ingress, skipping")
		}
		promEndpoint := fmt.Sprintf("https://%s", promRoute.Status.Ingress[0].Host)

		// Create prometheus client
		cfg := promconfig.HTTPClientConfig{
			Authorization: &promconfig.Authorization{
				Type:        "Bearer",
				Credentials: promconfig.Secret(token),
			},
			TLSConfig: promconfig.TLSConfig{
				InsecureSkipVerify: true,
			},
		}
		rt, err := promconfig.NewRoundTripperFromConfig(cfg, "e2e-budget-checker")
		if err != nil {
			t.Fatalf("failed to get create round tripper: %v", err)
		}
		client, err := promapi.NewClient(promapi.Config{
			Address:      promEndpoint,
			RoundTripper: rt,
		})
		if err != nil {
			t.Fatalf("failed to get create prometheus client: %v", err)
		}
		v1api := promv1.NewAPI(client)

		// Compare metrics against budgets
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name
		clusterAgeMinutes := int32(time.Since(hostedCluster.CreationTimestamp.Time).Round(time.Minute).Minutes())
		budgets := []struct {
			name   string
			query  string
			budget float64
		}{
			{
				name:   "control-plane-operator read",
				query:  fmt.Sprintf(`sum by (pod) (max_over_time(hypershift:controlplane:component_api_requests_total{app="control-plane-operator", method="GET", namespace=~"%s"}[%dm]))`, namespace, clusterAgeMinutes),
				budget: 800,
			},
			{
				name:   "control-plane-operator mutate",
				query:  fmt.Sprintf(`sum by (pod) (max_over_time(hypershift:controlplane:component_api_requests_total{app="control-plane-operator", method!="GET", namespace=~"%s"}[%dm]))`, namespace, clusterAgeMinutes),
				budget: 400,
			},
			{
				name:   "control-plane-operator no 404 deletes",
				query:  fmt.Sprintf(`sum by (pod) (max_over_time(hypershift:controlplane:component_api_requests_total{app="control-plane-operator", method="DELETE", code="404", namespace=~"%s"}[%dm]))`, namespace, clusterAgeMinutes),
				budget: 5,
			},
			// hypershift-operator budget can not be per HC so metric will be
			// significantly under budget for all but the last test(s) to complete on
			// a particular test cluster These budgets will also need to scale up with
			// additional tests that create HostedClusters
			{
				name:   "hypershift-operator read",
				query:  `sum(hypershift:operator:component_api_requests_total{method="GET"})`,
				budget: 1500,
			},
			{
				name:   "hypershift-operator mutate",
				query:  `sum(hypershift:operator:component_api_requests_total{method!="GET"})`,
				budget: 2000,
			},
			{
				name:   "hypershift-operator no 404 deletes",
				query:  `sum(hypershift:operator:component_api_requests_total{method="DELETE", code="404"})`,
				budget: 5,
			},
		}

		for _, budget := range budgets {
			t.Run(budget.name, func(t *testing.T) {
				result, _, err := v1api.Query(ctx, budget.query, time.Now())
				if err != nil {
					t.Fatalf("failed to query prometheus: %v", err)
				}
				vector, ok := result.(prommodel.Vector)
				if !ok {
					t.Fatal("expected vector result")
				}
				if len(vector) == 0 {
					if budget.budget < 10 {
						t.Log("no samples returned for query with small budget, skipping check")
					} else {
						t.Errorf("no samples returned for query with large budget, failed check")
					}
				}
				for _, sample := range vector {
					podMsg := ""
					if podName, ok := sample.Metric["pod"]; ok {
						podMsg = fmt.Sprintf("pod %s ", podName)
					}
					if float64(sample.Value) > budget.budget {
						t.Errorf("%sover budget: budget: %.0f, actual: %.0f", podMsg, budget.budget, sample.Value)
					} else {
						t.Logf("%swithin budget: budget: %.0f, actual: %.0f", podMsg, budget.budget, sample.Value)
					}
				}
			})
		}
	})
}

func EnsureHCPContainersHaveResourceRequests(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureHCPContainersHaveResourceRequests", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name
		var podList corev1.PodList
		if err := client.List(ctx, &podList, &crclient.ListOptions{Namespace: namespace}); err != nil {
			t.Fatalf("failed to list pods: %v", err)
		}
		for _, pod := range podList.Items {
			if strings.Contains(pod.Name, "-catalog-rollout-") {
				continue
			}
			for _, container := range pod.Spec.Containers {
				if container.Resources.Requests == nil {
					t.Errorf("container %s in pod %s has no resource requests", container.Name, pod.Name)
					continue
				}
				if _, ok := container.Resources.Requests[corev1.ResourceCPU]; !ok {
					t.Errorf("container %s in pod %s has no CPU resource request", container.Name, pod.Name)
				}
				if _, ok := container.Resources.Requests[corev1.ResourceMemory]; !ok {
					t.Errorf("container %s in pod %s has no memory resource request", container.Name, pod.Name)
				}
			}
		}
	})
}
