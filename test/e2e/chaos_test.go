//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestHAEtcdChaos launches a HighlyAvailable control plane and executes a suite
// of chaotic etcd tests which ensure no data is lost in the chaos.
func TestHAEtcdChaos(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	// Create a cluster
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.NodePoolReplicas = 0

	cluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, hyperv1.NonePlatform, globalOpts.ArtifactDir)

	t.Run("KillRandomMembers", testKillRandomMembers(ctx, client, cluster))
	t.Run("KillAllMembers", testKillAllMembers(ctx, client, cluster))
}

// TestEtcdChaos launches a SingleReplica control plane and executes a suite of
// chaotic etcd tests which ensure no data is lost in the chaos.
func TestEtcdChaos(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	// Create a cluster
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)
	clusterOpts.NodePoolReplicas = 0

	cluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, hyperv1.NonePlatform, globalOpts.ArtifactDir)

	t.Run("KillAllMembers", testKillAllMembers(ctx, client, cluster))
}

// testKillRandomMembers ensures that data is preserved following a period where
// random etcd members are repeatedly killed.
func testKillRandomMembers(parentCtx context.Context, client crclient.Client, cluster *hyperv1.HostedCluster) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		defer cancel()

		guestNamespace := manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name).Name
		t.Logf("Hosted control plane namespace is %s", guestNamespace)

		// Get a client for the cluster
		t.Logf("Waiting for guest client to become available")
		guestClient := e2eutil.WaitForGuestClient(t, ctx, client, cluster)

		// Create data in the cluster which should survive the ensuring chaos
		value, _ := time.Now().MarshalText()
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      e2eutil.SimpleNameGenerator.GenerateName("marker-"),
			},
			Data: map[string]string{"value": string(value)},
		}
		err := guestClient.Create(ctx, cm)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create marker configmap")

		// Find etcd pods in the control plane namespace
		etcdSts := cpomanifests.EtcdStatefulSet(guestNamespace)
		err = client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get etcd statefulset")

		etcdPods := &corev1.PodList{}
		err = client.List(ctx, etcdPods, &crclient.ListOptions{
			Namespace:     manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name).Name,
			LabelSelector: labels.Set(etcdSts.Spec.Selector.MatchLabels).AsSelector(),
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to list etcd pods")
		g.Expect(etcdPods.Items).NotTo(BeEmpty(), "couldn't find any etcd pods")
		t.Logf("found %d etcd pods", len(etcdPods.Items))

		// Delete random etcd pods for a while
		func() {
			duration, period := 30*time.Second, 5*time.Second
			t.Logf("deleting random etcd pods every %s for %s", period, duration)
			ctx, cancel := context.WithTimeout(ctx, duration)
			defer cancel()
			wait.UntilWithContext(ctx, func(ctx context.Context) {
				pod := randomPods(etcdPods.Items, 1)[0]
				err := client.Delete(ctx, &pod, &crclient.DeleteOptions{
					GracePeriodSeconds: pointer.Int64Ptr(0),
				})
				if err != nil {
					t.Errorf("failed to delete pod %s: %s", pod.Name, err)
				} else {
					t.Logf("deleted pod %s", pod.Name)
				}
			}, period)
		}()

		// The etcd cluster should eventually roll out completely
		err = wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
			err := client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
			if err != nil {
				t.Logf("failed to get statefulset %s/%s: %s", etcdSts.Namespace, etcdSts.Name, err)
				return false, nil
			}
			return *etcdSts.Spec.Replicas == etcdSts.Status.ReadyReplicas, nil
		}, ctx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "etcd statefulset available replicas never converged")
		t.Logf("etcd statefulset recovered successfully")

		// The data should eventually be observed to have survived
		err = wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
			actual := &corev1.ConfigMap{}
			if err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(cm), actual); err != nil {
				t.Logf("failed to get marker configmap: %s", err)
				return false, nil
			}
			g.Expect(actual.Data).ToNot(BeNil(), "marker configmap is missing data")
			g.Expect(actual.Data["value"]).To(Equal(string(value)), "marker data value doesn't match original")
			t.Logf("marker data was verified")
			return true, nil
		}, ctx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "failed to verify data following disruption")
	}
}

// testKillAllMembers ensures that data is preserved following the simultaneous
// loss of all etcd members.
func testKillAllMembers(parentCtx context.Context, client crclient.Client, cluster *hyperv1.HostedCluster) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		defer cancel()

		guestNamespace := manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name).Name
		t.Logf("Hosted control plane namespace is %s", guestNamespace)

		// Get a client for the cluster
		t.Logf("Waiting for guest client to become available")
		guestClient := e2eutil.WaitForGuestClient(t, ctx, client, cluster)

		// Create data in the cluster which should survive the ensuring chaos
		value, _ := time.Now().MarshalText()
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      e2eutil.SimpleNameGenerator.GenerateName("marker-"),
			},
			Data: map[string]string{"value": string(value)},
		}
		err := guestClient.Create(ctx, cm)
		g.Expect(err).NotTo(HaveOccurred(), "failed to create marker configmap")

		// Find etcd pods in the control plane namespace
		etcdSts := cpomanifests.EtcdStatefulSet(guestNamespace)
		err = client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get etcd statefulset")

		etcdPods := &corev1.PodList{}
		err = client.List(ctx, etcdPods, &crclient.ListOptions{
			Namespace:     manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name).Name,
			LabelSelector: labels.Set(etcdSts.Spec.Selector.MatchLabels).AsSelector(),
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to list etcd pods")
		g.Expect(etcdPods.Items).NotTo(BeEmpty(), "couldn't find any etcd pods")
		t.Logf("found %d etcd pods", len(etcdPods.Items))

		// Delete all etcd pods which should be a majority outage
		var wg sync.WaitGroup
		wg.Add(len(etcdPods.Items))
		for i := range etcdPods.Items {
			go func(pod *corev1.Pod) {
				timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				err := client.Delete(timeout, pod, &crclient.DeleteOptions{
					GracePeriodSeconds: pointer.Int64Ptr(0),
				})
				if err != nil {
					t.Errorf("failed to delete pod %s: %s", pod.Name, err)
				} else {
					t.Logf("deleted pod %s", pod.Name)
				}
				wg.Done()
			}(&etcdPods.Items[i])
		}
		wg.Wait()

		// The etcd cluster should eventually roll out completely
		err = wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
			err := client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
			if err != nil {
				t.Logf("failed to get statefulset %s/%s: %s", etcdSts.Namespace, etcdSts.Name, err)
				return false, nil
			}
			return *etcdSts.Spec.Replicas == etcdSts.Status.ReadyReplicas, nil
		}, ctx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "etcd statefulset available replicas never converged")
		t.Logf("etcd statefulset recovered successfully")

		// The data should eventually be observed to have survived
		err = wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
			actual := &corev1.ConfigMap{}
			if err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(cm), actual); err != nil {
				t.Logf("failed to get marker configmap: %s", err)
				return false, nil
			}
			g.Expect(actual.Data).ToNot(BeNil(), "marker configmap is missing data")
			g.Expect(actual.Data["value"]).To(Equal(string(value)), "marker data value doesn't match original")
			t.Logf("marker data was verified")
			return true, nil
		}, ctx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "failed to verify data following disruption")
	}
}

// TODO: Generics :-)
func randomPods(pods []corev1.Pod, count int) []corev1.Pod {
	var selected []corev1.Pod
	indexes := rand.Perm(len(pods))
	for i := 0; i < count; i++ {
		selected = append(selected, pods[indexes[i]])
	}
	return selected
}
