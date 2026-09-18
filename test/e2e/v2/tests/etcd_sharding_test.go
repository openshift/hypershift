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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	etcdutil "github.com/openshift/hypershift/support/etcd"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterEtcdShardingTests registers all etcd sharding test cases.
func RegisterEtcdShardingTests(getTestCtx internal.TestContextGetter) {
	EtcdShardDeploymentTest(getTestCtx)
	EtcdShardKubeAPIServerRoutingTest(getTestCtx)
	EtcdShardEventRoutingTest(getTestCtx)
	EtcdShardLeaseRoutingTest(getTestCtx)
	EtcdShardUnshardedResourceTest(getTestCtx)
	EtcdShardMemberRestartTest(getTestCtx)
	EtcdShardImmutabilityTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:EtcdSharding] Etcd Sharding", Label("lifecycle", "etcd-sharding"), Ordered, func() {
	var testCtx *internal.TestContext

	BeforeAll(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterEtcdShardingTests(func() *internal.TestContext { return testCtx })
})

// EtcdShardDeploymentTest verifies that every configured shard has a healthy
// StatefulSet with the requested replica count and storage backend, the
// derived client and discovery Services, per-shard serving and peer
// certificates, and a PodDisruptionBudget when it runs three replicas.
func EtcdShardDeploymentTest(getTestCtx internal.TestContextGetter) {
	It("should deploy a dedicated etcd cluster for each shard", func() {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace
		shards := etcdShardsOrSkip(testCtx)

		for _, shard := range shards {
			stsName := e2eutil.EtcdShardStatefulSetName(shard.Name)

			e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("etcd shard StatefulSet %s to be ready", stsName),
				func(ctx context.Context) (*appsv1.StatefulSet, error) {
					sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: cpNamespace, Name: stsName}}
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(sts), sts)
					return sts, err
				},
				[]e2eutil.Predicate[*appsv1.StatefulSet]{func(sts *appsv1.StatefulSet) (bool, string, error) {
					if ptr.Deref(sts.Spec.Replicas, 0) != shard.Replicas {
						return false, fmt.Sprintf("wanted %d replicas, got %d", shard.Replicas, ptr.Deref(sts.Spec.Replicas, 0)), nil
					}
					got := sts.Status.ReadyReplicas
					return got == shard.Replicas, fmt.Sprintf("wanted %d ready replicas, got %d", shard.Replicas, got), nil
				}},
				e2eutil.WithInterval(10*time.Second),
				e2eutil.WithTimeout(15*time.Minute),
			)

			sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: cpNamespace, Name: stsName}}
			Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(sts), sts)).To(Succeed())

			switch shard.Storage.Type {
			case hyperv1.EmptyDirEtcdShardStorage:
				Expect(sts.Spec.VolumeClaimTemplates).To(BeEmpty(),
					"EmptyDir shard %s must not declare volumeClaimTemplates", shard.Name)
				var dataVolume *corev1.Volume
				for i := range sts.Spec.Template.Spec.Volumes {
					if sts.Spec.Template.Spec.Volumes[i].Name == "data" {
						dataVolume = &sts.Spec.Template.Spec.Volumes[i]
						break
					}
				}
				Expect(dataVolume).NotTo(BeNil(), "EmptyDir shard %s must have a 'data' volume", shard.Name)
				Expect(dataVolume.EmptyDir).NotTo(BeNil(), "shard %s 'data' volume must be an emptyDir", shard.Name)
				Expect(dataVolume.EmptyDir.Medium).To(Equal(corev1.StorageMediumMemory),
					"shard %s emptyDir must be memory backed", shard.Name)
				Expect(dataVolume.EmptyDir.SizeLimit).NotTo(BeNil(),
					"shard %s emptyDir must have a size limit", shard.Name)
			default:
				Expect(sts.Spec.VolumeClaimTemplates).NotTo(BeEmpty(),
					"PersistentVolume shard %s must declare volumeClaimTemplates", shard.Name)
			}

			for _, svcName := range []string{etcdutil.ClientServiceName(stsName), etcdutil.DiscoveryServiceName(stsName)} {
				svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: cpNamespace, Name: svcName}}
				Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc)).To(Succeed(),
					"Service %s must exist for shard %s", svcName, shard.Name)
				Expect(svc.Spec.Selector).To(HaveKeyWithValue("app", stsName),
					"Service %s must select the pods of shard %s", svcName, shard.Name)
			}

			for _, secretName := range []string{stsName + "-server-tls", stsName + "-peer-tls"} {
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: cpNamespace, Name: secretName}}
				Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(secret), secret)).To(Succeed(),
					"Secret %s must exist for shard %s", secretName, shard.Name)
			}

			pdb := &policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Namespace: cpNamespace, Name: stsName}}
			err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(pdb), pdb)
			if shard.Replicas >= 3 {
				Expect(err).NotTo(HaveOccurred(),
					"shard %s with %d replicas must have a PodDisruptionBudget", shard.Name, shard.Replicas)
			} else {
				Expect(apierrors.IsNotFound(err)).To(BeTrue(),
					"single replica shard %s must not have a PodDisruptionBudget", shard.Name)
			}

			e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("ControlPlaneComponent %s to be available", stsName),
				func(ctx context.Context) (*hyperv1.ControlPlaneComponent, error) {
					cpc := &hyperv1.ControlPlaneComponent{ObjectMeta: metav1.ObjectMeta{Namespace: cpNamespace, Name: stsName}}
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(cpc), cpc)
					return cpc, err
				},
				[]e2eutil.Predicate[*hyperv1.ControlPlaneComponent]{
					e2eutil.ConditionPredicate[*hyperv1.ControlPlaneComponent](e2eutil.Condition{
						Type:   string(hyperv1.ControlPlaneComponentAvailable),
						Status: metav1.ConditionTrue,
					}),
				},
				e2eutil.WithInterval(10*time.Second),
				e2eutil.WithTimeout(15*time.Minute),
			)
		}
	})
}

// EtcdShardKubeAPIServerRoutingTest verifies the generated kube-apiserver
// config carries exactly one --etcd-servers-overrides entry per sharded
// resource, each pointing at the owning shard's client Service.
func EtcdShardKubeAPIServerRoutingTest(getTestCtx internal.TestContextGetter) {
	It("should configure kube-apiserver to route each sharded resource to its shard", func() {
		testCtx := getTestCtx()
		shards := etcdShardsOrSkip(testCtx)

		expected := e2eutil.ExpectedEtcdServersOverrides(shards, testCtx.ControlPlaneNamespace)
		Expect(expected).NotTo(BeEmpty(), "the sharded cluster must route at least one resource")

		actual, err := e2eutil.KASEtcdServersOverrides(testCtx.Context, testCtx.MgmtClient, testCtx.ControlPlaneNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(actual).To(ConsistOf(expected))
	})
}

// EtcdShardEventRoutingTest creates a marker Event in the hosted cluster and
// proves it is persisted in the events shard and nowhere else.
func EtcdShardEventRoutingTest(getTestCtx internal.TestContextGetter) {
	It("should store events in the events shard and not in the default etcd", func() {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace
		shards := etcdShardsOrSkip(testCtx)
		requireEtcdShard(shards, e2eutil.EtcdShardEventsName)

		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())

		marker := createMarkerEvent(ctx, hcClient)
		DeferCleanup(func() {
			if err := hcClient.Delete(ctx, marker); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete marker Event %s", marker.Name)
			}
		})

		markerKey := e2eutil.EtcdResourceKeyPrefix("", "events") + marker.Namespace + "/" + marker.Name
		eventsShard := e2eutil.EtcdShardStatefulSetName(e2eutil.EtcdShardEventsName)

		shardKeys, err := e2eutil.EtcdKeysWithPrefix(ctx, testCtx.MgmtClient, cpNamespace, eventsShard, markerKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(shardKeys).To(ContainElement(markerKey), "marker event must be stored in the %s shard", eventsShard)

		defaultKeys, err := e2eutil.EtcdKeysWithPrefix(ctx, testCtx.MgmtClient, cpNamespace, "etcd", markerKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(defaultKeys).To(BeEmpty(), "marker event must not be stored in the default etcd")
	})
}

// EtcdShardLeaseRoutingTest verifies leases live exclusively in the leases
// shard. The hosted cluster's controllers renew leases continuously, so the
// shard must be non-empty while the default etcd must never have seen one.
func EtcdShardLeaseRoutingTest(getTestCtx internal.TestContextGetter) {
	It("should store leases exclusively in the leases shard", func() {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace
		shards := etcdShardsOrSkip(testCtx)
		requireEtcdShard(shards, e2eutil.EtcdShardLeasesName)

		leasesShard := e2eutil.EtcdShardStatefulSetName(e2eutil.EtcdShardLeasesName)
		leasePrefix := e2eutil.EtcdResourceKeyPrefix("", "leases")

		shardCount, err := e2eutil.EtcdKeyCount(ctx, testCtx.MgmtClient, cpNamespace, leasesShard, leasePrefix)
		Expect(err).NotTo(HaveOccurred())
		Expect(shardCount).To(BeNumerically(">", 0), "the %s shard must hold the hosted cluster's leases", leasesShard)

		defaultCount, err := e2eutil.EtcdKeyCount(ctx, testCtx.MgmtClient, cpNamespace, "etcd", leasePrefix)
		Expect(err).NotTo(HaveOccurred())
		Expect(defaultCount).To(Equal(0), "the default etcd must not hold any leases when leases are sharded")
	})
}

// EtcdShardUnshardedResourceTest verifies that resource kinds without a shard
// stay in the default etcd and never leak into a shard.
func EtcdShardUnshardedResourceTest(getTestCtx internal.TestContextGetter) {
	It("should keep unsharded resources in the default etcd", func() {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace
		shards := etcdShardsOrSkip(testCtx)

		configMapPrefix := e2eutil.EtcdResourceKeyPrefix("", "configmaps")

		defaultCount, err := e2eutil.EtcdKeyCount(ctx, testCtx.MgmtClient, cpNamespace, "etcd", configMapPrefix)
		Expect(err).NotTo(HaveOccurred())
		Expect(defaultCount).To(BeNumerically(">", 0), "the default etcd must hold unsharded resources")

		for _, shard := range shards {
			stsName := e2eutil.EtcdShardStatefulSetName(shard.Name)
			shardCount, err := e2eutil.EtcdKeyCount(ctx, testCtx.MgmtClient, cpNamespace, stsName, configMapPrefix)
			Expect(err).NotTo(HaveOccurred())
			Expect(shardCount).To(Equal(0), "shard %s must not hold configmaps", stsName)
		}
	})
}

// EtcdShardMemberRestartTest deletes one member of the events shard and
// verifies the shard converges and keeps serving writes for the resource kind
// it owns. Data loss on the shard itself is expected for EmptyDir backed
// shards, so only post-restart writes are asserted.
func EtcdShardMemberRestartTest(getTestCtx internal.TestContextGetter) {
	It("should keep serving sharded resources after a shard member restarts", func() {
		testCtx := getTestCtx()
		ctx := testCtx.Context
		cpNamespace := testCtx.ControlPlaneNamespace
		shards := etcdShardsOrSkip(testCtx)
		shard := requireEtcdShard(shards, e2eutil.EtcdShardEventsName)
		if shard.Replicas < 3 {
			Skip("restarting a shard member requires a 3 replica shard")
		}
		stsName := e2eutil.EtcdShardStatefulSetName(shard.Name)

		sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: cpNamespace, Name: stsName}}
		Expect(testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(sts), sts)).To(Succeed())

		pods := &corev1.PodList{}
		Expect(testCtx.MgmtClient.List(ctx, pods, &crclient.ListOptions{
			Namespace:     cpNamespace,
			LabelSelector: labels.Set(sts.Spec.Selector.MatchLabels).AsSelector(),
		})).To(Succeed())
		Expect(pods.Items).NotTo(BeEmpty(), "no pods found for shard %s", stsName)

		victim := pods.Items[0]
		originalUID := victim.UID
		GinkgoWriter.Printf("Deleting etcd shard pod %s\n", victim.Name)
		Expect(testCtx.MgmtClient.Delete(ctx, &victim)).To(Succeed(), "failed to delete shard pod %s", victim.Name)

		e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("shard pod %s to be replaced", victim.Name),
			func(ctx context.Context) (*corev1.Pod, error) {
				pod := &corev1.Pod{}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&victim), pod)
				return pod, err
			},
			[]e2eutil.Predicate[*corev1.Pod]{func(pod *corev1.Pod) (bool, string, error) {
				return pod.UID != originalUID, fmt.Sprintf("pod UID %s", pod.UID), nil
			}},
			e2eutil.WithInterval(5*time.Second),
			e2eutil.WithTimeout(15*time.Minute),
		)

		e2eutil.EventuallyObject(GinkgoTB(), ctx, fmt.Sprintf("shard StatefulSet %s to converge", stsName),
			func(ctx context.Context) (*appsv1.StatefulSet, error) {
				current := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: cpNamespace, Name: stsName}}
				err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(current), current)
				return current, err
			},
			[]e2eutil.Predicate[*appsv1.StatefulSet]{func(current *appsv1.StatefulSet) (bool, string, error) {
				got := current.Status.ReadyReplicas
				return got == shard.Replicas, fmt.Sprintf("wanted %d ready replicas, got %d", shard.Replicas, got), nil
			}},
			e2eutil.WithInterval(10*time.Second),
			e2eutil.WithTimeout(15*time.Minute),
		)

		// A write issued after the restart must still be routed to the shard.
		hc, err := testCtx.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		hcClient, err := testCtx.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())

		marker := createMarkerEvent(ctx, hcClient)
		DeferCleanup(func() {
			if err := hcClient.Delete(ctx, marker); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete marker Event %s", marker.Name)
			}
		})

		markerKey := e2eutil.EtcdResourceKeyPrefix("", "events") + marker.Namespace + "/" + marker.Name
		Eventually(func() ([]string, error) {
			return e2eutil.EtcdKeysWithPrefix(ctx, testCtx.MgmtClient, cpNamespace, stsName, markerKey)
		}).WithTimeout(5*time.Minute).WithPolling(10*time.Second).
			Should(ContainElement(markerKey), "events written after the restart must land in the %s shard", stsName)
	})
}

// EtcdShardImmutabilityTest verifies the shard list cannot be grown, shrunk,
// renamed, or re-pointed at different resources after creation. There is no
// mechanism to migrate existing keys between shards, so sharding is a
// create-time only decision.
func EtcdShardImmutabilityTest(getTestCtx internal.TestContextGetter) {
	It("should reject changes to the shard list after creation", func() {
		testCtx := getTestCtx()
		etcdShardsOrSkip(testCtx)

		expectShardUpdateRejected(testCtx, "shards cannot be added or removed after creation", func(hc *hyperv1.HostedCluster) {
			hc.Spec.Etcd.Managed.Shards = append(hc.Spec.Etcd.Managed.Shards, hyperv1.ManagedEtcdShardSpec{
				Name:      "added",
				Replicas:  3,
				Resources: []hyperv1.EtcdShardResource{{APIGroup: ptr.To(""), Resource: "pods"}},
			})
		})

		expectShardUpdateRejected(testCtx, "shards cannot be added or removed after creation", func(hc *hyperv1.HostedCluster) {
			hc.Spec.Etcd.Managed.Shards = hc.Spec.Etcd.Managed.Shards[:len(hc.Spec.Etcd.Managed.Shards)-1]
		})

		expectShardUpdateRejected(testCtx, "existing shards cannot be replaced", func(hc *hyperv1.HostedCluster) {
			hc.Spec.Etcd.Managed.Shards[0].Name += "-renamed"
		})

		expectShardUpdateRejected(testCtx, "resources are immutable", func(hc *hyperv1.HostedCluster) {
			hc.Spec.Etcd.Managed.Shards[0].Resources = []hyperv1.EtcdShardResource{{APIGroup: ptr.To(""), Resource: "pods"}}
		})

		expectShardUpdateRejected(testCtx, "replicas is immutable", func(hc *hyperv1.HostedCluster) {
			if hc.Spec.Etcd.Managed.Shards[0].Replicas == 3 {
				hc.Spec.Etcd.Managed.Shards[0].Replicas = 1
			} else {
				hc.Spec.Etcd.Managed.Shards[0].Replicas = 3
			}
		})
	})
}

// etcdShardsOrSkip returns the shards configured on the hosted cluster and
// skips the spec when the cluster was not created with etcd sharding.
func etcdShardsOrSkip(testCtx *internal.TestContext) []hyperv1.ManagedEtcdShardSpec {
	GinkgoHelper()

	hc, err := testCtx.GetHostedCluster()
	Expect(err).NotTo(HaveOccurred())
	if hc.Spec.Etcd.ManagementType != hyperv1.Managed || hc.Spec.Etcd.Managed == nil || len(hc.Spec.Etcd.Managed.Shards) == 0 {
		Skip("hosted cluster was not created with etcd sharding")
	}
	return hc.Spec.Etcd.Managed.Shards
}

// requireEtcdShard returns the named shard, skipping the spec when the hosted
// cluster does not define it.
func requireEtcdShard(shards []hyperv1.ManagedEtcdShardSpec, name string) hyperv1.ManagedEtcdShardSpec {
	GinkgoHelper()

	for _, shard := range shards {
		if shard.Name == name {
			return shard
		}
	}
	Skip(fmt.Sprintf("hosted cluster has no %q etcd shard", name))
	return hyperv1.ManagedEtcdShardSpec{}
}

// createMarkerEvent creates an Event in the hosted cluster's default namespace
// so its etcd key can be looked up by name.
func createMarkerEvent(ctx context.Context, client crclient.Client) *corev1.Event {
	GinkgoHelper()

	now := metav1.Now()
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      e2eutil.SimpleNameGenerator.GenerateName("etcd-sharding-marker-"),
		},
		InvolvedObject: corev1.ObjectReference{Kind: "Namespace", Name: "default", APIVersion: "v1"},
		Reason:         "EtcdShardingE2E",
		Message:        "marker event asserting events are routed to the events etcd shard",
		Type:           corev1.EventTypeNormal,
		Source:         corev1.EventSource{Component: "hypershift-e2e"},
		FirstTimestamp: now,
		LastTimestamp:  now,
		Count:          1,
	}
	Expect(client.Create(ctx, event)).To(Succeed(), "failed to create marker event in the hosted cluster")
	GinkgoWriter.Printf("Created marker Event %s/%s\n", event.Namespace, event.Name)
	return event
}

// expectShardUpdateRejected applies mutate to a fresh copy of the
// HostedCluster and asserts the API server rejects it with expectedMessage.
func expectShardUpdateRejected(testCtx *internal.TestContext, expectedMessage string, mutate func(*hyperv1.HostedCluster)) {
	GinkgoHelper()

	hc, err := testCtx.GetHostedCluster()
	Expect(err).NotTo(HaveOccurred())
	mutate(hc)
	err = testCtx.MgmtClient.Update(testCtx.Context, hc)
	Expect(err).To(HaveOccurred(), "expected the update to be rejected with %q", expectedMessage)
	Expect(err.Error()).To(ContainSubstring(expectedMessage))
}
