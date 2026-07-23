package etcd

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildEtcdInitContainer(t *testing.T) {
	t.Parallel()

	const (
		testNamespace      = "test-hcp-namespace"
		testInitialCluster = "etcd-0=https://etcd-0.etcd-discovery.test-hcp-namespace.svc:2380,etcd-1=https://etcd-1.etcd-discovery.test-hcp-namespace.svc:2380,etcd-2=https://etcd-2.etcd-discovery.test-hcp-namespace.svc:2380"
	)

	testCases := []struct {
		name       string
		restoreUrl string
		validate   func(g Gomega, c corev1.Container)
	}{
		{
			name:       "When restoreUrl is provided, it should set RESTORE_URL_ETCD env var to that URL",
			restoreUrl: "https://etcd-backup-bucket.s3.us-east-1.amazonaws.com/backups/etcd-snapshot-2024-01-15.db",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Env).To(ContainElement(corev1.EnvVar{
					Name:  "RESTORE_URL_ETCD",
					Value: "https://etcd-backup-bucket.s3.us-east-1.amazonaws.com/backups/etcd-snapshot-2024-01-15.db",
				}))
			},
		},
		{
			name:       "When called, it should set HOSTNAME from pod metadata.name via downward API",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Env).To(ContainElement(corev1.EnvVar{
					Name: "HOSTNAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
					},
				}))
			},
		},
		{
			name:       "When called, it should set HCP_NAMESPACE env var",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Env).To(ContainElement(corev1.EnvVar{
					Name:  "HCP_NAMESPACE",
					Value: testNamespace,
				}))
			},
		},
		{
			name:       "When called, it should set ETCD_INITIAL_CLUSTER env var",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Env).To(ContainElement(corev1.EnvVar{
					Name:  "ETCD_INITIAL_CLUSTER",
					Value: testInitialCluster,
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
			c := buildEtcdInitContainer(tc.restoreUrl, testNamespace, testInitialCluster)
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

	baseInitContainers := []corev1.Container{
		{Name: "ensure-dns"},
		{Name: "reset-member"},
	}

	testCases := []struct {
		name               string
		restoreURL         string
		snapshotRestored   bool
		baseInitContainers []corev1.Container
		expectedOrder      []string
	}{
		{
			name:          "When restoreSnapshotURL is set, it should place etcd-init before reset-member",
			restoreURL:    "https://etcd-backup-bucket.s3.us-east-1.amazonaws.com/backups/etcd-snapshot-2024-01-15.db",
			expectedOrder: []string{"ensure-dns", "etcd-init", "reset-member"},
		},
		{
			name:          "When restoreSnapshotURL is not set, it should not add etcd-init",
			expectedOrder: []string{"ensure-dns", "reset-member"},
		},
		{
			name:             "When EtcdSnapshotRestored condition is true, it should not add etcd-init even if restoreSnapshotURL is set",
			restoreURL:       "https://etcd-backup-bucket.s3.us-east-1.amazonaws.com/backups/etcd-snapshot-2024-01-15.db",
			snapshotRestored: true,
			expectedOrder:    []string{"ensure-dns", "reset-member"},
		},
		{
			name:               "When reset-member is absent, it should append etcd-init at the end",
			restoreURL:         "https://etcd-backup-bucket.s3.us-east-1.amazonaws.com/backups/etcd-snapshot-2024-01-15.db",
			baseInitContainers: []corev1.Container{{Name: "ensure-dns"}},
			expectedOrder:      []string{"ensure-dns", "etcd-init"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed: &hyperv1.ManagedEtcdSpec{
							Storage: hyperv1.ManagedEtcdStorageSpec{
								Type: hyperv1.PersistentVolumeEtcdStorage,
							},
						},
					},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.0.0.0/16")},
						},
					},
					ControllerAvailabilityPolicy: hyperv1.SingleReplica,
				},
			}
			if tc.restoreURL != "" {
				hcp.Spec.Etcd.Managed.Storage.RestoreSnapshotURL = []string{tc.restoreURL}
			}
			if tc.snapshotRestored {
				hcp.Status.Conditions = []metav1.Condition{
					{
						Type:   string(hyperv1.EtcdSnapshotRestored),
						Status: metav1.ConditionTrue,
					},
				}
			}

			src := tc.baseInitContainers
			if src == nil {
				src = baseInitContainers
			}
			containers := make([]corev1.Container, len(src))
			copy(containers, src)
			sts := &appsv1.StatefulSet{}
			sts.Spec.Template.Spec.InitContainers = containers
			sts.Spec.Template.Spec.Containers = []corev1.Container{{Name: ComponentName}, {Name: "etcd-metrics"}}

			err := adaptStatefulSet(component.WorkloadContext{Context: context.Background(), HCP: hcp}, sts)
			g.Expect(err).ToNot(HaveOccurred())

			names := make([]string, len(sts.Spec.Template.Spec.InitContainers))
			for i, c := range sts.Spec.Template.Spec.InitContainers {
				names[i] = c.Name
			}
			g.Expect(names).To(Equal(tc.expectedOrder))
		})
	}
}
