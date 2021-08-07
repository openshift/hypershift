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
	log.Info("created test namespace", "name", namespace.Name)
	return namespace
}

// DumpHostedCluster tries to dump important resources related to the HostedCluster, and
// logs any failures along the way.
func DumpHostedCluster(ctx context.Context, hostedCluster *hyperv1.HostedCluster, artifactDir string) {
	if len(artifactDir) == 0 {
		log.Info("skipping cluster dump because no artifact dir was provided")
		return
	}
	err := cmdcluster.DumpCluster(ctx, &cmdcluster.DumpOptions{
		Namespace:   hostedCluster.Namespace,
		Name:        hostedCluster.Name,
		ArtifactDir: artifactDir,
	})
	if err != nil {
		log.Error(err, "failed to dump cluster")
	}
}

// DumpAndDestroyHostedCluster calls DumpHostedCluster and then destroys the HostedCluster,
// logging any failures along the way.
func DumpAndDestroyHostedCluster(ctx context.Context, hostedCluster *hyperv1.HostedCluster, awsCreds string, awsRegion string, baseDomain string, artifactDir string) {
	DumpHostedCluster(ctx, hostedCluster, artifactDir)

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

	log.Info("waiting for cluster to be destroyed", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name)
	err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		err := cmdcluster.DestroyCluster(ctx, opts)
		if err != nil {
			log.Error(err, "failed to destroy cluster, will retry")
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	if err != nil {
		log.Error(err, "failed to destroy cluster")
	} else {
		log.Info("destroyed cluster", "namespace", opts.Namespace, "name", opts.Name)
	}
}

// DeleteNamespace deletes and finalizes the given namespace, logging any failures
// along the way.
func DeleteNamespace(ctx context.Context, client crclient.Client, namespace string) {
	log.Info("deleting namespace", "namespace", namespace)
	err := wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		err := client.Delete(ctx, ns, &crclient.DeleteOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			log.Error(err, "failed to delete namespace, will retry", "namespace", namespace)
			return false, nil
		}
		return true, nil
	}, ctx.Done())
	if err != nil {
		log.Error(err, "failed to delete namespace")
		return
	}

	log.Info("waiting for namespace to be finalized", "namespace", namespace)
	err = wait.PollInfinite(1*time.Second, func() (done bool, err error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(ns), ns); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			log.Error(err, "failed to get namespace", "namespace", namespace)
			return false, nil
		}
		return false, nil
	})
	if err != nil {
		log.Error(err, "namespace was not finalized")
	} else {
		log.Info("deleted namespace", "namespace", namespace)
	}
}

func WaitForGuestKubeConfig(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) ([]byte, error) {
	log.Info("waiting for hostedcluster kubeconfig to be published", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name)
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
	log.Info("found kubeconfig for cluster", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name)

	// TODO: this key should probably be published or an API constant
	data, hasData := guestKubeConfigSecret.Data["kubeconfig"]
	if !hasData {
		return nil, fmt.Errorf("kubeconfig secret is missing kubeconfig key")
	}
	return data, nil
}

func WaitForGuestClient(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) crclient.Client {
	g := NewWithT(t)

	guestKubeConfigSecretData, err := WaitForGuestKubeConfig(ctx, client, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "couldn't get kubeconfig")

	guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
	g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")

	log.Info("waiting for a successful connection to the guest apiserver")
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

	log.Info("successfully connected to the guest apiserver")
	return guestClient
}

func WaitForReadyNodes(t *testing.T, ctx context.Context, client crclient.Client, nodePool *hyperv1.NodePool) {
	g := NewWithT(t)

	log.Info("waiting for nodepool nodes to become ready", "namespace", nodePool.Namespace, "name", nodePool.Name)
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
		log.Info("all nodes are ready", "count", len(nodes.Items))
		return true, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed to ensure guest nodes became ready")

	log.Info("all nodes for nodepool appear to be ready", "count", int(*nodePool.Spec.NodeCount), "namespace", nodePool.Namespace, "name", nodePool.Name)
}

func WaitForImageRollout(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, image string) {
	g := NewWithT(t)

	log.Info("waiting for hostedcluster to rollout image", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name, "image", image)
	err := wait.PollUntil(10*time.Second, func() (done bool, err error) {
		latest := hostedCluster.DeepCopy()
		err = client.Get(ctx, crclient.ObjectKeyFromObject(latest), latest)
		if err != nil {
			log.Error(err, "failed to get hostedcluster")
			return false, nil
		}

		isAvailable := meta.IsStatusConditionTrue(latest.Status.Conditions, string(hyperv1.HostedClusterAvailable))

		rolloutComplete := latest.Status.Version != nil &&
			latest.Status.Version.Desired.Image == image &&
			len(latest.Status.Version.History) > 0 &&
			latest.Status.Version.History[0].Image == latest.Status.Version.Desired.Image &&
			latest.Status.Version.History[0].State == configv1.CompletedUpdate

		if isAvailable && rolloutComplete {
			return true, nil
		}
		log.Info("waiting for hostedcluster rollout", "image", image, "isAvailable", isAvailable, "rolloutComplete", rolloutComplete)
		return false, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "failed waiting for image rollout")

	log.Info("observed hostedcluster to have successfully rolled out image", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name, "image", image)
}

// DumpGuestCluster tries to collect resources from the from the hosted cluster,
// and logs any failures that occur.
func DumpGuestCluster(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, destDir string) {
	if len(destDir) == 0 {
		log.Info("skipping guest cluster dump because no dest dir was provided")
		return
	}
	kubeconfigTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	kubeconfig, err := WaitForGuestKubeConfig(kubeconfigTimeout, client, hostedCluster)
	if err != nil {
		log.Error(err, "failed to get guest kubeconfig")
		return
	}

	kubeconfigFile, err := ioutil.TempFile(os.TempDir(), "kubeconfig-")
	if err != nil {
		log.Error(err, "failed to create temporary directory")
		return
	}
	defer func() {
		if err := os.Remove(kubeconfigFile.Name()); err != nil {
			log.Error(err, "failed to remote temp file", "file", kubeconfigFile.Name())
		}
	}()

	if _, err := kubeconfigFile.Write(kubeconfig); err != nil {
		log.Error(err, "failed to write kubeconfig")
		return
	}
	if err := kubeconfigFile.Close(); err != nil {
		log.Error(err, "failed to close kubeconfig file")
		return
	}

	dumpDir := filepath.Join(destDir, "hostedcluster-"+hostedCluster.Name)
	log.Info("dumping guest cluster", "namespace", hostedCluster.Namespace, "name", hostedCluster.Name, "dest", dumpDir)
	if err := cmdcluster.DumpGuestCluster(ctx, kubeconfigFile.Name(), dumpDir); err != nil {
		log.Error(err, "failed to dump guest cluster")
		return
	}
	log.Info("dumped guest cluster data", "dir", dumpDir)
}
