package etcd

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestBuildEtcdInitContainer(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		restoreUrl string
		validate   func(g Gomega, c corev1.Container)
	}{
		{
			name:       "When restoreUrl is provided, it should set RESTORE_URL_ETCD env var to that URL",
			restoreUrl: "https://example.com/snapshot.db",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Env).To(ContainElement(corev1.EnvVar{
					Name:  "RESTORE_URL_ETCD",
					Value: "https://example.com/snapshot.db",
				}))
			},
		},
		{
			name:       "When called, it should set the container name to etcd-init",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Name).To(Equal("etcd-init"))
			},
		},
		{
			name:       "When called, it should set image to etcd",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Image).To(Equal("etcd"))
			},
		},
		{
			name:       "When called, it should set ImagePullPolicy to PullIfNotPresent",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))
			},
		},
		{
			name:       "When called, it should mount /var/lib volume named data",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.VolumeMounts).To(ContainElement(corev1.VolumeMount{
					Name:      "data",
					MountPath: "/var/lib",
				}))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			c := buildEtcdInitContainer(tc.restoreUrl)
			tc.validate(g, c)
		})
	}
}

func TestBuildEtcdDefragControllerContainer(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		namespace string
		validate  func(g Gomega, c corev1.Container)
	}{
		{
			name:      "When called with a namespace, it should pass the namespace as an arg",
			namespace: "test-namespace",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Args).To(Equal([]string{
					"etcd-defrag-controller",
					"--namespace",
					"test-namespace",
				}))
			},
		},
		{
			name:      "When called, it should set container name to etcd-defrag",
			namespace: "any-namespace",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Name).To(Equal("etcd-defrag"))
			},
		},
		{
			name:      "When called, it should mount client-tls and etcd-ca volumes",
			namespace: "any-namespace",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.VolumeMounts).To(ConsistOf(
					corev1.VolumeMount{
						Name:      "client-tls",
						MountPath: "/etc/etcd/tls/client",
					},
					corev1.VolumeMount{
						Name:      "etcd-ca",
						MountPath: "/etc/etcd/tls/etcd-ca",
					},
				))
			},
		},
		{
			name:      "When called, it should set resource requests for CPU and memory",
			namespace: "any-namespace",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceCPU, resource.MustParse("10m")))
				g.Expect(c.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceMemory, resource.MustParse("50Mi")))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			c := buildEtcdDefragControllerContainer(tc.namespace)
			tc.validate(g, c)
		})
	}
}

func TestIsManagedETCD(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		managementType hyperv1.EtcdManagementType
		expected       bool
	}{
		{
			name:           "When etcd management type is Managed, it should return true",
			managementType: hyperv1.Managed,
			expected:       true,
		},
		{
			name:           "When etcd management type is Unmanaged, it should return false",
			managementType: hyperv1.Unmanaged,
			expected:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Etcd: hyperv1.EtcdSpec{
							ManagementType: tc.managementType,
						},
					},
				},
			}

			result, err := isManagedETCD(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestDefragControllerPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name               string
		availabilityPolicy hyperv1.AvailabilityPolicy
		expected           bool
	}{
		{
			name:               "When availability policy is HighlyAvailable, it should return true",
			availabilityPolicy: hyperv1.HighlyAvailable,
			expected:           true,
		},
		{
			name:               "When availability policy is SingleReplica, it should return false",
			availabilityPolicy: hyperv1.SingleReplica,
			expected:           false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						ControllerAvailabilityPolicy: tc.availabilityPolicy,
					},
				},
			}

			result := defragControllerPredicate(cpContext)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestAdaptStatefulSet(t *testing.T) {
	t.Parallel()

	const ns = "test-ns"

	baseHCP := func(conditions ...metav1.Condition) *hyperv1.HostedControlPlane {
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: ns,
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Networking: hyperv1.ClusterNetworking{
					ClusterNetwork: []hyperv1.ClusterNetworkEntry{
						{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")},
					},
				},
				Etcd: hyperv1.EtcdSpec{
					ManagementType: hyperv1.Managed,
					Managed: &hyperv1.ManagedEtcdSpec{
						AutomatedBackup: &hyperv1.AutomatedEtcdBackupConfig{
							Storage: hyperv1.AutomatedEtcdBackupStorage{
								Type: hyperv1.AutomatedEtcdBackupStorageTypeGCS,
								GCS: &hyperv1.AutomatedEtcdBackupGCS{
									Bucket:            "my-bucket",
									GCPServiceAccount: "sa1234@proj.iam.gserviceaccount.com",
								},
							},
						},
						Storage: hyperv1.ManagedEtcdStorageSpec{
							Type: hyperv1.PersistentVolumeEtcdStorage,
							PersistentVolume: &hyperv1.PersistentVolumeEtcdStorageSpec{
								Size: ptrQuantity("8Gi"),
							},
						},
					},
				},
				ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
				InfraID:                      "test-infra",
			},
		}
		hcp.Status.Conditions = conditions
		return hcp
	}

	baseSts := func() *appsv1.StatefulSet {
		return &appsv1.StatefulSet{
			Spec: appsv1.StatefulSetSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: ComponentName},
							{Name: "etcd-metrics"},
						},
						InitContainers: []corev1.Container{
							{Name: "reset-member"},
						},
					},
				},
				VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
					{Spec: corev1.PersistentVolumeClaimSpec{}},
				},
			},
		}
	}

	testCases := []struct {
		name                   string
		conditions             []metav1.Condition
		completedJob           bool
		expectRestoreContainer bool
		expectReplicasOne      bool
	}{
		{
			name: "When EtcdSnapshotRestored is true it should not inject restore init container or override replicas",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.EtcdSnapshotRestored),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			completedJob:           false,
			expectRestoreContainer: false,
			expectReplicasOne:      false,
		},
		{
			name:                   "When EtcdSnapshotRestored is false and restore job completed it should inject restore init container and set replicas to 1",
			conditions:             nil,
			completedJob:           true,
			expectRestoreContainer: true,
			expectReplicasOne:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := baseHCP(tc.conditions...)
			sts := baseSts()

			scheme := runtime.NewScheme()
			_ = batchv1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tc.completedJob {
				job := manifests.EtcdRestoreJob(ns)
				job.CreationTimestamp = metav1.Now()
				job.Status.Conditions = []batchv1.JobCondition{
					{
						Type:   batchv1.JobComplete,
						Status: corev1.ConditionTrue,
					},
				}
				builder = builder.WithObjects(job)
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				Client:  builder.Build(),
				HCP:     hcp,
			}

			err := adaptStatefulSet(cpContext, sts)
			g.Expect(err).ToNot(HaveOccurred())

			hasRestoreContainer := false
			for _, c := range sts.Spec.Template.Spec.InitContainers {
				if c.Name == "etcd-restore" {
					hasRestoreContainer = true
					break
				}
			}
			g.Expect(hasRestoreContainer).To(Equal(tc.expectRestoreContainer),
				"etcd-restore init container presence mismatch")

			if tc.expectReplicasOne {
				g.Expect(sts.Spec.Replicas).ToNot(BeNil())
				g.Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
			} else {
				g.Expect(sts.Spec.Replicas).To(BeNil(),
					"replicas should not be overridden when snapshot is restored")
			}
		})
	}
}

func ptrQuantity(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}
