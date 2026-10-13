package etcd

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
