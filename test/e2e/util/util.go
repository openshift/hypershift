package util

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/test/e2e/util/cluster"
	awscluster "github.com/openshift/hypershift/test/e2e/util/cluster/aws"
	nonecluster "github.com/openshift/hypershift/test/e2e/util/cluster/none"
	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
)

// CreateAWSCluster creates a new namespace and a HostedCluster in that namespace using
// the provided options.
//
// This function calls t.Cleanup() with a function which tears down the cluster
// and *blocks until teardown completes*, so no explicit cleanup from the caller
// is required.
func CreateAWSCluster(t *testing.T, ctx context.Context, client crclient.Client, opts core.CreateOptions, artifactDir string) *hyperv1.HostedCluster {
	return createCluster(t, ctx, client, awscluster.New(t, opts), artifactDir)
}

func CreateNoneCluster(t *testing.T, ctx context.Context, client crclient.Client, opts core.CreateOptions, artifactDir string) *hyperv1.HostedCluster {
	return createCluster(t, ctx, client, nonecluster.New(t, opts), artifactDir)
}

func createCluster(t *testing.T, ctx context.Context, client crclient.Client, clusterMgr cluster.Cluster, artifactDir string) *hyperv1.HostedCluster {
	g := NewWithT(t)
	start := time.Now()

	// Set up a namespace to contain the hostedcluster
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: SimpleNameGenerator.GenerateName("e2e-clusters-"),
			Labels: map[string]string{
				"hypershift-e2e-component": "hostedclusters-namespace",
			},
		},
	}
	err := client.Create(ctx, namespace)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create namespace")

	// Define the hostedcluster and adjust options to match it
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      SimpleNameGenerator.GenerateName("example-"),
		},
	}

	// Define a standard teardown function
	teardown := func(ctx context.Context) {
		if len(artifactDir) != 0 {
			clusterMgr.DumpCluster(ctx, hc, artifactDir)
		} else {
			t.Logf("Skipping cluster dump because no artifact dir was provided")
		}
		// TODO: Figure out why this is slow
		//e2eutil.DumpGuestCluster(context.Background(), client, hostedCluster, globalOpts.ArtifactDir)
		DestroyHostedCluster(t, ctx, hc, clusterMgr, artifactDir)
		DeleteNamespace(t, ctx, client, namespace.Name)
	}

	// Try and create the cluster
	t.Logf("Creating a new cluster. Options: %v", clusterMgr.Describe())
	if err := clusterMgr.CreateCluster(ctx, hc); err != nil {
		teardown(context.Background())
		g.Expect(err).NotTo(HaveOccurred(), "failed to create cluster")
	}

	// Assert we can retrieve the cluster that was created
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(hc), hc); err != nil {
		teardown(context.Background())
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")
	}

	t.Logf("Successfully created hostedcluster %s/%s in %s", hc.Namespace, hc.Name, time.Since(start).Round(time.Second))
	t.Cleanup(func() { teardown(context.Background()) })

	return hc
}

// DestroyHostedCluster destroys the HostedCluster
func DestroyHostedCluster(t *testing.T, ctx context.Context, hostedCluster *hyperv1.HostedCluster, clusterMgr cluster.Cluster, artifactDir string) {
	t.Logf("Waiting for cluster to be destroyed. Namespace: %s, name: %s", hostedCluster.Namespace, hostedCluster.Name)
	err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		err := clusterMgr.DestroyCluster(ctx, hostedCluster)
		if err != nil {
			t.Errorf("Failed to destroy cluster, will retry: %v", err)
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	if err != nil {
		t.Errorf("Failed to destroy cluster: %v", err)
	} else {
		t.Logf("Destroyed cluster. Namespace: %s, name: %s", hostedCluster.Namespace, hostedCluster.Name)
	}
}

// DeleteNamespace deletes and finalizes the given namespace, logging any failures
// along the way.
func DeleteNamespace(t *testing.T, ctx context.Context, client crclient.Client, namespace string) {
	t.Logf("Deleting namespace: %s", namespace)
	err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		err := client.Delete(ctx, ns, &crclient.DeleteOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			t.Errorf("Failed to delete namespace: %s, will retry: %v", namespace, err)
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	if err != nil {
		t.Errorf("Failed to delete namespace: %v", err)
		return
	}

	t.Logf("Waiting for namespace to be finalized. Namespace: %s", namespace)
	err = wait.PollInfinite(1*time.Second, func() (done bool, err error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(ns), ns); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			t.Errorf("Failed to get namespace: %s. %v", namespace, err)
			return false, nil
		}
		return false, nil
	})
	if err != nil {
		t.Errorf("Namespace was not finalized: %v", err)
	} else {
		t.Logf("Deleted namespace: %s", namespace)
	}
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
		kubeClient, err := crclient.New(guestConfig, crclient.Options{Scheme: hyperapi.Scheme})
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

// DumpGuestCluster tries to collect resources from the from the hosted cluster,
// and logs any failures that occur.
func DumpGuestCluster(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, destDir string) {
	if len(destDir) == 0 {
		t.Logf("Skipping guest cluster dump because no dest dir was provided")
		return
	}
	kubeconfigTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	kubeconfig, err := WaitForGuestKubeConfig(t, kubeconfigTimeout, client, hostedCluster)
	if err != nil {
		t.Errorf("Failed to get guest kubeconfig: %v", err)
		return
	}

	kubeconfigFile, err := ioutil.TempFile(os.TempDir(), "kubeconfig-")
	if err != nil {
		t.Errorf("Failed to create temporary directory: %v", err)
		return
	}
	defer func() {
		if err := os.Remove(kubeconfigFile.Name()); err != nil {
			t.Errorf("Failed to remote temp file: %s: %v", kubeconfigFile.Name(), err)
		}
	}()

	if _, err := kubeconfigFile.Write(kubeconfig); err != nil {
		t.Errorf("Failed to write kubeconfig: %v", err)
		return
	}
	if err := kubeconfigFile.Close(); err != nil {
		t.Errorf("Failed to close kubeconfig file: %v", err)
		return
	}

	dumpDir := filepath.Join(destDir, "hostedcluster-"+hostedCluster.Name)
	t.Logf("Dumping guest cluster. Namespace: %s, name: %s, dest: %s", hostedCluster.Namespace, hostedCluster.Name, dumpDir)
	if err := aws.DumpGuestCluster(ctx, kubeconfigFile.Name(), dumpDir); err != nil {
		t.Errorf("Failed to dump guest cluster: %v", err)
		return
	}
	t.Logf("Dumped guest cluster data. Dir: %s", dumpDir)
}

func EnsureNoCrashingPods(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("No controlplane pods crash", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

		var podList corev1.PodList
		if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
		}
		for _, pod := range podList.Items {
			// It needs some specific apis, we currently don't have checking for this
			if strings.HasPrefix(pod.Name, "manifests-bootstrapper") {
				continue
			}

			// TODO: This is needed because of an upstream NPD, see e.G. here: https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/origin-ci-test/pr-logs/pull/openshift_hypershift/486/pull-ci-openshift-hypershift-main-e2e-aws-pooled/1445408206435127296/artifacts/e2e-aws-pooled/test-e2e/artifacts/namespaces/e2e-clusters-slgzn-example-f748r/core/pods/logs/capa-controller-manager-f66fd8977-knt6h-manager-previous.log
			// remove this exception once upstream is fixed and we have the fix
			if strings.HasPrefix(pod.Name, "capa-controller-manager") {
				continue
			}

			// TODO: Remove after https://issues.redhat.com/browse/HOSTEDCP-238 is done
			if strings.HasPrefix(pod.Name, "packageserver") {
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

func EnsureNodeCountMatchesNodePoolReplicas(t *testing.T, ctx context.Context, hostClient, guestClient crclient.Client, nodePoolName crclient.ObjectKey) {
	t.Run("EnsureNodeCountMatchesNodePoolReplicas", func(t *testing.T) {
		var nodepool hyperv1.NodePool
		if err := hostClient.Get(ctx, nodePoolName, &nodepool); err != nil {
			t.Fatalf("failed to get nodepool: %v", err)
		}

		var nodes corev1.NodeList
		if err := guestClient.List(ctx, &nodes); err != nil {
			t.Fatalf("failed to list nodes in guest cluster: %v", err)
		}

		var nodeCount int
		if nodepool.Spec.NodeCount != nil {
			nodeCount = int(*nodepool.Spec.NodeCount)
		}

		if nodeCount != len(nodes.Items) {
			t.Errorf("nodepool replicas %d does not match number of nodes in cluster %d", nodeCount, len(nodes.Items))
		}
	})
}
