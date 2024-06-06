//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestHAEtcdChaos launches a HighlyAvailable control plane and executes a suite
// of chaotic etcd tests which ensure no data is lost in the chaos.
func TestHAEtcdChaos(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	// Create a cluster
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.NodePoolReplicas = 0

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		t.Run("SingleMemberRecovery", testSingleMemberRecovery(ctx, mgtClient, hostedCluster))
		t.Run("KillRandomMembers", testKillRandomMembers(ctx, mgtClient, hostedCluster))
		t.Run("KillAllMembers", testKillAllMembers(ctx, mgtClient, hostedCluster))

	}).Execute(&clusterOpts, hyperv1.NonePlatform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

// testKillRandomMembers ensures that data is preserved following a period where
// random etcd members are repeatedly killed.
func testKillRandomMembers(parentCtx context.Context, client crclient.Client, cluster *hyperv1.HostedCluster) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		defer cancel()

		guestNamespace := manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name)
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
		var previousCreationError string
		err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			if err := guestClient.Create(ctx, cm); err != nil {
				if err.Error() != previousCreationError {
					t.Logf("failed to create marker ConfigMap: %v", err)
					previousCreationError = err.Error()
				}
				return false, nil
			}
			return true, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to create marker configmap")

		// Find etcd pods in the control plane namespace
		etcdSts := cpomanifests.EtcdStatefulSet(guestNamespace)
		err = client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get etcd statefulset")

		etcdPods := &corev1.PodList{}
		err = client.List(ctx, etcdPods, &crclient.ListOptions{
			Namespace:     guestNamespace,
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
					GracePeriodSeconds: pointer.Int64(0),
				})
				if err != nil {
					t.Errorf("failed to delete pod %s: %s", pod.Name, err)
				} else {
					t.Logf("deleted pod %s", pod.Name)
				}
			}, period)
		}()

		// The etcd cluster should eventually roll out completely
		var previousSTSError string
		err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			err := client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
			if err != nil {
				if err.Error() != previousSTSError {
					t.Logf("failed to get statefulset %s/%s: %s", etcdSts.Namespace, etcdSts.Name, err)
					previousSTSError = err.Error()
				}
				return false, nil
			}
			return *etcdSts.Spec.Replicas == etcdSts.Status.ReadyReplicas, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "etcd statefulset available replicas never converged")
		t.Logf("etcd statefulset recovered successfully")

		// The data should eventually be observed to have survived
		var previousCMError string
		err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			actual := &corev1.ConfigMap{}
			if err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(cm), actual); err != nil {
				if err.Error() != previousCMError {
					t.Logf("failed to get marker configmap %s/%s: %s", cm.Namespace, cm.Name, err)
					previousCMError = err.Error()
				}
				return false, nil
			}
			g.Expect(actual.Data).ToNot(BeNil(), "marker configmap is missing data")
			g.Expect(actual.Data["value"]).To(Equal(string(value)), "marker data value doesn't match original")
			t.Logf("marker data was verified")
			return true, nil
		})
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

		guestNamespace := manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name)
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
		var previousCreationError string
		err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			if err := guestClient.Create(ctx, cm); err != nil {
				if err.Error() != previousCreationError {
					t.Logf("failed to create marker ConfigMap: %v", err)
					previousCreationError = err.Error()
				}
				return false, nil
			}
			return true, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to create marker configmap")

		// Find etcd pods in the control plane namespace
		etcdSts := cpomanifests.EtcdStatefulSet(guestNamespace)
		err = client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get etcd statefulset")

		etcdPods := &corev1.PodList{}
		err = client.List(ctx, etcdPods, &crclient.ListOptions{
			Namespace:     guestNamespace,
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
					GracePeriodSeconds: pointer.Int64(0),
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

		// Ensure that all etcd pods are replaced
		var previousPodError string
		var previousUID types.UID
		err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			for _, pod := range etcdPods.Items {
				actual := &corev1.Pod{}
				if err := client.Get(ctx, crclient.ObjectKeyFromObject(&pod), actual); err != nil {
					if err.Error() != previousPodError {
						t.Logf("failed to get pod %s/%s: %v", pod.Namespace, pod.Name, err)
						previousPodError = err.Error()
					}
					return false, nil
				}
				if pod.UID == actual.UID {
					if pod.UID != previousUID {
						t.Logf("pod %s/%s not replaced yet", pod.Namespace, pod.Name)
					}
					previousUID = pod.UID
					return false, nil
				}
			}
			return true, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to wait for etcd pods to be replaced")

		// The etcd cluster should eventually roll out completely
		var previousSTSError string
		err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			err := client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
			if err != nil {
				if err.Error() != previousSTSError {
					t.Logf("failed to get statefulset %s/%s: %s", etcdSts.Namespace, etcdSts.Name, err)
					previousSTSError = err.Error()
				}
				return false, nil
			}
			return *etcdSts.Spec.Replicas == etcdSts.Status.ReadyReplicas, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "etcd statefulset available replicas never converged")
		t.Logf("etcd statefulset recovered successfully")

		// The data should eventually be observed to have survived
		var previousCMError string
		err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			actual := &corev1.ConfigMap{}
			if err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(cm), actual); err != nil {
				if err.Error() != previousCMError {
					t.Logf("failed to create marker configmap %s/%s: %s", cm.Namespace, cm.Name, err)
					previousCMError = err.Error()
				}
				return false, nil
			}
			g.Expect(actual.Data).ToNot(BeNil(), "marker configmap is missing data")
			g.Expect(actual.Data["value"]).To(Equal(string(value)), "marker data value doesn't match original")
			t.Logf("marker data was verified")
			return true, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to verify data following disruption")
	}
}

// testSingleMemberRecovery ensures that the etcd cluster can recover from a single member losing its data
func testSingleMemberRecovery(parentCtx context.Context, client crclient.Client, cluster *hyperv1.HostedCluster) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		defer cancel()

		guestNamespace := manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name)
		t.Logf("Hosted control plane namespace is %s", guestNamespace)

		// Wait for a guest client to become available
		t.Logf("Waiting for guest client to become available")
		_ = e2eutil.WaitForGuestClient(t, ctx, client, cluster)

		// Find etcd pods in the control plane namespace
		etcdSts := cpomanifests.EtcdStatefulSet(guestNamespace)
		err := client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get etcd statefulset")

		etcdPods := &corev1.PodList{}
		err = client.List(ctx, etcdPods, &crclient.ListOptions{
			Namespace:     manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name),
			LabelSelector: labels.Set(etcdSts.Spec.Selector.MatchLabels).AsSelector(),
		})

		// Delete a single etcd pod along with its pvc
		randomPod := randomPods(etcdPods.Items, 1)[0]
		originalPodID := randomPod.UID
		pvcName := "data-etcd" + strings.TrimPrefix(randomPod.Name, "etcd")
		pvc := &corev1.PersistentVolumeClaim{}
		pvc.Name = pvcName
		pvc.Namespace = randomPod.Namespace

		var wg sync.WaitGroup
		wg.Add(2)
		go func(pod *corev1.Pod) {
			defer wg.Done()
			err := client.Delete(ctx, pod)
			g.Expect(err).ToNot(HaveOccurred(), "failed to delete etcd pod")
			t.Logf("Deleted etcd pod %s", pod.Name)
		}(&randomPod)
		go func(pvc *corev1.PersistentVolumeClaim) {
			defer wg.Done()
			err := client.Delete(ctx, pvc)
			g.Expect(err).ToNot(HaveOccurred(), "failed to delete etcd pvc")
			t.Logf("Deleted etcd pvc %s", pvc.Name)
		}(pvc)
		wg.Wait()

		// Ensure that all etcd pods are replaced
		var previousPodError string
		var previoiusUID types.UID
		err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			if err := client.Get(ctx, crclient.ObjectKeyFromObject(&randomPod), &randomPod); err != nil {
				if err.Error() != previousPodError {
					t.Logf("failed to get pod %s/%s: %v", randomPod.Namespace, randomPod.Name, err)
					previousPodError = err.Error()
				}
				return false, nil
			}
			if randomPod.UID == originalPodID {
				if randomPod.UID != previoiusUID {
					t.Logf("pod %s/%s not replaced yet", randomPod.Namespace, randomPod.Name)
				}
				previoiusUID = randomPod.UID
				return false, nil
			}
			return true, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to wait for etcd pod to be replaced")

		// The etcd cluster should eventually roll out completely
		var previousSTSError string
		err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
			err := client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
			if err != nil {
				if err.Error() != previousSTSError {
					t.Logf("failed to get statefulset %s/%s: %s", etcdSts.Namespace, etcdSts.Name, err)
					previousSTSError = err.Error()
				}
				return false, nil
			}
			return *etcdSts.Spec.Replicas == etcdSts.Status.ReadyReplicas, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "etcd statefulset available replicas never converged")
		t.Logf("etcd statefulset recovered successfully")

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
