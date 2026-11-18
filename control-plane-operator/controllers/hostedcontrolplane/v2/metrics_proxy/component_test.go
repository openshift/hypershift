package metricsproxy

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	endpointresolverv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/endpoint_resolver"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	metricsproxybin "github.com/openshift/hypershift/control-plane-operator/metrics-proxy"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/yaml"
)

const configHashAnnotation = "component.hypershift.openshift.io/config-hash"

// TestNewComponent reconciles the metrics-proxy component end to end. It uses
// the SRE metrics set configuration shared by the package and must not run in
// parallel.
func TestNewComponent(t *testing.T) {
	namespace := "clusters-test"
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: namespace},
		Spec: hyperv1.HostedControlPlaneSpec{
			ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.22.0-x86_64",
			Platform:     hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
			Monitoring: hyperv1.MonitoringSpec{MetricsForwarding: hyperv1.MetricsForwardingSpec{
				Mode: hyperv1.MetricsForwardingModeForward,
				// Forward the SRE set while the control plane collects the Telemetry set.
				MetricsSet: hyperv1.MetricsSetSRE,
			}},
		},
	}
	imageProvider := testutil.FakeImageProvider(testutil.WithVersion("4.22.0"))
	availableComponent := func(name string) *hyperv1.ControlPlaneComponent {
		return &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Status: hyperv1.ControlPlaneComponentStatus{
				Version: imageProvider.Version(),
				Conditions: []metav1.Condition{
					{Type: string(hyperv1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
					{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue},
				},
			},
		}
	}
	serviceMonitor := newServiceMonitor("openshift-apiserver", namespace, "openshift-apiserver", "https", "https", "openshift-apiserver",
		"root-ca", "ca.crt", "metrics-client", "tls.crt", "metrics-client", "tls.key")
	service := newService("openshift-apiserver", namespace, "openshift-apiserver", "https", 8443)

	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).
		WithObjects(availableComponent(endpointresolverv2.ComponentName), availableComponent("kube-apiserver"), serviceMonitor, service).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				// Match the real client's preservation of a typed object's GVK on Get.
				gvk := obj.GetObjectKind().GroupVersionKind()
				defer obj.GetObjectKind().SetGroupVersionKind(gvk)
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
	cpContext := component.ControlPlaneContext{
		Context:                  t.Context(),
		Client:                   fakeClient,
		HCP:                      hcp,
		ApplyProvider:            upsert.NewApplyProvider(true),
		ReleaseImageProvider:     imageProvider,
		UserReleaseImageProvider: imageProvider,
		MetricsSet:               metrics.MetricsSetTelemetry,
		SkipCertificateSigning:   true,
	}

	reconcile := func(g Gomega) (metricsproxybin.ComponentFileConfig, string) {
		g.Expect(NewComponent("apps.example.com").Reconcile(cpContext)).To(Succeed())

		cm := &corev1.ConfigMap{}
		g.Expect(fakeClient.Get(t.Context(), client.ObjectKey{Namespace: namespace, Name: "metrics-proxy-config"}, cm)).To(Succeed())
		var cfg metricsproxybin.FileConfig
		g.Expect(yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg)).To(Succeed())
		oas, ok := findComponent(cfg.Components, "openshift-apiserver")
		g.Expect(ok).To(BeTrue(), "openshift-apiserver not found in %s", cm.Data["config.yaml"])

		deployment := &appsv1.Deployment{}
		g.Expect(fakeClient.Get(t.Context(), client.ObjectKey{Namespace: namespace, Name: ComponentName}, deployment)).To(Succeed())
		g.Expect(deployment.Spec.Template.Spec.Containers[0].Args).To(ContainElements("--metrics-set", "SRE"))
		hash := deployment.Spec.Template.Annotations[configHashAnnotation]
		g.Expect(hash).NotTo(BeEmpty())
		return oas, hash
	}

	t.Run("When the forwarding metrics set is SRE without SRE collection, it should serialize the sre-metric-set rules", func(t *testing.T) {
		loadSREMetricsSetConfig(t, `
openshiftAPIServer:
  - action: keep
    sourceLabels: ["__name__"]
    regex: "apiserver_request_total"
`)
		g := NewGomegaWithT(t)
		oas, firstHash := reconcile(g)
		g.Expect(oas.MetricsFilter).To(Equal(metricsproxybin.MetricsFilterRelabel))
		g.Expect(oas.MetricRelabelConfigs).To(HaveLen(1))
		g.Expect(oas.MetricRelabelConfigs[0].Regex).To(Equal("apiserver_request_total"))

		t.Run("When the configuration is reconciled again unchanged, it should keep the config hash", func(t *testing.T) {
			g := NewGomegaWithT(t)
			_, hash := reconcile(g)
			g.Expect(hash).To(Equal(firstHash))
		})

		t.Run("When the sre-metric-set configuration changes, it should update the scrape config and roll out the deployment", func(t *testing.T) {
			loadSREMetricsSetConfig(t, `
openshiftAPIServer:
  - action: keep
    sourceLabels: ["__name__"]
    regex: "apiserver_request_total|apiserver_current_inflight_requests"
`)
			g := NewGomegaWithT(t)
			oas, hash := reconcile(g)
			g.Expect(oas.MetricRelabelConfigs).To(HaveLen(1))
			g.Expect(oas.MetricRelabelConfigs[0].Regex).To(Equal("apiserver_request_total|apiserver_current_inflight_requests"))
			g.Expect(hash).NotTo(Equal(firstHash))
		})

		t.Run("When the sre-metric-set configuration drops the component's rules, it should forward no samples for it", func(t *testing.T) {
			loadSREMetricsSetConfig(t, "{}")
			g := NewGomegaWithT(t)
			oas, hash := reconcile(g)
			g.Expect(oas.MetricsFilter).To(Equal(metricsproxybin.MetricsFilterRelabel))
			g.Expect(oas.MetricRelabelConfigs).To(BeEmpty())
			g.Expect(hash).NotTo(Equal(firstHash))
		})
	})
}
