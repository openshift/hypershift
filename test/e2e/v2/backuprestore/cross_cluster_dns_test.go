//go:build e2ev2 && backuprestore

package backuprestore

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestScaleDownExternalDNS(t *testing.T) {
	t.Run("When ExternalDNS Deployment has replicas, it should scale the Deployment to zero", func(t *testing.T) {
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: externalDNSNamespace, Name: externalDNSDeploymentName},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To[int32](1),
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "external-dns"}},
			},
		}
		client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(deployment).Build()
		testCtx := &internal.TestContext{Context: t.Context(), MgmtClient: client}

		g := NewWithT(t)
		g.Expect(ScaleDownExternalDNS(testCtx)).To(Succeed())

		updated := &appsv1.Deployment{}
		g.Expect(client.Get(t.Context(), types.NamespacedName{Namespace: externalDNSNamespace, Name: externalDNSDeploymentName}, updated)).To(Succeed())
		g.Expect(updated.Spec.Replicas).NotTo(BeNil())
		g.Expect(*updated.Spec.Replicas).To(BeZero())
	})

	t.Run("When ExternalDNS Deployment is missing, it should return an error", func(t *testing.T) {
		client := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		testCtx := &internal.TestContext{Context: t.Context(), MgmtClient: client}
		NewWithT(t).Expect(ScaleDownExternalDNS(testCtx)).To(MatchError(ContainSubstring("failed to get ExternalDNS Deployment")))
	})
}

func TestScaleUpExternalDNS(t *testing.T) {
	t.Run("When ExternalDNS has a ready pod, it should scale the Deployment to one", func(t *testing.T) {
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: externalDNSNamespace, Name: externalDNSDeploymentName},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To[int32](0),
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "external-dns"}},
			},
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: externalDNSNamespace, Name: "external-dns-ready", Labels: map[string]string{"app": "external-dns"}},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		}
		client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(deployment, pod).Build()
		testCtx := &internal.TestContext{Context: t.Context(), MgmtClient: client}

		g := NewWithT(t)
		g.Expect(ScaleUpExternalDNS(testCtx)).To(Succeed())

		updated := &appsv1.Deployment{}
		g.Expect(client.Get(t.Context(), types.NamespacedName{Namespace: externalDNSNamespace, Name: externalDNSDeploymentName}, updated)).To(Succeed())
		g.Expect(updated.Spec.Replicas).NotTo(BeNil())
		g.Expect(*updated.Spec.Replicas).To(Equal(int32(1)))
	})

	t.Run("When the ExternalDNS pod is not ready, it should return an error after waiting", func(t *testing.T) {
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: externalDNSNamespace, Name: externalDNSDeploymentName},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To[int32](0),
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "external-dns"}},
			},
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: externalDNSNamespace, Name: "external-dns-unready", Labels: map[string]string{"app": "external-dns"}},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}},
			},
		}
		client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(deployment, pod).Build()
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		testCtx := &internal.TestContext{Context: ctx, MgmtClient: client}

		NewWithT(t).Expect(ScaleUpExternalDNS(testCtx)).To(MatchError(ContainSubstring("did not reach 1 ready replicas")))
	})

	t.Run("When ExternalDNS Deployment is missing, it should return an error", func(t *testing.T) {
		client := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		testCtx := &internal.TestContext{Context: t.Context(), MgmtClient: client}
		NewWithT(t).Expect(ScaleUpExternalDNS(testCtx)).To(MatchError(ContainSubstring("failed to get ExternalDNS Deployment")))
	})
}

func TestPublicServiceRouteEndpoints(t *testing.T) {
	t.Run("When AWS service routes have explicit hostnames, it should return their public Route endpoints", func(t *testing.T) {
		hostedCluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "restore-cluster", Namespace: "clusters"},
			Spec: hyperv1.HostedClusterSpec{
				Services: []hyperv1.ServicePublishingStrategyMapping{
					{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route, Route: &hyperv1.RoutePublishingStrategy{Hostname: "api-restore.example.com"}}},
					{Service: hyperv1.OAuthServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route, Route: &hyperv1.RoutePublishingStrategy{Hostname: "oauth-restore.example.com"}}},
					{Service: hyperv1.Konnectivity, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route, Route: &hyperv1.RoutePublishingStrategy{Hostname: "konnectivity-restore.example.com"}}},
				},
			},
		}

		endpoints, err := PublicServiceRouteEndpoints(hostedCluster)
		g := NewWithT(t)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(endpoints).To(ConsistOf(
			PublicServiceRouteEndpoint{Service: hyperv1.APIServer, DNSName: "api-restore.example.com", RouteName: "kube-apiserver"},
			PublicServiceRouteEndpoint{Service: hyperv1.OAuthServer, DNSName: "oauth-restore.example.com", RouteName: "oauth"},
			PublicServiceRouteEndpoint{Service: hyperv1.Konnectivity, DNSName: "konnectivity-restore.example.com", RouteName: "konnectivity-server"},
		))
	})

	t.Run("When an API or OAuth Route hostname is missing, it should return an error", func(t *testing.T) {
		hostedCluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "restore-cluster", Namespace: "clusters"},
			Spec: hyperv1.HostedClusterSpec{Services: []hyperv1.ServicePublishingStrategyMapping{
				{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route, Route: &hyperv1.RoutePublishingStrategy{Hostname: "api-restore.example.com"}}},
			}},
		}
		_, err := PublicServiceRouteEndpoints(hostedCluster)
		NewWithT(t).Expect(err).To(MatchError(ContainSubstring("OAuthServer")))
	})
}
