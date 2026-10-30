package mirrorregistry

import (
	"reflect"
	"testing"

	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func TestBuildNamespace(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "When the namespace is configured, it should label the namespace for the registry"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := Options{Name: "mirror-registry", Namespace: "test-mirror-registry"}
			got := buildNamespace(opts)
			if got.Name != opts.Namespace {
				t.Errorf("namespace name = %q, want %q", got.Name, opts.Namespace)
			}
			assertResourceLabels(t, got.Labels, opts.Name)
		})
	}
}

func TestBuildCASecret(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "When a CA key pair is provided, it should persist both PEMs in a labeled Opaque Secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := Options{Name: "mirror-registry", Namespace: "test-mirror-registry"}
			ca, _ := testRegistryTLSMaterial(t)
			got := buildCASecret(opts, ca)
			if got.Name != opts.Name+"-ca" || got.Namespace != opts.Namespace {
				t.Errorf("CA Secret identity = %s/%s, want %s/%s", got.Namespace, got.Name, opts.Namespace, opts.Name+"-ca")
			}
			if got.Type != corev1.SecretTypeOpaque {
				t.Errorf("CA Secret type = %q, want %q", got.Type, corev1.SecretTypeOpaque)
			}
			if !reflect.DeepEqual(got.Data, map[string][]byte{"ca.crt": ca.CertPEM, "ca.key": ca.KeyPEM}) {
				t.Fatal("CA Secret data must contain exactly the certificate and private key PEMs")
			}
			assertResourceLabels(t, got.Labels, opts.Name)
		})
	}
}

func TestBuildTLSSecret(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "When serving TLS material is provided, it should create a TLS Secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := Options{Name: "mirror-registry", Namespace: "test-mirror-registry"}
			_, serving := testRegistryTLSMaterial(t)
			got := buildTLSSecret(opts, serving)
			if got.Name != opts.Name+"-tls" || got.Namespace != opts.Namespace {
				t.Errorf("TLS Secret identity = %s/%s, want %s/%s", got.Namespace, got.Name, opts.Namespace, opts.Name+"-tls")
			}
			if got.Type != corev1.SecretTypeTLS {
				t.Errorf("TLS Secret type = %q, want %q", got.Type, corev1.SecretTypeTLS)
			}
			if !reflect.DeepEqual(got.Data, map[string][]byte{corev1.TLSCertKey: serving.CertPEM, corev1.TLSPrivateKeyKey: serving.KeyPEM}) {
				t.Errorf("TLS Secret data = %#v, want certificate and key PEM", got.Data)
			}
			assertResourceLabels(t, got.Labels, opts.Name)
		})
	}
}

func TestBuildPVC(t *testing.T) {
	tests := []struct {
		name             string
		wantStorageClass *string
	}{
		{name: "When storage class is empty, it should use the cluster default"},
		{name: "When storage class is configured, it should select that class", wantStorageClass: ptr.To("gp3-csi")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := Options{Name: "mirror-registry", Namespace: "test-mirror-registry", Storage: StorageOptions{Size: resource.MustParse("5Gi")}}
			if test.wantStorageClass != nil {
				opts.Storage.StorageClassName = *test.wantStorageClass
			}
			got := buildPVC(opts)
			if got.Name != opts.Name || got.Namespace != opts.Namespace {
				t.Errorf("PVC identity = %s/%s, want %s/%s", got.Namespace, got.Name, opts.Namespace, opts.Name)
			}
			if !reflect.DeepEqual(got.Spec.AccessModes, []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}) {
				t.Errorf("PVC access modes = %v, want ReadWriteOnce", got.Spec.AccessModes)
			}
			if got.Spec.Resources.Requests[corev1.ResourceStorage] != opts.Storage.Size {
				t.Errorf("PVC storage request = %v, want %v", got.Spec.Resources.Requests[corev1.ResourceStorage], opts.Storage.Size)
			}
			if !reflect.DeepEqual(got.Spec.StorageClassName, test.wantStorageClass) {
				t.Errorf("PVC storage class = %v, want %v", got.Spec.StorageClassName, test.wantStorageClass)
			}
			assertResourceLabels(t, got.Labels, opts.Name)
		})
	}
}

func TestBuildService(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "When the registry Service is built, it should expose HTTPS on ClusterIP port 5000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := Options{Name: "mirror-registry", Namespace: "test-mirror-registry"}
			got := buildService(opts)
			if got.Name != opts.Name || got.Namespace != opts.Namespace {
				t.Errorf("Service identity = %s/%s, want %s/%s", got.Namespace, got.Name, opts.Namespace, opts.Name)
			}
			if got.Spec.Type != corev1.ServiceTypeClusterIP {
				t.Errorf("Service type = %q, want ClusterIP", got.Spec.Type)
			}
			if !reflect.DeepEqual(got.Spec.Selector, resourceLabels(opts.Name)) {
				t.Errorf("Service selector = %v, want %v", got.Spec.Selector, resourceLabels(opts.Name))
			}
			wantPort := corev1.ServicePort{Name: "https", Port: RegistryPort, TargetPort: intstr.FromInt32(RegistryPort), Protocol: corev1.ProtocolTCP}
			if !reflect.DeepEqual(got.Spec.Ports, []corev1.ServicePort{wantPort}) {
				t.Errorf("Service ports = %#v, want %#v", got.Spec.Ports, []corev1.ServicePort{wantPort})
			}
			assertResourceLabels(t, got.Labels, opts.Name)
		})
	}
}

func TestBuildConfigMap(t *testing.T) {
	opts := Options{Name: "mirror-registry", Namespace: "test-mirror-registry"}
	const wantWritableConfig = `version: 0.1
log:
  fields:
    service: registry
storage:
  filesystem:
    rootdirectory: /var/lib/registry
  delete:
    enabled: false
  maintenance:
    uploadpurging:
      enabled: false
    readonly:
      enabled: false
http:
  addr: :5000
  tls:
    certificate: /certs/tls.crt
    key: /certs/tls.key
health:
  storagedriver:
    enabled: true
    interval: 10s
    threshold: 3
`
	const wantReadOnlyConfig = `version: 0.1
log:
  fields:
    service: registry
storage:
  filesystem:
    rootdirectory: /var/lib/registry
  delete:
    enabled: false
  maintenance:
    uploadpurging:
      enabled: false
    readonly:
      enabled: true
http:
  addr: :5000
  tls:
    certificate: /certs/tls.crt
    key: /certs/tls.key
health:
  storagedriver:
    enabled: true
    interval: 10s
    threshold: 3
`
	tests := []struct {
		name     string
		readOnly bool
		want     string
	}{
		{name: "When read-only is false, it should keep registry storage writable", want: wantWritableConfig},
		{name: "When read-only is true, it should set storage.maintenance.readonly.enabled to true", readOnly: true, want: wantReadOnlyConfig},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := buildConfigMap(opts, test.readOnly)
			if got.Name != opts.Name+"-config" || got.Namespace != opts.Namespace {
				t.Errorf("ConfigMap identity = %s/%s, want %s/%s", got.Namespace, got.Name, opts.Namespace, opts.Name+"-config")
			}
			assertResourceLabels(t, got.Labels, opts.Name)
			if got.Data["config.yml"] != test.want {
				t.Errorf("config.yml =\n%s\nwant exactly:\n%s", got.Data["config.yml"], test.want)
			}
		})
	}
	writable := buildConfigMap(opts, false)
	readOnly := buildConfigMap(opts, true)
	writableWithReadOnlyContent := writable.DeepCopy()
	writableWithReadOnlyContent.Data["config.yml"] = readOnly.Data["config.yml"]
	if !reflect.DeepEqual(writableWithReadOnlyContent, readOnly) {
		t.Errorf("read-only toggle changed ConfigMap fields other than config content")
	}
}

func TestBuildDeployment(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "When the registry Deployment is built, it should use restricted-v2 settings and persistent storage"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := Options{Name: "mirror-registry", Namespace: "test-mirror-registry", Image: DefaultRegistryImage}
			got := buildDeployment(opts, "config-sha256")
			assertDeploymentMetadata(t, got, opts)
			assertDeploymentConfigHash(t, got, opts)

			pod := got.Spec.Template.Spec
			assertDeploymentPodSecurity(t, pod)
			if len(pod.Containers) != 1 {
				t.Fatalf("container count = %d, want 1", len(pod.Containers))
			}
			assertRegistryContainer(t, pod.Containers[0], opts)
			assertDeploymentVolumes(t, pod, opts)
		})
	}
}

func assertDeploymentMetadata(t *testing.T, got *appsv1.Deployment, opts Options) {
	t.Helper()
	if got.Name != opts.Name || got.Namespace != opts.Namespace {
		t.Errorf("Deployment identity = %s/%s, want %s/%s", got.Namespace, got.Name, opts.Namespace, opts.Name)
	}
	assertResourceLabels(t, got.Labels, opts.Name)
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 1 {
		t.Errorf("Deployment replicas = %v, want 1", got.Spec.Replicas)
	}
	if got.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("Deployment strategy = %q, want Recreate", got.Spec.Strategy.Type)
	}
	if !reflect.DeepEqual(got.Spec.Selector.MatchLabels, resourceLabels(opts.Name)) || !reflect.DeepEqual(got.Spec.Template.Labels, resourceLabels(opts.Name)) {
		t.Errorf("Deployment selector/template labels do not match registry labels")
	}
}

func assertDeploymentConfigHash(t *testing.T, got *appsv1.Deployment, opts Options) {
	t.Helper()
	if got.Spec.Template.Annotations[configHashAnnotation] != "config-sha256" {
		t.Errorf("pod template config hash = %q, want config-sha256", got.Spec.Template.Annotations[configHashAnnotation])
	}
	secondHash := buildDeployment(opts, "read-only-sha256")
	deploymentWithSecondHash := got.DeepCopy()
	deploymentWithSecondHash.Spec.Template.Annotations[configHashAnnotation] = "read-only-sha256"
	if !reflect.DeepEqual(deploymentWithSecondHash, secondHash) {
		t.Errorf("changing the config hash should only change the pod-template hash annotation")
	}
}

func assertDeploymentPodSecurity(t *testing.T, pod corev1.PodSpec) {
	t.Helper()
	security := pod.SecurityContext
	if security == nil || security.RunAsNonRoot == nil || !*security.RunAsNonRoot {
		t.Errorf("pod security context must set runAsNonRoot=true")
	}
	if security == nil || security.SeccompProfile == nil || security.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("pod security context must set seccomp RuntimeDefault")
	}
	if security != nil && (security.RunAsUser != nil || security.FSGroup != nil) {
		t.Errorf("pod security context must not set a fixed UID or fsGroup")
	}
}

func assertRegistryContainer(t *testing.T, container corev1.Container, opts Options) {
	t.Helper()
	if container.Name != "registry" || container.Image != opts.Image || !reflect.DeepEqual(container.Args, []string{"/etc/registry/config.yml"}) {
		t.Errorf("registry container identity/image/args = %s, %s, %v", container.Name, container.Image, container.Args)
	}
	security := container.SecurityContext
	if security == nil || security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation {
		t.Errorf("container security context must set allowPrivilegeEscalation=false")
	}
	if security == nil || security.Capabilities == nil || !reflect.DeepEqual(security.Capabilities.Drop, []corev1.Capability{"ALL"}) {
		t.Errorf("container must drop all Linux capabilities")
	}
	if security != nil && security.RunAsUser != nil {
		t.Errorf("container must not set a fixed runAsUser")
	}
	wantMounts := []corev1.VolumeMount{
		{Name: "config", MountPath: "/etc/registry", ReadOnly: true},
		{Name: "tls", MountPath: "/certs", ReadOnly: true},
		{Name: "data", MountPath: "/var/lib/registry"},
	}
	if !reflect.DeepEqual(container.VolumeMounts, wantMounts) {
		t.Errorf("registry volume mounts = %#v, want %#v", container.VolumeMounts, wantMounts)
	}
	wantPorts := []corev1.ContainerPort{{Name: "https", ContainerPort: RegistryPort, Protocol: corev1.ProtocolTCP}}
	if !reflect.DeepEqual(container.Ports, wantPorts) {
		t.Errorf("registry ports = %#v, want %#v", container.Ports, wantPorts)
	}
	assertRegistryProbes(t, container)
}

func assertRegistryProbes(t *testing.T, container corev1.Container) {
	t.Helper()
	wantProbe := func(period, delay int32) *corev1.Probe {
		return &corev1.Probe{
			ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/v2/", Port: intstr.FromInt32(RegistryPort), Scheme: corev1.URISchemeHTTPS}},
			PeriodSeconds:       period,
			InitialDelaySeconds: delay,
		}
	}
	if !reflect.DeepEqual(container.ReadinessProbe, wantProbe(5, 0)) || !reflect.DeepEqual(container.LivenessProbe, wantProbe(10, 10)) {
		t.Errorf("registry probes = readiness %#v, liveness %#v; want HTTPS GET /v2/ probes", container.ReadinessProbe, container.LivenessProbe)
	}
}

func assertDeploymentVolumes(t *testing.T, pod corev1.PodSpec, opts Options) {
	t.Helper()
	wantVolumes := []corev1.Volume{
		{
			Name: "config",
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: buildConfigMap(opts, false).Name},
			}},
		},
		{
			Name: "tls",
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: buildTLSSecret(opts, KeyPair{}).Name,
			}},
		},
		{
			Name: "data",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: buildPVC(opts).Name,
			}},
		},
	}
	if !reflect.DeepEqual(pod.Volumes, wantVolumes) {
		t.Errorf("registry volumes = %#v, want config, TLS, and PVC volumes %#v", pod.Volumes, wantVolumes)
	}
}

func TestBuildRoute(t *testing.T) {
	tests := []struct {
		name string
		host string
	}{
		{
			name: "When the route host follows the configured name and namespace, it should create a passthrough Route",
			host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := Options{Name: "mirror-registry", Namespace: "test-mirror-registry"}
			got := buildRoute(opts, test.host)
			if got.Name != opts.Name || got.Namespace != opts.Namespace {
				t.Errorf("Route identity = %s/%s, want %s/%s", got.Namespace, got.Name, opts.Namespace, opts.Name)
			}
			assertResourceLabels(t, got.Labels, opts.Name)
			if got.Spec.Host != test.host {
				t.Errorf("Route host = %q, want %q", got.Spec.Host, test.host)
			}
			if got.Spec.To.Kind != "Service" || got.Spec.To.Name != opts.Name || got.Spec.Port == nil || got.Spec.Port.TargetPort != intstr.FromString("https") {
				t.Errorf("Route backend = %#v, want Service %q port https", got.Spec.To, opts.Name)
			}
			if got.Spec.TLS == nil || got.Spec.TLS.Termination != routev1.TLSTerminationPassthrough || got.Spec.TLS.InsecureEdgeTerminationPolicy != routev1.InsecureEdgeTerminationPolicyNone {
				t.Errorf("Route TLS = %#v, want passthrough with insecure policy None", got.Spec.TLS)
			}
		})
	}
}

func assertResourceLabels(t *testing.T, got map[string]string, name string) {
	t.Helper()
	want := map[string]string{
		"app.kubernetes.io/name": name,
		registryPartOfLabel:      registryPartOf,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("label %q = %q, want %q", key, got[key], value)
		}
	}
}

func testRegistryTLSMaterial(t *testing.T) (KeyPair, KeyPair) {
	t.Helper()
	ca, err := generateCA()
	if err != nil {
		t.Fatalf("generate test CA: %v", err)
	}
	serving, err := servingCert(ca, "mirror-registry", []string{
		"mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com",
	})
	if err != nil {
		t.Fatalf("generate test serving certificate: %v", err)
	}
	return ca, serving
}
