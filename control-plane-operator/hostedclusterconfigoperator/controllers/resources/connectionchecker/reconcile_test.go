package connectionchecker

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileDaemonSet(t *testing.T) {
	tests := []struct {
		name     string
		kasIP    string
		kasPort  int32
		endpoint string
		validate func(*testing.T, *appsv1.DaemonSet)
	}{
		{
			name:     "When...DaemonSet is reconciled with IPv4 address...it should configure correctly",
			kasIP:    "172.20.0.1",
			kasPort:  6443,
			endpoint: "/version",
			validate: func(t *testing.T, ds *appsv1.DaemonSet) {
				g := NewWithT(t)

				// Verify basic metadata
				g.Expect(ds.Name).To(Equal(manifests.KASConnectionCheckerDSName))
				g.Expect(ds.Namespace).To(Equal(manifests.KASConnectionCheckerNamespace))

				// Verify selector and labels
				g.Expect(ds.Spec.Selector.MatchLabels).To(HaveKeyWithValue("app", "kas-connection-checker"))
				g.Expect(ds.Spec.Template.Labels).To(HaveKeyWithValue("app", "kas-connection-checker"))

				// Verify pod spec
				podSpec := ds.Spec.Template.Spec
				g.Expect(*podSpec.AutomountServiceAccountToken).To(BeFalse())
				g.Expect(podSpec.HostNetwork).To(BeTrue())
				g.Expect(podSpec.DNSPolicy).To(Equal(corev1.DNSClusterFirstWithHostNet))
				g.Expect(podSpec.PriorityClassName).To(Equal(systemNodeCriticalPriority))

				// Verify tolerations (should tolerate everything)
				g.Expect(podSpec.Tolerations).To(HaveLen(1))
				g.Expect(podSpec.Tolerations[0].Operator).To(Equal(corev1.TolerationOpExists))

				// Verify container
				g.Expect(podSpec.Containers).To(HaveLen(1))
				container := podSpec.Containers[0]
				g.Expect(container.Name).To(Equal("kas-connection-checker"))
				g.Expect(container.Image).To(Equal(pauseImage))
				g.Expect(container.ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))

				// Verify readiness probe
				g.Expect(container.ReadinessProbe).NotTo(BeNil())
				httpGet := container.ReadinessProbe.HTTPGet
				g.Expect(httpGet).NotTo(BeNil())
				g.Expect(httpGet.Scheme).To(Equal(corev1.URISchemeHTTPS))
				g.Expect(httpGet.Host).To(Equal("172.20.0.1"))
				g.Expect(httpGet.Port).To(Equal(intstr.FromInt(6443)))
				g.Expect(httpGet.Path).To(Equal("/version"))

				// Verify probe timing
				g.Expect(container.ReadinessProbe.InitialDelaySeconds).To(Equal(int32(5)))
				g.Expect(container.ReadinessProbe.TimeoutSeconds).To(Equal(int32(10)))
				g.Expect(container.ReadinessProbe.PeriodSeconds).To(Equal(int32(10)))
				g.Expect(container.ReadinessProbe.SuccessThreshold).To(Equal(int32(1)))
				g.Expect(container.ReadinessProbe.FailureThreshold).To(Equal(int32(3)))

				// Verify resource requests
				g.Expect(container.Resources.Requests).To(HaveKey(corev1.ResourceMemory))
				g.Expect(container.Resources.Requests).To(HaveKey(corev1.ResourceCPU))
			},
		},
		{
			name:     "When...DaemonSet is reconciled with IPv6 address...it should configure correctly",
			kasIP:    "fd00::1",
			kasPort:  6443,
			endpoint: "/version",
			validate: func(t *testing.T, ds *appsv1.DaemonSet) {
				g := NewWithT(t)

				// Verify readiness probe uses IPv6 address
				container := ds.Spec.Template.Spec.Containers[0]
				g.Expect(container.ReadinessProbe).NotTo(BeNil())
				httpGet := container.ReadinessProbe.HTTPGet
				g.Expect(httpGet.Host).To(Equal("fd00::1"))
				g.Expect(httpGet.Port).To(Equal(intstr.FromInt(6443)))
			},
		},
		{
			name:     "When...DaemonSet is reconciled with IBM Cloud endpoint...it should use livez endpoint",
			kasIP:    "172.20.0.1",
			kasPort:  6443,
			endpoint: "/livez?exclude=etcd&exclude=log",
			validate: func(t *testing.T, ds *appsv1.DaemonSet) {
				g := NewWithT(t)

				// Verify readiness probe uses IBM Cloud specific endpoint
				container := ds.Spec.Template.Spec.Containers[0]
				g.Expect(container.ReadinessProbe).NotTo(BeNil())
				httpGet := container.ReadinessProbe.HTTPGet
				g.Expect(httpGet.Path).To(Equal("/livez?exclude=etcd&exclude=log"))
			},
		},
		{
			name:     "When...DaemonSet is reconciled with custom port...it should configure correctly",
			kasIP:    "172.20.0.1",
			kasPort:  8443,
			endpoint: "/version",
			validate: func(t *testing.T, ds *appsv1.DaemonSet) {
				g := NewWithT(t)

				// Verify readiness probe uses custom port
				container := ds.Spec.Template.Spec.Containers[0]
				g.Expect(container.ReadinessProbe).NotTo(BeNil())
				httpGet := container.ReadinessProbe.HTTPGet
				g.Expect(httpGet.Port).To(Equal(intstr.FromInt(8443)))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()

			// Create fake client
			scheme := runtime.NewScheme()
			g.Expect(appsv1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			// Create DaemonSet manifest
			daemonSet := manifests.KASConnectionCheckerDaemonSet()

			// Reconcile DaemonSet
			createOrUpdate := upsert.New(false).CreateOrUpdate
			err := ReconcileDaemonSet(
				ctx,
				daemonSet,
				tt.kasIP,
				tt.kasPort,
				tt.endpoint,
				createOrUpdate,
				fakeClient,
			)

			// Verify no error
			g.Expect(err).NotTo(HaveOccurred())

			// Run custom validation
			tt.validate(t, daemonSet)
		})
	}
}
