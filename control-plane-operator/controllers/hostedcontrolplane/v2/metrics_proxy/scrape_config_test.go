package metricsproxy

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metricsproxybin "github.com/openshift/hypershift/control-plane-operator/metrics-proxy"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

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
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": appLabel},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: portName, Port: portNum, TargetPort: intstr.FromInt32(portNum)},
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

func newDeployment(name, namespace, portName string, portNum int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
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
			},
		},
	}
}

func TestAdaptScrapeConfig(t *testing.T) {
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
	_ = appsv1.AddToScheme(scheme)
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

	if err := adaptScrapeConfig(cpContext, cm); err != nil {
		t.Fatalf("adaptScrapeConfig() returned error: %v", err)
	}

	configData, ok := cm.Data["config.yaml"]
	if !ok {
		t.Fatal("config.yaml key missing from ConfigMap data")
	}

	var cfg metricsproxybin.FileConfig
	if err := yaml.Unmarshal([]byte(configData), &cfg); err != nil {
		t.Fatalf("failed to unmarshal config YAML: %v", err)
	}

	t.Run("When all ServiceMonitors and services exist, it should include all 10 components", func(t *testing.T) {
		t.Parallel()
		if len(cfg.Components) != 10 {
			t.Errorf("expected 10 components, got %d", len(cfg.Components))
		}
	})

	t.Run("When endpoint resolver config is generated, it should have correct URL and CA", func(t *testing.T) {
		t.Parallel()
		expectedURL := "https://endpoint-resolver." + namespace + ".svc"
		if cfg.EndpointResolver.URL != expectedURL {
			t.Errorf("expected endpoint resolver URL %q, got %q", expectedURL, cfg.EndpointResolver.URL)
		}
		if cfg.EndpointResolver.CAFile != endpointResolverCA {
			t.Errorf("expected endpoint resolver CA %q, got %q", endpointResolverCA, cfg.EndpointResolver.CAFile)
		}
	})

	t.Run("When kube-apiserver config is generated, it should have correct port and certs", func(t *testing.T) {
		t.Parallel()
		kas, ok := cfg.Components["kube-apiserver"]
		if !ok {
			t.Fatal("kube-apiserver not found in components")
		}
		if kas.MetricsPort != 6443 {
			t.Errorf("expected kube-apiserver port 6443, got %d", kas.MetricsPort)
		}
		expectedCA := certBasePath + "/root-ca/ca.crt"
		if kas.CAFile != expectedCA {
			t.Errorf("expected CA file %q, got %q", expectedCA, kas.CAFile)
		}
		expectedCert := certBasePath + "/metrics-client/tls.crt"
		if kas.CertFile != expectedCert {
			t.Errorf("expected cert file %q, got %q", expectedCert, kas.CertFile)
		}
	})

	t.Run("When etcd config is generated, it should use etcd-specific certs", func(t *testing.T) {
		t.Parallel()
		etcd, ok := cfg.Components["etcd"]
		if !ok {
			t.Fatal("etcd not found in components")
		}
		expectedCA := certBasePath + "/etcd-ca/ca.crt"
		if etcd.CAFile != expectedCA {
			t.Errorf("expected etcd CA file %q, got %q", expectedCA, etcd.CAFile)
		}
		expectedCert := certBasePath + "/etcd-metrics-client-tls/etcd-client.crt"
		if etcd.CertFile != expectedCert {
			t.Errorf("expected etcd cert file %q, got %q", expectedCert, etcd.CertFile)
		}
		expectedKey := certBasePath + "/etcd-metrics-client-tls/etcd-client.key"
		if etcd.KeyFile != expectedKey {
			t.Errorf("expected etcd key file %q, got %q", expectedKey, etcd.KeyFile)
		}
	})

	t.Run("When CVO config is generated, it should have no client cert", func(t *testing.T) {
		t.Parallel()
		cvo, ok := cfg.Components["cluster-version-operator"]
		if !ok {
			t.Fatal("cluster-version-operator not found in components")
		}
		if cvo.CertFile != "" {
			t.Errorf("expected empty cert file for CVO, got %q", cvo.CertFile)
		}
		if cvo.KeyFile != "" {
			t.Errorf("expected empty key file for CVO, got %q", cvo.KeyFile)
		}
	})

	t.Run("When NTO config is generated, it should include namespace in serverName", func(t *testing.T) {
		t.Parallel()
		nto, ok := cfg.Components["node-tuning-operator"]
		if !ok {
			t.Fatal("node-tuning-operator not found in components")
		}
		expectedServerName := "node-tuning-operator." + namespace + ".svc"
		if nto.TLSServerName != expectedServerName {
			t.Errorf("expected NTO server name %q, got %q", expectedServerName, nto.TLSServerName)
		}
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
	_ = appsv1.AddToScheme(scheme)
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
		t.Parallel()
		if err := adaptScrapeConfig(cpContext, cm); err != nil {
			t.Fatalf("adaptScrapeConfig() returned error: %v", err)
		}

		var cfg metricsproxybin.FileConfig
		if err := yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
			t.Fatalf("failed to unmarshal config YAML: %v", err)
		}

		if len(cfg.Components) != 1 {
			t.Errorf("expected 1 component, got %d", len(cfg.Components))
		}
		if _, ok := cfg.Components["kube-apiserver"]; !ok {
			t.Error("expected kube-apiserver to be present")
		}
	})
}

func TestAdaptScrapeConfigWithPodMonitors(t *testing.T) {
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

	deployments := []runtime.Object{
		newDeployment("cluster-autoscaler", namespace, "metrics", 8085),
		newDeployment("control-plane-operator", namespace, "metrics", 8080),
		newDeployment("hosted-cluster-config-operator", namespace, "metrics", 8080),
		newDeployment("ignition-server", namespace, "metrics", 8080),
		newDeployment("ingress-operator", namespace, "metrics", 60000),
		newDeployment("karpenter", namespace, "metrics", 8080),
		newDeployment("karpenter-operator", namespace, "metrics", 8080),
		newDeployment("cluster-image-registry-operator", namespace, "metrics", 60000),
	}

	allObjects := append(podMonitors, deployments...)

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
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
	if err := adaptScrapeConfig(cpContext, cm); err != nil {
		t.Fatalf("adaptScrapeConfig() returned error: %v", err)
	}

	var cfg metricsproxybin.FileConfig
	if err := yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
		t.Fatalf("failed to unmarshal config YAML: %v", err)
	}

	t.Run("When all PodMonitors and deployments exist, it should include all 8 PodMonitor components", func(t *testing.T) {
		t.Parallel()
		if len(cfg.Components) != 8 {
			t.Errorf("expected 8 components, got %d", len(cfg.Components))
		}
	})

	t.Run("When cluster-autoscaler config is generated, it should have correct port and scheme", func(t *testing.T) {
		t.Parallel()
		ca, ok := cfg.Components["cluster-autoscaler"]
		if !ok {
			t.Fatal("cluster-autoscaler not found in components")
		}
		if ca.MetricsPort != 8085 {
			t.Errorf("expected cluster-autoscaler port 8085, got %d", ca.MetricsPort)
		}
		if ca.MetricsScheme != "http" {
			t.Errorf("expected scheme http, got %q", ca.MetricsScheme)
		}
		if ca.ServiceName != "cluster-autoscaler" {
			t.Errorf("expected service name cluster-autoscaler, got %q", ca.ServiceName)
		}
	})

	t.Run("When cluster-image-registry-operator config is generated, it should have TLS CA", func(t *testing.T) {
		t.Parallel()
		ciro, ok := cfg.Components["cluster-image-registry-operator"]
		if !ok {
			t.Fatal("cluster-image-registry-operator not found in components")
		}
		if ciro.MetricsPort != 60000 {
			t.Errorf("expected port 60000, got %d", ciro.MetricsPort)
		}
		if ciro.MetricsScheme != "https" {
			t.Errorf("expected scheme https, got %q", ciro.MetricsScheme)
		}
		expectedCA := certBasePath + "/root-ca/ca.crt"
		if ciro.CAFile != expectedCA {
			t.Errorf("expected CA file %q, got %q", expectedCA, ciro.CAFile)
		}
		// No client cert for this component.
		if ciro.CertFile != "" {
			t.Errorf("expected empty cert file, got %q", ciro.CertFile)
		}
		if ciro.KeyFile != "" {
			t.Errorf("expected empty key file, got %q", ciro.KeyFile)
		}
		expectedServerName := "cluster-image-registry-operator." + namespace + ".svc"
		if ciro.TLSServerName != expectedServerName {
			t.Errorf("expected server name %q, got %q", expectedServerName, ciro.TLSServerName)
		}
	})

	t.Run("When PodMonitor without TLS is generated, it should have no TLS fields", func(t *testing.T) {
		t.Parallel()
		cpo, ok := cfg.Components["control-plane-operator"]
		if !ok {
			t.Fatal("control-plane-operator not found in components")
		}
		if cpo.CAFile != "" {
			t.Errorf("expected empty CA file, got %q", cpo.CAFile)
		}
		if cpo.TLSServerName != "" {
			t.Errorf("expected empty server name, got %q", cpo.TLSServerName)
		}
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
		// One PodMonitor with its Deployment.
		newPodMonitor("cluster-autoscaler", namespace, "metrics", "http", nil),
		newDeployment("cluster-autoscaler", namespace, "metrics", 8085),
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
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
	if err := adaptScrapeConfig(cpContext, cm); err != nil {
		t.Fatalf("adaptScrapeConfig() returned error: %v", err)
	}

	var cfg metricsproxybin.FileConfig
	if err := yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
		t.Fatalf("failed to unmarshal config YAML: %v", err)
	}

	t.Run("When both ServiceMonitor and PodMonitor components exist, it should include both", func(t *testing.T) {
		t.Parallel()
		if len(cfg.Components) != 2 {
			t.Errorf("expected 2 components, got %d", len(cfg.Components))
		}
		if _, ok := cfg.Components["kube-apiserver"]; !ok {
			t.Error("expected kube-apiserver to be present")
		}
		if _, ok := cfg.Components["cluster-autoscaler"]; !ok {
			t.Error("expected cluster-autoscaler to be present")
		}
	})
}

func TestAdaptScrapeConfigPodMonitorMissingDeployment(t *testing.T) {
	t.Parallel()

	namespace := "clusters-test-hosted"

	objects := []runtime.Object{
		newPodMonitor("cluster-autoscaler", namespace, "metrics", "http", nil),
		// No deployment — should be skipped.
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
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

	t.Run("When PodMonitor deployment is missing, it should skip without error", func(t *testing.T) {
		t.Parallel()
		if err := adaptScrapeConfig(cpContext, cm); err != nil {
			t.Fatalf("adaptScrapeConfig() returned error: %v", err)
		}

		var cfg metricsproxybin.FileConfig
		if err := yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
			t.Fatalf("failed to unmarshal config YAML: %v", err)
		}

		if len(cfg.Components) != 0 {
			t.Errorf("expected 0 components, got %d", len(cfg.Components))
		}
	})
}
