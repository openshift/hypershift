package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	cmdcluster "github.com/openshift/hypershift/cmd/cluster"
)

func GenerateNamespace(t *testing.T, ctx context.Context, client crclient.Client, prefix string) *corev1.Namespace {
	g := NewWithT(t)
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prefix,
			Labels: map[string]string{
				"hypershift-e2e-component": "hostedclusters-namespace",
			},
		},
	}
	err := client.Create(ctx, namespace)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")
	g.Expect(namespace.Name).ToNot(BeEmpty(), "generated namespace has no name")
	t.Logf("Created test namespace %s", namespace.Name)
	return namespace
}

func DestroyCluster(t *testing.T, ctx context.Context, opts *cmdcluster.DestroyOptions, artifactDir string) {
	if len(artifactDir) > 0 {
		err := cmdcluster.DumpCluster(ctx, &cmdcluster.DumpOptions{
			Namespace:   opts.Namespace,
			Name:        opts.Name,
			ArtifactDir: artifactDir,
		})
		if err != nil {
			t.Logf("error dumping cluster contents: %s", err)
		}
	}

	g := NewWithT(t)

	t.Logf("Waiting for hostedcluster %s/%s to be destroyed", opts.Namespace, opts.Name)
	err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		err := cmdcluster.DestroyCluster(ctx, opts)
		if err != nil {
			t.Logf("error destroying cluster, will retry: %s", err)
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed to destroy cluster")

	t.Logf("Finished destroying hostedcluster %s/%s", opts.Namespace, opts.Name)
}

func DeleteNamespace(t *testing.T, ctx context.Context, client crclient.Client, namespace string) {
	g := NewWithT(t)

	t.Logf("Deleting namespace %s", namespace)
	err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		err := client.Delete(ctx, ns, &crclient.DeleteOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			t.Logf("error deleting namespace %q, will retry: %s", namespace, err)
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed to delete namespace")

	t.Logf("Waiting for namespace %q to be finalized", namespace)
	err = wait.PollInfinite(1*time.Second, func() (done bool, err error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(ns), ns); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			t.Logf("error getting namespace %q: %s", namespace, err)
			return false, nil
		}
		return false, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), "namespace was not finalized")

	t.Logf("Finished deleting namespace %s", namespace)
}

func WaitForGuestClient(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) crclient.Client {
	g := NewWithT(t)

	t.Logf("Waiting for hostedcluster %s/%s kubeconfig to be published", hostedCluster.Namespace, hostedCluster.Name)
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
	g.Expect(err).NotTo(HaveOccurred(), "guest kubeconfig didn't become available")

	// TODO: this key should probably be published or an API constant
	g.Expect(guestKubeConfigSecret.Data).To(HaveKey("kubeconfig"), "guest kubeconfig secret is missing kubeconfig key")
	guestKubeConfigSecretData := guestKubeConfigSecret.Data["kubeconfig"]

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

	t.Logf("Successfully connected to the guest apiserver")
	return guestClient
}

func WaitForReadyNodes(t *testing.T, ctx context.Context, client crclient.Client, nodePool *hyperv1.NodePool) {
	g := NewWithT(t)

	t.Logf("Waiting for nodepool %s/%s nodes to become ready", nodePool.Namespace, nodePool.Name)
	nodes := &corev1.NodeList{}
	err := wait.PollUntil(5*time.Second, func() (done bool, err error) {
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
		if len(readyNodes) != int(*nodePool.Spec.NodeCount) {
			return false, nil
		}
		t.Logf("found %d ready nodes", len(nodes.Items))
		return true, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed to ensure guest nodes became ready")

	t.Logf("All %d nodes for nodepool %s/%s appear to be ready", int(*nodePool.Spec.NodeCount), nodePool.Namespace, nodePool.Name)
}

func WaitForReadyClusterOperators(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewWithT(t)

	t.Logf("Waiting for hostedcluster %s/%s operators to become ready", hostedCluster.Namespace, hostedCluster.Name)
	clusterOperators := &configv1.ClusterOperatorList{}
	err := wait.PollUntil(10*time.Second, func() (done bool, err error) {
		err = client.List(ctx, clusterOperators)
		if err != nil {
			t.Logf("failed to list cluster operators: %v", err)
			return false, nil
		}
		if len(clusterOperators.Items) == 0 {
			return false, nil
		}
		ready := true
		for _, clusterOperator := range clusterOperators.Items {
			available := false
			degraded := true
			for _, cond := range clusterOperator.Status.Conditions {
				if cond.Type == configv1.OperatorAvailable && cond.Status == configv1.ConditionTrue {
					available = true
				}
				if cond.Type == configv1.OperatorDegraded && cond.Status == configv1.ConditionFalse {
					degraded = false
				}
				// TODO: This is a bug in the console operator where it doesn't do its route
				// health check periodically https://bugzilla.redhat.com/show_bug.cgi?id=1945326
				// Fortunately, the ingress operator also does a canary route check that ensures
				// that direct ingress is working so we still have coverage.
				if clusterOperator.GetName() == "console" {
					degraded = false
				}
			}
			if !available || degraded {
				ready = false
				break
			}
		}
		if !ready {
			return false, nil
		}
		t.Logf("guest cluster operators are ready")
		return true, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed to ensure guest cluster operators became ready")

	t.Logf("All cluster operators for hostedcluster %s/%s appear to be ready", hostedCluster.Namespace, hostedCluster.Name)
}

func WaitForImageRollout(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, image string) {
	g := NewWithT(t)

	t.Logf("Waiting for hostedcluster %s/%s to rollout image %s", hostedCluster.Namespace, hostedCluster.Name, image)
	err := wait.PollUntil(1*time.Second, func() (done bool, err error) {
		latest := hostedCluster.DeepCopy()
		err = client.Get(ctx, crclient.ObjectKeyFromObject(latest), latest)
		if err != nil {
			t.Logf("error getting cluster: %s", err)
			return false, nil
		}

		isAvailable := meta.IsStatusConditionPresentAndEqual(latest.Status.Conditions, string(hyperv1.Available), metav1.ConditionTrue)

		rolloutComplete := latest.Status.Version != nil &&
			latest.Status.Version.Desired.Image == image &&
			len(latest.Status.Version.History) > 0 &&
			latest.Status.Version.History[0].Image == latest.Status.Version.Desired.Image &&
			latest.Status.Version.History[0].State == configv1.CompletedUpdate

		if isAvailable && rolloutComplete {
			return true, nil
		}
		t.Logf("Still waiting for hostedcluster rollout (image=%s, isAvailable=%v, rolloutComplete=%v)", image, isAvailable, rolloutComplete)
		return false, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed waiting for image rollout")

	t.Logf("Observed hostedcluster %s/%s successfully rollout image %s", hostedCluster.Namespace, hostedCluster.Name, image)
}
