package util

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
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
	t.Logf("Created test namespace: %s", namespace.Name)
	return namespace
}

// DumpHostedCluster tries to dump important resources related to the HostedCluster, and
// logs any failures along the way.
func DumpHostedCluster(t *testing.T, ctx context.Context, hostedCluster *hyperv1.HostedCluster, artifactDir string) {
	if len(artifactDir) == 0 {
		t.Logf("Skipping cluster dump because no artifact dir was provided")
		return
	}
	err := cmdcluster.DumpCluster(ctx, &cmdcluster.DumpOptions{
		Namespace:   hostedCluster.Namespace,
		Name:        hostedCluster.Name,
		ArtifactDir: artifactDir,
	})
	if err != nil {
		t.Errorf("Failed to dump cluster: %v", err)
	}
}

// DumpAndDestroyHostedCluster calls DumpHostedCluster and then destroys the HostedCluster,
// logging any failures along the way.
func DumpAndDestroyHostedCluster(t *testing.T, ctx context.Context, hostedCluster *hyperv1.HostedCluster, awsCreds string, awsRegion string, baseDomain string, artifactDir string) {
	// TODO: Figure out why this is slow
	//DumpHostedCluster(ctx, hostedCluster, artifactDir)

	opts := &cmdcluster.DestroyOptions{
		Namespace:          hostedCluster.Namespace,
		Name:               hostedCluster.Name,
		Region:             awsRegion,
		InfraID:            hostedCluster.Name,
		BaseDomain:         baseDomain,
		AWSCredentialsFile: awsCreds,
		PreserveIAM:        false,
		ClusterGracePeriod: 15 * time.Minute,
	}

	t.Logf("Waiting for cluster to be destroyed. Namespace: %s, name: %s", hostedCluster.Namespace, hostedCluster.Name)
	err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		err := cmdcluster.DestroyCluster(ctx, opts)
		if err != nil {
			t.Errorf("Failed to destroy cluster, will retry: %v", err)
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	if err != nil {
		t.Errorf("Failed to destroy cluster: %v", err)
	} else {
		t.Logf("Destroyed cluster. Namespace: %s, name: %s", opts.Namespace, opts.Name)
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
	t.Logf("Found kubeconfig for cluster. Namespace: %s, name: %s", hostedCluster.Namespace, hostedCluster.Name)

	// TODO: this key should probably be published or an API constant
	data, hasData := guestKubeConfigSecret.Data["kubeconfig"]
	if !hasData {
		return nil, fmt.Errorf("kubeconfig secret is missing kubeconfig key")
	}
	return data, nil
}

func WaitForGuestClient(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) crclient.Client {
	g := NewWithT(t)

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

	t.Logf("successfully connected to the guest apiserver")
	return guestClient
}

func WaitForNReadyNodes(t *testing.T, ctx context.Context, client crclient.Client, n int32) []corev1.Node {
	g := NewWithT(t)

	t.Logf("Waiting for nodes to become ready. Want: %v", n)
	nodes := &corev1.NodeList{}
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
			return false, nil
		}
		t.Logf("All nodes are ready. Count: %v", len(nodes.Items))
		return true, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed to ensure guest nodes became ready")

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
	if err := cmdcluster.DumpGuestCluster(ctx, kubeconfigFile.Name(), dumpDir); err != nil {
		t.Errorf("Failed to dump guest cluster: %v", err)
		return
	}
	t.Logf("Dumped guest cluster data. Dir: %s", dumpDir)
}
