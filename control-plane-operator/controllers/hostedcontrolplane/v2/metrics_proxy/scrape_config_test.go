package metricsproxy

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metricsproxybin "github.com/openshift/hypershift/control-plane-operator/metrics-proxy"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func newServiceMonitor(name, namespace, svcLabel, portOrTargetPort, scheme, serverName, caConfigMapName, caKey, certSecretName, certKey, keySecretName, keyKey string) *prometheusoperatorv1.ServiceMonitor {
	ep := prometheusoperatorv1.Endpoint{
		Scheme: (*prometheusoperatorv1.Scheme)(ptr.To(scheme)),
	}

	// Use Port if it looks like a named port, otherwise use TargetPort.
	tp := intstr.FromString(portOrTargetPort)
	if tp.IntValue() > 0 {
		ep.TargetPort = &tp
	} else {
		ep.Port = portOrTargetPort
	}

	tlsCfg := &prometheusoperatorv1.TLSConfig{}
	tlsCfg.ServerName = ptr.To(serverName)

	if caConfigMapName != "" {
		tlsCfg.CA = prometheusoperatorv1.SecretOrConfigMap{
			ConfigMap: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: caConfigMapName},
				Key:                  caKey,
			},
		}
	}
	if certSecretName != "" {
		tlsCfg.Cert = prometheusoperatorv1.SecretOrConfigMap{
			Secret: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: certSecretName},
				Key:                  certKey,
			},
		}
		tlsCfg.KeySecret = &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: keySecretName},
			Key:                  keyKey,
		}
	}

	ep.TLSConfig = tlsCfg

	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: prometheusoperatorv1.ServiceMonitorSpec{
			Endpoints: []prometheusoperatorv1.Endpoint{ep},
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": svcLabel},
			},
		},
	}
}

func newService(name, namespace, appLabel, portName string, portNum int32) *corev1.Service {
	return newServiceWithTargetPort(name, namespace, appLabel, portName, portNum, intstr.FromInt32(portNum))
}

func newServiceWithTargetPort(name, namespace, appLabel, portName string, portNum int32, targetPort intstr.IntOrString) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": appLabel},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": appLabel},
			Ports: []corev1.ServicePort{
				{Name: portName, Port: portNum, TargetPort: targetPort},
			},
		},
	}
}

func newPodMonitor(name, namespace, portName, scheme string, tlsCfg *prometheusoperatorv1.SafeTLSConfig) *prometheusoperatorv1.PodMonitor {
	ep := prometheusoperatorv1.PodMetricsEndpoint{
		Port:   ptr.To(portName),
		Scheme: (*prometheusoperatorv1.Scheme)(ptr.To(scheme)),
	}
	if tlsCfg != nil {
		ep.TLSConfig = tlsCfg
	}
	return &prometheusoperatorv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: prometheusoperatorv1.PodMonitorSpec{
			PodMetricsEndpoints: []prometheusoperatorv1.PodMetricsEndpoint{ep},
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
		},
	}
}

func newPod(name, namespace, portName string, portNum int32) *corev1.Pod {
	return newPodWithLabels(name, namespace, portName, portNum, map[string]string{"app": name})
}

func newPodWithLabels(name, namespace, portName string, portNum int32, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: name,
					Ports: []corev1.ContainerPort{
						{Name: portName, ContainerPort: portNum},
					},
				},
			},
		},
	}
}

func findComponent(components []metricsproxybin.ComponentFileConfig, name string) (metricsproxybin.ComponentFileConfig, bool) {
	for _, c := range components {
		if c.Name == name {
			return c, true
		}
	}
	return metricsproxybin.ComponentFileConfig{}, false
}

func TestAdaptScrapeConfig(t *testing.T) {
	g := NewGomegaWithT(t)
	t.Parallel()

	namespace := "clusters-test-hosted"

	serviceMonitors := []runtime.Object{
		newServiceMonitor("kube-apiserver", namespace, "kube-apiserver", "client", "https", "kube-apiserver",
			"root-ca", "ca.crt", "metrics-client", "tls.crt", "metrics-client", "tls.key"),
		newServiceMonitor("etcd", namespace, "etcd", "metrics", "https", "etcd-client",
			"etcd-ca", "ca.crt", "etcd-metrics-client-tls", "etcd-client.crt", "etcd-metrics-client-tls", "etcd-client.key"),
		newServiceMonitor("kube-controller-manager", namespace, "kube-controller-manager", "client", "https", "kube-controller-manager",
			"root-ca", "ca.crt", "metrics-client", "tls.crt", "metrics-client", "tls.key"),
		newServiceMonitor("openshift-apiserver", namespace, "openshift-apiserver", "https", "https", "openshift-apiserver",
			"root-ca", "ca.crt", "metrics-client", "tls.crt", "metrics-client", "tls.key"),
		newServiceMonitor("openshift-controller-manager", namespace, "openshift-controller-manager", "https", "https", "openshift-controller-manager",
			"root-ca", "ca.crt", "metrics-client", "tls.crt", "metrics-client", "tls.key"),
		newServiceMonitor("openshift-route-controller-manager", namespace, "openshift-route-controller-manager", "https", "https", "openshift-controller-manager",
			"root-ca", "ca.crt", "metrics-client", "tls.crt", "metrics-client", "tls.key"),
		// CVO has no client cert.
		newServiceMonitor("cluster-version-operator", namespace, "cluster-version-operator", "https", "https", "cluster-version-operator",
			"root-ca", "ca.crt", "", "", "", ""),
		newServiceMonitor("node-tuning-operator", namespace, "node-tuning-operator", "60000", "https", "node-tuning-operator."+namespace+".svc",
			"root-ca", "ca.crt", "metrics-client", "tls.crt", "metrics-client", "tls.key"),
		newServiceMonitor("olm-operator", namespace, "olm-operator", "metrics", "https", "olm-operator-metrics",
			"root-ca", "ca.crt", "metrics-client", "tls.crt", "metrics-client", "tls.key"),
		newServiceMonitor("catalog-operator", namespace, "catalog-operator", "metrics", "https", "catalog-operator-metrics",
			"root-ca", "ca.crt", "metrics-client", "tls.crt", "metrics-client", "tls.key"),
	}

	services := []runtime.Object{
		newService("kube-apiserver", namespace, "kube-apiserver", "client", 6443),
		newService("etcd-client", namespace, "etcd", "metrics", 2381),
		newService("kube-controller-manager", namespace, "kube-controller-manager", "client", 10257),
		newService("openshift-apiserver", namespace, "openshift-apiserver", "https", 8443),
		newService("openshift-controller-manager", namespace, "openshift-controller-manager", "https", 8443),
		newService("openshift-route-controller-manager", namespace, "openshift-route-controller-manager", "https", 8443),
		newService("cluster-version-operator", namespace, "cluster-version-operator", "https", 8443),
		newService("node-tuning-operator", namespace, "node-tuning-operator", "60000", 60000),
		newService("olm-operator-metrics", namespace, "olm-operator", "metrics", 8443),
		newService("catalog-operator-metrics", namespace, "catalog-operator", "metrics", 8443),
	}

	allObjects := append(serviceMonitors, services...)

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	_ = prometheusoperatorv1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(allObjects...).Build()

	cpContext := component.WorkloadContext{
		Context: context.Background(),
		Client:  fakeClient,
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "test",
			},
		},
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "metrics-proxy-config",
		},
	}

	g.Expect(adaptScrapeConfig(cpContext, cm)).To(Succeed())
	g.Expect(cm.Data).To(HaveKey("config.yaml"))

	var cfg metricsproxybin.FileConfig
	g.Expect(yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg)).To(Succeed())

	t.Run("When all ServiceMonitors and services exist, it should include all 10 components", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		g.Expect(cfg.Components).To(HaveLen(10))
	})

	t.Run("When endpoint resolver config is generated, it should have correct URL and CA", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		g.Expect(cfg.EndpointResolver.URL).To(Equal("https://endpoint-resolver." + namespace + ".svc"))
		g.Expect(cfg.EndpointResolver.CAFile).To(Equal(endpointResolverCA))
	})

	t.Run("When kube-apiserver config is generated, it should have correct port, certs and selector", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		kas, ok := findComponent(cfg.Components, "kube-apiserver")
		g.Expect(ok).To(BeTrue(), "kube-apiserver not found in components")
		g.Expect(kas.MetricsPort).To(Equal(int32(6443)))
		g.Expect(kas.CAFile).To(Equal(certBasePath + "/root-ca/ca.crt"))
		g.Expect(kas.CertFile).To(Equal(certBasePath + "/metrics-client/tls.crt"))
		g.Expect(kas.Selector).To(HaveKeyWithValue("app", "kube-apiserver"))
	})

	t.Run("When etcd config is generated, it should use etcd-specific certs", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		etcd, ok := findComponent(cfg.Components, "etcd")
		g.Expect(ok).To(BeTrue(), "etcd not found in components")
		g.Expect(etcd.CAFile).To(Equal(certBasePath + "/etcd-ca/ca.crt"))
		g.Expect(etcd.CertFile).To(Equal(certBasePath + "/etcd-metrics-client-tls/etcd-client.crt"))
		g.Expect(etcd.KeyFile).To(Equal(certBasePath + "/etcd-metrics-client-tls/etcd-client.key"))
	})

	t.Run("When CVO config is generated, it should have no client cert", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		cvo, ok := findComponent(cfg.Components, "cluster-version-operator")
		g.Expect(ok).To(BeTrue(), "cluster-version-operator not found in components")
		g.Expect(cvo.CertFile).To(BeEmpty())
		g.Expect(cvo.KeyFile).To(BeEmpty())
	})

	t.Run("When NTO config is generated, it should include namespace in serverName", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		nto, ok := findComponent(cfg.Components, "node-tuning-operator")
		g.Expect(ok).To(BeTrue(), "node-tuning-operator not found in components")
		g.Expect(nto.TLSServerName).To(Equal("node-tuning-operator." + namespace + ".svc"))
	})
}

func TestAdaptScrapeConfigMissingService(t *testing.T) {
	t.Parallel()

	namespace := "clusters-test-hosted"

	// Only provide kube-apiserver ServiceMonitor and Service.
	objects := []runtime.Object{
		newServiceMonitor("kube-apiserver", namespace, "kube-apiserver", "client", "https", "kube-apiserver",
			"root-ca", "ca.crt", "metrics-client", "tls.crt", "metrics-client", "tls.key"),
		newServiceMonitor("etcd", namespace, "etcd", "metrics", "https", "etcd-client",
			"etcd-ca", "ca.crt", "etcd-metrics-client-tls", "etcd-client.crt", "etcd-metrics-client-tls", "etcd-client.key"),
		newService("kube-apiserver", namespace, "kube-apiserver", "client", 6443),
		// No etcd service — etcd should be skipped.
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	_ = prometheusoperatorv1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()

	cpContext := component.WorkloadContext{
		Context: context.Background(),
		Client:  fakeClient,
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "test",
			},
		},
	}

	cm := &corev1.ConfigMap{}

	t.Run("When some services are missing, it should skip missing components without error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		g.Expect(adaptScrapeConfig(cpContext, cm)).To(Succeed())

		var cfg metricsproxybin.FileConfig
		g.Expect(yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg)).To(Succeed())

		g.Expect(cfg.Components).To(HaveLen(1))
		_, ok := findComponent(cfg.Components, "kube-apiserver")
		g.Expect(ok).To(BeTrue(), "expected kube-apiserver to be present")
	})
}

func TestAdaptScrapeConfigWithPodMonitors(t *testing.T) {
	g := NewGomegaWithT(t)
	t.Parallel()

	namespace := "clusters-test-hosted"

	podMonitors := []runtime.Object{
		newPodMonitor("cluster-autoscaler", namespace, "metrics", "http", nil),
		newPodMonitor("control-plane-operator", namespace, "metrics", "http", nil),
		newPodMonitor("hosted-cluster-config-operator", namespace, "metrics", "http", nil),
		newPodMonitor("ignition-server", namespace, "metrics", "http", nil),
		newPodMonitor("ingress-operator", namespace, "metrics", "http", nil),
		newPodMonitor("karpenter", namespace, "metrics", "http", nil),
		newPodMonitor("karpenter-operator", namespace, "metrics", "http", nil),
		// cluster-image-registry-operator has TLS (CA only).
		newPodMonitor("cluster-image-registry-operator", namespace, "metrics", "https", &prometheusoperatorv1.SafeTLSConfig{
			CA: prometheusoperatorv1.SecretOrConfigMap{
				ConfigMap: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
					Key:                  "ca.crt",
				},
			},
			ServerName: ptr.To("cluster-image-registry-operator." + namespace + ".svc"),
		}),
	}

	pods := []runtime.Object{
		newPod("cluster-autoscaler", namespace, "metrics", 8085),
		newPod("control-plane-operator", namespace, "metrics", 8080),
		newPod("hosted-cluster-config-operator", namespace, "metrics", 8080),
		newPod("ignition-server", namespace, "metrics", 8080),
		newPod("ingress-operator", namespace, "metrics", 60000),
		newPod("karpenter", namespace, "metrics", 8080),
		newPod("karpenter-operator", namespace, "metrics", 8080),
		newPod("cluster-image-registry-operator", namespace, "metrics", 60000),
	}

	allObjects := append(podMonitors, pods...)

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	_ = prometheusoperatorv1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(allObjects...).Build()

	cpContext := component.WorkloadContext{
		Context: context.Background(),
		Client:  fakeClient,
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "test",
			},
		},
	}

	cm := &corev1.ConfigMap{}
	g.Expect(adaptScrapeConfig(cpContext, cm)).To(Succeed())

	var cfg metricsproxybin.FileConfig
	g.Expect(yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg)).To(Succeed())

	t.Run("When all PodMonitors and pods exist, it should include all 8 PodMonitor components", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		g.Expect(cfg.Components).To(HaveLen(8))
	})

	t.Run("When cluster-autoscaler config is generated, it should have correct port, scheme and selector", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		ca, ok := findComponent(cfg.Components, "cluster-autoscaler")
		g.Expect(ok).To(BeTrue(), "cluster-autoscaler not found in components")
		g.Expect(ca.MetricsPort).To(Equal(int32(8085)))
		g.Expect(ca.MetricsScheme).To(Equal("http"))
		g.Expect(ca.Selector).To(HaveKeyWithValue("app", "cluster-autoscaler"))
	})

	t.Run("When cluster-image-registry-operator config is generated, it should have TLS CA", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		ciro, ok := findComponent(cfg.Components, "cluster-image-registry-operator")
		g.Expect(ok).To(BeTrue(), "cluster-image-registry-operator not found in components")
		g.Expect(ciro.MetricsPort).To(Equal(int32(60000)))
		g.Expect(ciro.MetricsScheme).To(Equal("https"))
		g.Expect(ciro.CAFile).To(Equal(certBasePath + "/root-ca/ca.crt"))
		g.Expect(ciro.CertFile).To(BeEmpty())
		g.Expect(ciro.KeyFile).To(BeEmpty())
		g.Expect(ciro.TLSServerName).To(Equal("cluster-image-registry-operator." + namespace + ".svc"))
	})

	t.Run("When PodMonitor without TLS is generated, it should have no TLS fields", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		cpo, ok := findComponent(cfg.Components, "control-plane-operator")
		g.Expect(ok).To(BeTrue(), "control-plane-operator not found in components")
		g.Expect(cpo.CAFile).To(BeEmpty())
		g.Expect(cpo.TLSServerName).To(BeEmpty())
	})
}

func TestAdaptScrapeConfigMixedMonitors(t *testing.T) {
	t.Parallel()

	namespace := "clusters-test-hosted"

	objects := []runtime.Object{
		// One ServiceMonitor with its Service.
		newServiceMonitor("kube-apiserver", namespace, "kube-apiserver", "client", "https", "kube-apiserver",
			"root-ca", "ca.crt", "metrics-client", "tls.crt", "metrics-client", "tls.key"),
		newService("kube-apiserver", namespace, "kube-apiserver", "client", 6443),
		// One PodMonitor with its Pod.
		newPodMonitor("cluster-autoscaler", namespace, "metrics", "http", nil),
		newPod("cluster-autoscaler", namespace, "metrics", 8085),
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	_ = prometheusoperatorv1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()

	cpContext := component.WorkloadContext{
		Context: context.Background(),
		Client:  fakeClient,
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "test",
			},
		},
	}

	cm := &corev1.ConfigMap{}
	g := NewGomegaWithT(t)
	g.Expect(adaptScrapeConfig(cpContext, cm)).To(Succeed())

	var cfg metricsproxybin.FileConfig
	g.Expect(yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg)).To(Succeed())

	t.Run("When both ServiceMonitor and PodMonitor components exist, it should include both", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		g.Expect(cfg.Components).To(HaveLen(2))
		_, ok := findComponent(cfg.Components, "kube-apiserver")
		g.Expect(ok).To(BeTrue(), "expected kube-apiserver to be present")
		_, ok = findComponent(cfg.Components, "cluster-autoscaler")
		g.Expect(ok).To(BeTrue(), "expected cluster-autoscaler to be present")
	})
}

func TestAdaptScrapeConfigNamedTargetPort(t *testing.T) {
	g := NewGomegaWithT(t)
	t.Parallel()

	namespace := "clusters-test-hosted"

	objects := []runtime.Object{
		// Service with a named targetPort that differs from the service port.
		newServiceWithTargetPort("kube-apiserver", namespace, "kube-apiserver", "client", 443, intstr.FromString("https")),
		newServiceMonitor("kube-apiserver", namespace, "kube-apiserver", "client", "https", "kube-apiserver",
			"root-ca", "ca.crt", "metrics-client", "tls.crt", "metrics-client", "tls.key"),
		// Pod with the named container port "https" on 6443.
		newPod("kube-apiserver", namespace, "https", 6443),
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	_ = prometheusoperatorv1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()

	cpContext := component.WorkloadContext{
		Context: context.Background(),
		Client:  fakeClient,
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "test",
			},
		},
	}

	cm := &corev1.ConfigMap{}
	g.Expect(adaptScrapeConfig(cpContext, cm)).To(Succeed())

	var cfg metricsproxybin.FileConfig
	g.Expect(yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg)).To(Succeed())

	t.Run("When service has a named targetPort, it should resolve the port from the pod", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		kas, ok := findComponent(cfg.Components, "kube-apiserver")
		g.Expect(ok).To(BeTrue(), "kube-apiserver not found in components")
		g.Expect(kas.MetricsPort).To(Equal(int32(6443)))
	})
}

func TestAdaptScrapeConfigPodMonitorNameMismatch(t *testing.T) {
	g := NewGomegaWithT(t)
	t.Parallel()

	namespace := "clusters-test-hosted"

	// Regression test: the PodMonitor name differs from the pod label value.
	// This mirrors the real control-plane-operator case where the PodMonitor YAML
	// has name: controlplane-operator but the pod label is name: control-plane-operator.
	objects := []runtime.Object{
		&prometheusoperatorv1.PodMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "controlplane-operator",
				Namespace: namespace,
			},
			Spec: prometheusoperatorv1.PodMonitorSpec{
				PodMetricsEndpoints: []prometheusoperatorv1.PodMetricsEndpoint{
					{Port: ptr.To("metrics"), Scheme: (*prometheusoperatorv1.Scheme)(ptr.To("http"))},
				},
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"name": "control-plane-operator"},
				},
			},
		},
		newPodWithLabels("control-plane-operator", namespace, "metrics", 8080,
			map[string]string{"name": "control-plane-operator"}),
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	_ = prometheusoperatorv1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()

	cpContext := component.WorkloadContext{
		Context: context.Background(),
		Client:  fakeClient,
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "test",
			},
		},
	}

	cm := &corev1.ConfigMap{}
	g.Expect(adaptScrapeConfig(cpContext, cm)).To(Succeed())

	var cfg metricsproxybin.FileConfig
	g.Expect(yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg)).To(Succeed())

	t.Run("When PodMonitor name differs from pod label, it should still resolve via selector", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		cpo, ok := findComponent(cfg.Components, "controlplane-operator")
		g.Expect(ok).To(BeTrue(), "controlplane-operator not found in components")
		g.Expect(cpo.MetricsPort).To(Equal(int32(8080)))
	})
}

func TestAdaptScrapeConfigPodMonitorMissingPod(t *testing.T) {
	t.Parallel()

	namespace := "clusters-test-hosted"

	objects := []runtime.Object{
		newPodMonitor("cluster-autoscaler", namespace, "metrics", "http", nil),
		// No pod — should be skipped.
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	_ = prometheusoperatorv1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()

	cpContext := component.WorkloadContext{
		Context: context.Background(),
		Client:  fakeClient,
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "test",
			},
		},
	}

	cm := &corev1.ConfigMap{}

	t.Run("When PodMonitor pod is missing, it should skip without error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Parallel()
		g.Expect(adaptScrapeConfig(cpContext, cm)).To(Succeed())

		var cfg metricsproxybin.FileConfig
		g.Expect(yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg)).To(Succeed())
		g.Expect(cfg.Components).To(BeEmpty())
	})
}

func TestEndpointScheme(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		scheme   *prometheusoperatorv1.Scheme
		expected string
	}{
		{
			name:     "When scheme is nil, it should default to http",
			scheme:   nil,
			expected: "http",
		},
		{
			name:     "When scheme is https, it should return https",
			scheme:   (*prometheusoperatorv1.Scheme)(ptr.To("https")),
			expected: "https",
		},
		{
			name:     "When scheme is http, it should return http",
			scheme:   (*prometheusoperatorv1.Scheme)(ptr.To("http")),
			expected: "http",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(endpointScheme(tc.scheme)).To(Equal(tc.expected))
		})
	}
}

func TestEndpointMetricsPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "When path is empty, it should default to /metrics",
			path:     "",
			expected: "/metrics",
		},
		{
			name:     "When path is provided, it should return that path",
			path:     "/custom/metrics",
			expected: "/custom/metrics",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(endpointMetricsPath(tc.path)).To(Equal(tc.expected))
		})
	}
}

func TestSafeTLSServerName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		tlsCfg   *prometheusoperatorv1.TLSConfig
		expected string
	}{
		{
			name:     "When TLS config is nil, it should return empty string",
			tlsCfg:   nil,
			expected: "",
		},
		{
			name: "When ServerName is nil, it should return empty string",
			tlsCfg: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: nil,
				},
			},
			expected: "",
		},
		{
			name: "When ServerName is set, it should return the server name",
			tlsCfg: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: ptr.To("kube-apiserver"),
				},
			},
			expected: "kube-apiserver",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(safeTLSServerName(tc.tlsCfg)).To(Equal(tc.expected))
		})
	}
}

func TestCertFilePathFromSecretOrConfigMap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		ref      prometheusoperatorv1.SecretOrConfigMap
		expected string
	}{
		{
			name:     "When both Secret and ConfigMap are nil, it should return empty string",
			ref:      prometheusoperatorv1.SecretOrConfigMap{},
			expected: "",
		},
		{
			name: "When ConfigMap is set, it should return configmap path",
			ref: prometheusoperatorv1.SecretOrConfigMap{
				ConfigMap: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
					Key:                  "ca.crt",
				},
			},
			expected: certBasePath + "/root-ca/ca.crt",
		},
		{
			name: "When Secret is set, it should return secret path",
			ref: prometheusoperatorv1.SecretOrConfigMap{
				Secret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "my-secret"},
					Key:                  "tls.crt",
				},
			},
			expected: certBasePath + "/my-secret/tls.crt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(certFilePathFromSecretOrConfigMap(tc.ref)).To(Equal(tc.expected))
		})
	}
}

func TestPopulateTLSFilePaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		ca           prometheusoperatorv1.SecretOrConfigMap
		cert         prometheusoperatorv1.SecretOrConfigMap
		keySecret    *corev1.SecretKeySelector
		expectedCA   string
		expectedCert string
		expectedKey  string
	}{
		{
			name:         "When all TLS refs are empty, it should set empty paths",
			ca:           prometheusoperatorv1.SecretOrConfigMap{},
			cert:         prometheusoperatorv1.SecretOrConfigMap{},
			keySecret:    nil,
			expectedCA:   "",
			expectedCert: "",
			expectedKey:  "",
		},
		{
			name: "When CA, cert, and key are all provided, it should set all paths",
			ca: prometheusoperatorv1.SecretOrConfigMap{
				ConfigMap: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
					Key:                  "ca.crt",
				},
			},
			cert: prometheusoperatorv1.SecretOrConfigMap{
				Secret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "metrics-client"},
					Key:                  "tls.crt",
				},
			},
			keySecret: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "metrics-client"},
				Key:                  "tls.key",
			},
			expectedCA:   certBasePath + "/root-ca/ca.crt",
			expectedCert: certBasePath + "/metrics-client/tls.crt",
			expectedKey:  certBasePath + "/metrics-client/tls.key",
		},
		{
			name: "When only CA is provided, it should set only CA path",
			ca: prometheusoperatorv1.SecretOrConfigMap{
				ConfigMap: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
					Key:                  "ca.crt",
				},
			},
			cert:         prometheusoperatorv1.SecretOrConfigMap{},
			keySecret:    nil,
			expectedCA:   certBasePath + "/root-ca/ca.crt",
			expectedCert: "",
			expectedKey:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			comp := &metricsproxybin.ComponentFileConfig{}
			populateTLSFilePaths(comp, tc.ca, tc.cert, tc.keySecret)
			g.Expect(comp.CAFile).To(Equal(tc.expectedCA))
			g.Expect(comp.CertFile).To(Equal(tc.expectedCert))
			g.Expect(comp.KeyFile).To(Equal(tc.expectedKey))
		})
	}
}

func TestPopulateMetricsLabelsFromAnnotations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		annotations       map[string]string
		expectedJob       string
		expectedNamespace string
		expectedService   string
		expectedEndpoint  string
	}{
		{
			name:              "When no annotations are present, it should leave fields empty",
			annotations:       map[string]string{},
			expectedJob:       "",
			expectedNamespace: "",
			expectedService:   "",
			expectedEndpoint:  "",
		},
		{
			name:              "When annotations is nil, it should leave fields empty",
			annotations:       nil,
			expectedJob:       "",
			expectedNamespace: "",
			expectedService:   "",
			expectedEndpoint:  "",
		},
		{
			name: "When all metrics annotations are present, it should populate all fields",
			annotations: map[string]string{
				"hypershift.openshift.io/metrics-job":       "kube-apiserver",
				"hypershift.openshift.io/metrics-namespace": "openshift-kube-apiserver",
				"hypershift.openshift.io/metrics-service":   "kube-apiserver",
				"hypershift.openshift.io/metrics-endpoint":  "https",
			},
			expectedJob:       "kube-apiserver",
			expectedNamespace: "openshift-kube-apiserver",
			expectedService:   "kube-apiserver",
			expectedEndpoint:  "https",
		},
		{
			name: "When only some annotations are present, it should populate only matching fields",
			annotations: map[string]string{
				"hypershift.openshift.io/metrics-job":       "etcd",
				"hypershift.openshift.io/metrics-namespace": "openshift-etcd",
			},
			expectedJob:       "etcd",
			expectedNamespace: "openshift-etcd",
			expectedService:   "",
			expectedEndpoint:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			comp := &metricsproxybin.ComponentFileConfig{}
			populateMetricsLabelsFromAnnotations(comp, tc.annotations)
			g.Expect(comp.MetricsJob).To(Equal(tc.expectedJob))
			g.Expect(comp.MetricsNamespace).To(Equal(tc.expectedNamespace))
			g.Expect(comp.MetricsService).To(Equal(tc.expectedService))
			g.Expect(comp.MetricsEndpoint).To(Equal(tc.expectedEndpoint))
		})
	}
}

func TestFindServiceForMonitor(t *testing.T) {
	t.Parallel()

	namespace := "test-ns"

	tests := []struct {
		name            string
		objects         []runtime.Object
		smName          string
		selector        metav1.LabelSelector
		expectedSvc     string
		expectedErr     bool
		expectedSelKeys []string
	}{
		{
			name: "When service exists with same name as ServiceMonitor, it should find it by direct lookup",
			objects: []runtime.Object{
				newService("kube-apiserver", namespace, "kube-apiserver", "client", 6443),
			},
			smName:          "kube-apiserver",
			selector:        metav1.LabelSelector{MatchLabels: map[string]string{"app": "kube-apiserver"}},
			expectedSvc:     "kube-apiserver",
			expectedErr:     false,
			expectedSelKeys: []string{"app"},
		},
		{
			name: "When no service matches name but label selector matches, it should fall back to label selector",
			objects: []runtime.Object{
				newService("etcd-client", namespace, "etcd", "metrics", 2381),
			},
			smName:          "etcd",
			selector:        metav1.LabelSelector{MatchLabels: map[string]string{"app": "etcd"}},
			expectedSvc:     "etcd-client",
			expectedErr:     false,
			expectedSelKeys: []string{"app"},
		},
		{
			name:        "When no service exists at all, it should return an error",
			objects:     []runtime.Object{},
			smName:      "missing",
			selector:    metav1.LabelSelector{MatchLabels: map[string]string{"app": "missing"}},
			expectedErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.objects...).Build()

			cpContext := component.WorkloadContext{
				Context: context.Background(),
				Client:  fakeClient,
			}

			svcName, podSelector, err := findServiceForMonitor(cpContext, namespace, tc.smName, tc.selector)
			if tc.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(svcName).To(Equal(tc.expectedSvc))
				for _, key := range tc.expectedSelKeys {
					g.Expect(podSelector).To(HaveKey(key))
				}
			}
		})
	}
}

func TestResolveServicePort(t *testing.T) {
	t.Parallel()

	namespace := "test-ns"

	tests := []struct {
		name         string
		objects      []runtime.Object
		serviceName  string
		portName     string
		podSelector  map[string]string
		expectedPort int32
		expectedErr  bool
	}{
		{
			name: "When service has a numeric targetPort, it should resolve directly",
			objects: []runtime.Object{
				newService("my-svc", namespace, "app", "https", 6443),
			},
			serviceName:  "my-svc",
			portName:     "https",
			podSelector:  map[string]string{"app": "app"},
			expectedPort: 6443,
			expectedErr:  false,
		},
		{
			name: "When service has a named targetPort, it should resolve from a pod",
			objects: []runtime.Object{
				newServiceWithTargetPort("my-svc", namespace, "app", "client", 443, intstr.FromString("https")),
				newPodWithLabels("my-pod", namespace, "https", 6443, map[string]string{"app": "app"}),
			},
			serviceName:  "my-svc",
			portName:     "client",
			podSelector:  map[string]string{"app": "app"},
			expectedPort: 6443,
			expectedErr:  false,
		},
		{
			name: "When service port name does not match, it should return an error",
			objects: []runtime.Object{
				newService("my-svc", namespace, "app", "metrics", 8080),
			},
			serviceName: "my-svc",
			portName:    "nonexistent",
			podSelector: map[string]string{"app": "app"},
			expectedErr: true,
		},
		{
			name:        "When service does not exist, it should return an error",
			objects:     []runtime.Object{},
			serviceName: "missing-svc",
			portName:    "https",
			podSelector: map[string]string{"app": "app"},
			expectedErr: true,
		},
		{
			name: "When service has no targetPort, it should default to the service port value",
			objects: []runtime.Object{
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "my-svc", Namespace: namespace},
					Spec: corev1.ServiceSpec{
						Selector: map[string]string{"app": "app"},
						Ports:    []corev1.ServicePort{{Name: "web", Port: 8443}},
					},
				},
			},
			serviceName:  "my-svc",
			portName:     "web",
			podSelector:  map[string]string{"app": "app"},
			expectedPort: 8443,
			expectedErr:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.objects...).Build()

			cpContext := component.WorkloadContext{
				Context: context.Background(),
				Client:  fakeClient,
			}

			port, err := resolveServicePort(cpContext, namespace, tc.serviceName, tc.portName, tc.podSelector)
			if tc.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(port).To(Equal(tc.expectedPort))
			}
		})
	}
}

func TestComponentFromServiceMonitor_EdgeCases(t *testing.T) {
	t.Parallel()

	namespace := "test-ns"

	t.Run("When ServiceMonitor has no endpoints, it should return false", func(t *testing.T) {
		g := NewGomegaWithT(t)
		sm := &prometheusoperatorv1.ServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{Name: "empty-sm", Namespace: namespace},
			Spec:       prometheusoperatorv1.ServiceMonitorSpec{},
		}

		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		_ = prometheusoperatorv1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
		}

		_, ok := componentFromServiceMonitor(cpContext, logr.Discard(), namespace, sm)
		g.Expect(ok).To(BeFalse())
	})

	t.Run("When endpoint has no port reference, it should return false", func(t *testing.T) {
		g := NewGomegaWithT(t)
		sm := &prometheusoperatorv1.ServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{Name: "no-port-sm", Namespace: namespace},
			Spec: prometheusoperatorv1.ServiceMonitorSpec{
				Endpoints: []prometheusoperatorv1.Endpoint{{}},
				Selector:  metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
			},
		}
		svc := newService("no-port-sm", namespace, "test", "metrics", 8080)

		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		_ = prometheusoperatorv1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(svc).Build()

		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
		}

		_, ok := componentFromServiceMonitor(cpContext, logr.Discard(), namespace, sm)
		g.Expect(ok).To(BeFalse())
	})
}

func TestComponentFromPodMonitor_EdgeCases(t *testing.T) {
	t.Parallel()

	namespace := "test-ns"

	t.Run("When PodMonitor has no endpoints, it should return false", func(t *testing.T) {
		g := NewGomegaWithT(t)
		pm := &prometheusoperatorv1.PodMonitor{
			ObjectMeta: metav1.ObjectMeta{Name: "empty-pm", Namespace: namespace},
			Spec:       prometheusoperatorv1.PodMonitorSpec{},
		}

		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		_ = prometheusoperatorv1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
		}

		_, ok := componentFromPodMonitor(cpContext, logr.Discard(), namespace, pm)
		g.Expect(ok).To(BeFalse())
	})

	t.Run("When PodMonitor endpoint has nil port, it should return false", func(t *testing.T) {
		g := NewGomegaWithT(t)
		pm := &prometheusoperatorv1.PodMonitor{
			ObjectMeta: metav1.ObjectMeta{Name: "nil-port-pm", Namespace: namespace},
			Spec: prometheusoperatorv1.PodMonitorSpec{
				PodMetricsEndpoints: []prometheusoperatorv1.PodMetricsEndpoint{
					{Port: nil},
				},
				Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
			},
		}

		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		_ = prometheusoperatorv1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
		}

		_, ok := componentFromPodMonitor(cpContext, logr.Discard(), namespace, pm)
		g.Expect(ok).To(BeFalse())
	})
}

func TestResolvePodPort(t *testing.T) {
	t.Parallel()

	namespace := "test-ns"

	tests := []struct {
		name        string
		pods        []runtime.Object
		selector    map[string]string
		portName    string
		expectedErr bool
		expected    int32
	}{
		{
			name: "When a pod has the named port, it should resolve the port number",
			pods: []runtime.Object{
				newPodWithLabels("my-pod", namespace, "https", 6443, map[string]string{"app": "kas"}),
			},
			selector: map[string]string{"app": "kas"},
			portName: "https",
			expected: 6443,
		},
		{
			name: "When no pods match the selector, it should return an error",
			pods: []runtime.Object{
				newPodWithLabels("my-pod", namespace, "https", 6443, map[string]string{"app": "other"}),
			},
			selector:    map[string]string{"app": "kas"},
			portName:    "https",
			expectedErr: true,
		},
		{
			name: "When the port name does not match, it should return an error",
			pods: []runtime.Object{
				newPodWithLabels("my-pod", namespace, "metrics", 8080, map[string]string{"app": "kas"}),
			},
			selector:    map[string]string{"app": "kas"},
			portName:    "https",
			expectedErr: true,
		},
		{
			name:        "When no pods exist, it should return an error",
			pods:        nil,
			selector:    map[string]string{"app": "kas"},
			portName:    "https",
			expectedErr: true,
		},
		{
			name: "When multiple pods exist during rollout, it should resolve from the first match",
			pods: []runtime.Object{
				newPodWithLabels("pod-old", namespace, "https", 6443, map[string]string{"app": "kas"}),
				newPodWithLabels("pod-new", namespace, "https", 6443, map[string]string{"app": "kas"}),
			},
			selector: map[string]string{"app": "kas"},
			portName: "https",
			expected: 6443,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			t.Parallel()

			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tt.pods...).Build()

			cpContext := component.WorkloadContext{
				Context: context.Background(),
				Client:  fakeClient,
			}

			port, err := resolvePodPort(cpContext, namespace, labels.SelectorFromSet(tt.selector), tt.portName)
			if tt.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(port).To(Equal(tt.expected))
			}
		})
	}
}
