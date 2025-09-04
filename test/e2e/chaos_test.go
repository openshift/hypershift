//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	etcdrecoverymanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/etcdrecovery"
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
		t.Run("SingleMemberRecoveryWithCorruption", testEtcdMemberCorruption(ctx, mgtClient, hostedCluster))
		t.Run("SingleMissingMemberRecovery", testEtcdMemberMissing(ctx, mgtClient, hostedCluster))

	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "ha-etcd-chaos", globalOpts.ServiceAccountSigningKey)
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
		e2eutil.EventuallyObject(t, ctx, "create marker", func(ctx context.Context) (*corev1.ConfigMap, error) {
			err := guestClient.Create(ctx, cm)
			return cm, err
		}, nil)

		// Find etcd pods in the control plane namespace
		etcdSts := cpomanifests.EtcdStatefulSet(guestNamespace)
		err := client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
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
					GracePeriodSeconds: ptr.To[int64](0),
				})
				if err != nil {
					t.Errorf("failed to delete pod %s: %s", pod.Name, err)
				} else {
					t.Logf("deleted pod %s", pod.Name)
				}
			}, period)
		}()

		// The etcd cluster should eventually roll out completely
		e2eutil.EventuallyObject(t, ctx, "etcd StatefulSet replicas to converge", func(ctx context.Context) (*appsv1.StatefulSet, error) {
			sts := cpomanifests.EtcdStatefulSet(guestNamespace)
			err := client.Get(ctx, crclient.ObjectKeyFromObject(sts), sts)
			return sts, err
		}, []e2eutil.Predicate[*appsv1.StatefulSet]{func(sts *appsv1.StatefulSet) (done bool, reasons string, err error) {
			want := ptr.Deref(etcdSts.Spec.Replicas, 0)
			got := sts.Status.ReadyReplicas
			return want != 0 && want == got, fmt.Sprintf("wanted %d replicas in spec, got %d in status", want, got), nil
		}}, e2eutil.WithInterval(5*time.Second), e2eutil.WithTimeout(30*time.Minute))

		// The data should eventually be observed to have survived
		e2eutil.EventuallyObject(t, ctx, "verify data following disruption", func(ctx context.Context) (*corev1.ConfigMap, error) {
			actual := &corev1.ConfigMap{}
			err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(cm), actual)
			return actual, err
		}, []e2eutil.Predicate[*corev1.ConfigMap]{func(configMap *corev1.ConfigMap) (done bool, reasons string, err error) {
			diff := cmp.Diff(cm.Data, configMap.Data)
			return diff == "", fmt.Sprintf("incorrect data: %v", diff), nil
		}}, e2eutil.WithInterval(5*time.Second), e2eutil.WithTimeout(30*time.Minute))
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
		e2eutil.EventuallyObject(t, ctx, "create marker", func(ctx context.Context) (*corev1.ConfigMap, error) {
			err := guestClient.Create(ctx, cm)
			return cm, err
		}, nil)

		// Find etcd pods in the control plane namespace
		etcdSts := cpomanifests.EtcdStatefulSet(guestNamespace)
		err := client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
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
					GracePeriodSeconds: ptr.To[int64](0),
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
		e2eutil.EventuallyObjects(t, ctx, "etcd Pods to be replaced", func(ctx context.Context) ([]*corev1.Pod, error) {
			pods := &corev1.PodList{}
			err := client.List(ctx, etcdPods, &crclient.ListOptions{
				Namespace:     guestNamespace,
				LabelSelector: labels.Set(etcdSts.Spec.Selector.MatchLabels).AsSelector(),
			})
			var items []*corev1.Pod
			for i := range pods.Items {
				items[i] = &pods.Items[i]
			}
			return items, err
		}, nil, []e2eutil.Predicate[*corev1.Pod]{func(pod *corev1.Pod) (done bool, reasons string, err error) {
			for _, previousPod := range etcdPods.Items {
				if previousPod.Namespace == pod.Namespace && previousPod.Name == pod.Name {
					return previousPod.UID != pod.UID, fmt.Sprintf("Pod has UID %s", pod.UID), nil
				}
			}
			return false, "Pod not found in list", nil
		}}, e2eutil.WithInterval(5*time.Second), e2eutil.WithTimeout(30*time.Minute))

		// The etcd cluster should eventually roll out completely
		e2eutil.EventuallyObject(t, ctx, "etcd StatefulSet replicas to converge", func(ctx context.Context) (*appsv1.StatefulSet, error) {
			sts := cpomanifests.EtcdStatefulSet(guestNamespace)
			err := client.Get(ctx, crclient.ObjectKeyFromObject(sts), sts)
			return sts, err
		}, []e2eutil.Predicate[*appsv1.StatefulSet]{func(sts *appsv1.StatefulSet) (done bool, reasons string, err error) {
			want := ptr.Deref(etcdSts.Spec.Replicas, 0)
			got := sts.Status.ReadyReplicas
			return want != 0 && want == got, fmt.Sprintf("wanted %d replicas in spec, got %d in status", want, got), nil
		}}, e2eutil.WithInterval(5*time.Second), e2eutil.WithTimeout(30*time.Minute))

		// The data should eventually be observed to have survived
		e2eutil.EventuallyObject(t, ctx, "verify data following disruption", func(ctx context.Context) (*corev1.ConfigMap, error) {
			actual := &corev1.ConfigMap{}
			err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(cm), actual)
			return actual, err
		}, []e2eutil.Predicate[*corev1.ConfigMap]{func(configMap *corev1.ConfigMap) (done bool, reasons string, err error) {
			diff := cmp.Diff(cm.Data, configMap.Data)
			return diff == "", fmt.Sprintf("incorrect data: %v", diff), nil
		}}, e2eutil.WithInterval(5*time.Second), e2eutil.WithTimeout(30*time.Minute))
	}
}

// testEtcdMemberMissing ensures that the etcd cluster can be recovered having 1 member with data corruption
func testEtcdMemberCorruption(parentCtx context.Context, client crclient.Client, cluster *hyperv1.HostedCluster) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		defer cancel()

		guestNamespace := manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name)
		t.Logf("Hosted control plane namespace is %s", guestNamespace)

		// Find etcd pods in the control plane namespace
		etcdSts := cpomanifests.EtcdStatefulSet(guestNamespace)
		err := client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get etcd statefulset")

		etcdPods := &corev1.PodList{}
		err = client.List(ctx, etcdPods, &crclient.ListOptions{
			Namespace:     manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name),
			LabelSelector: labels.Set(etcdSts.Spec.Selector.MatchLabels).AsSelector(),
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to get etcd pods")

		pod := randomPods(etcdPods.Items, 1)[0]
		command := fmt.Sprintf("find /var/lib/data/member/wal -type f -name \"*.wal\" -print0 | shuf -z -n1 | xargs -0 rm")

		t.Logf("Deleting wal file from etcd pod: %s", pod.Name)
		cmdStdout, err := e2eutil.RunCommandInPod(ctx, client, "etcd", pod.Namespace, []string{"/bin/sh", "-c", command}, "etcd", 5*time.Minute)
		g.Expect(err).NotTo(HaveOccurred(), "failed to delete wal file from etcd pod: %s", pod.Name)
		g.Expect(cmdStdout).NotTo(ContainSubstring("No such file or directory"), "failed to delete wal file from etcd pod: %s", pod.Name)

		t.Logf("Deleting pod: %s", pod.Name)
		err = client.Delete(ctx, &pod)
		g.Expect(err).NotTo(HaveOccurred(), "failed to delete pod: %s", pod.Name)

		// Etcd recovery Job should be created
		// We don't check if the job is completed because it will be deleted after completion
		e2eutil.EventuallyObject(t, ctx, "etcd recovery job to be active", func(ctx context.Context) (*batchv1.Job, error) {
			recoveryJob := etcdrecoverymanifests.EtcdRecoveryJob(guestNamespace)
			err := client.Get(ctx, crclient.ObjectKeyFromObject(recoveryJob), recoveryJob)
			return recoveryJob, err
		}, []e2eutil.Predicate[*batchv1.Job]{func(job *batchv1.Job) (done bool, reasons string, err error) {
			want := int32(1)
			got := job.Status.Active
			return want != 0 && want == got, fmt.Sprintf("wanted status active to be %d , got %d", want, got), nil
		}}, e2eutil.WithInterval(5*time.Second), e2eutil.WithTimeout(10*time.Minute))

		// The etcd cluster should eventually roll out completely
		e2eutil.EventuallyObject(t, ctx, "etcd StatefulSet replicas to converge", func(ctx context.Context) (*appsv1.StatefulSet, error) {
			sts := cpomanifests.EtcdStatefulSet(guestNamespace)
			err := client.Get(ctx, crclient.ObjectKeyFromObject(sts), sts)
			return sts, err
		}, []e2eutil.Predicate[*appsv1.StatefulSet]{func(sts *appsv1.StatefulSet) (done bool, reasons string, err error) {
			want := ptr.Deref(etcdSts.Spec.Replicas, 3)
			got := sts.Status.ReadyReplicas
			return want != 3 && want == got, fmt.Sprintf("wanted %d replicas in spec, got %d in status", want, got), nil
		}}, e2eutil.WithInterval(5*time.Second), e2eutil.WithTimeout(30*time.Minute))
	}
}

// testEtcdMemberMissing ensures that the etcd cluster can recover from a missing member
func testEtcdMemberMissing(parentCtx context.Context, client crclient.Client, cluster *hyperv1.HostedCluster) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		defer cancel()

		guestNamespace := manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name)
		t.Logf("Hosted control plane namespace is %s", guestNamespace)

		// Find etcd pods in the control plane namespace
		etcdSts := cpomanifests.EtcdStatefulSet(guestNamespace)
		err := client.Get(ctx, crclient.ObjectKeyFromObject(etcdSts), etcdSts)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get etcd statefulset")

		etcdPods := &corev1.PodList{}
		err = client.List(ctx, etcdPods, &crclient.ListOptions{
			Namespace:     manifests.HostedControlPlaneNamespace(cluster.Namespace, cluster.Name),
			LabelSelector: labels.Set(etcdSts.Spec.Selector.MatchLabels).AsSelector(),
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to get etcd pods")

		pod := randomPods(etcdPods.Items, 1)[0]
		ep := fmt.Sprintf("https://etcd-client.%s.svc:2379", guestNamespace)
		baseCommand := []string{
			"/usr/bin/etcdctl",
			"--cacert=/etc/etcd/tls/etcd-ca/ca.crt",
			"--cert=/etc/etcd/tls/server/server.crt",
			"--key /etc/etcd/tls/server/server.key",
			fmt.Sprintf("--endpoints=%s", ep),
		}

		// Get Etcd member ID
		innerCommand := fmt.Sprintf("member list | grep %s | awk '{print $1}' | tr -d ,", pod.Name)
		memberDiscoveryCommand := append(baseCommand, innerCommand)

		// Final etcd commands
		command := append(baseCommand, "member", "remove", fmt.Sprintf("$(%s)", memberDiscoveryCommand))

		t.Logf("Removing Etcd Member: %s", pod.Name)
		cmdStdout, err := e2eutil.RunCommandInPod(ctx, client, "etcd", pod.Namespace, command, "etcd", 5*time.Minute)
		g.Expect(err).NotTo(HaveOccurred(), "failed to remove etcd member: %s", pod.Name)
		g.Expect(cmdStdout).NotTo(ContainSubstring("Error:"), "failed to remove etcd member: %s", pod.Name)

		t.Logf("Deleting pod: %s", pod.Name)
		err = client.Delete(ctx, &pod)
		g.Expect(err).NotTo(HaveOccurred(), "failed to delete pod: %s", pod.Name)

		// Etcd recovery Job should be created
		// We don't check if the job is completed because it will be deleted after completion
		e2eutil.EventuallyObject(t, ctx, "etcd recovery job to be active", func(ctx context.Context) (*batchv1.Job, error) {
			recoveryJob := etcdrecoverymanifests.EtcdRecoveryJob(guestNamespace)
			err := client.Get(ctx, crclient.ObjectKeyFromObject(recoveryJob), recoveryJob)
			return recoveryJob, err
		}, []e2eutil.Predicate[*batchv1.Job]{func(job *batchv1.Job) (done bool, reasons string, err error) {
			want := int32(1)
			got := job.Status.Active
			return want != 0 && want == got, fmt.Sprintf("wanted status active to be %d , got %d", want, got), nil
		}}, e2eutil.WithInterval(5*time.Second), e2eutil.WithTimeout(10*time.Minute))

		// The etcd cluster should eventually roll out completely
		e2eutil.EventuallyObject(t, ctx, "etcd StatefulSet replicas to converge", func(ctx context.Context) (*appsv1.StatefulSet, error) {
			sts := cpomanifests.EtcdStatefulSet(guestNamespace)
			err := client.Get(ctx, crclient.ObjectKeyFromObject(sts), sts)
			return sts, err
		}, []e2eutil.Predicate[*appsv1.StatefulSet]{func(sts *appsv1.StatefulSet) (done bool, reasons string, err error) {
			want := ptr.Deref(etcdSts.Spec.Replicas, 3)
			got := sts.Status.ReadyReplicas
			return want != 3 && want == got, fmt.Sprintf("wanted %d replicas in spec, got %d in status", want, got), nil
		}}, e2eutil.WithInterval(5*time.Second), e2eutil.WithTimeout(30*time.Minute))
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
		g.Expect(err).NotTo(HaveOccurred(), "failed to get etcd pods")

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

		// Ensure that the deleted etcd pod is replaced
		e2eutil.EventuallyObject(t, ctx, "the deleted etcd pod is replaced", func(ctx context.Context) (*corev1.Pod, error) {
			pod := &corev1.Pod{}
			err := client.Get(ctx, crclient.ObjectKeyFromObject(&randomPod), pod)
			return pod, err
		}, []e2eutil.Predicate[*corev1.Pod]{func(pod *corev1.Pod) (done bool, reasons string, err error) {
			return originalPodID != pod.UID, fmt.Sprintf("Pod has UID %s", pod.UID), nil
		}}, e2eutil.WithInterval(5*time.Second), e2eutil.WithTimeout(30*time.Minute))

		// The etcd cluster should eventually roll out completely
		e2eutil.EventuallyObject(t, ctx, "etcd StatefulSet replicas to converge", func(ctx context.Context) (*appsv1.StatefulSet, error) {
			sts := cpomanifests.EtcdStatefulSet(guestNamespace)
			err := client.Get(ctx, crclient.ObjectKeyFromObject(sts), sts)
			return sts, err
		}, []e2eutil.Predicate[*appsv1.StatefulSet]{func(sts *appsv1.StatefulSet) (done bool, reasons string, err error) {
			want := ptr.Deref(etcdSts.Spec.Replicas, 0)
			got := sts.Status.ReadyReplicas
			return want != 0 && want == got, fmt.Sprintf("wanted %d replicas in spec, got %d in status", want, got), nil
		}}, e2eutil.WithInterval(5*time.Second), e2eutil.WithTimeout(30*time.Minute))

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
