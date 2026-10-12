package mirrorregistry

import (
	"strconv"

	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	registryPartOfLabel  = "app.kubernetes.io/part-of"
	registryPartOf       = "hypershift-e2e-mirror-registry"
	configHashAnnotation = "mirrorregistry.hypershift.io/config-hash"
)

func resourceLabels(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name": name,
		registryPartOfLabel:      registryPartOf,
	}
}

func buildNamespace(opts Options) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   opts.Namespace,
			Labels: resourceLabels(opts.Name),
		},
	}
}

func buildTLSSecret(opts Options, serving KeyPair) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name + "-tls",
			Namespace: opts.Namespace,
			Labels:    resourceLabels(opts.Name),
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       serving.CertPEM,
			corev1.TLSPrivateKeyKey: serving.KeyPEM,
		},
	}
}

func buildPVC(opts Options) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: opts.Namespace,
			Labels:    resourceLabels(opts.Name),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: opts.Storage.Size},
			},
		},
	}
	if opts.Storage.StorageClassName != "" {
		pvc.Spec.StorageClassName = ptr.To(opts.Storage.StorageClassName)
	}
	return pvc
}

func buildService(opts Options) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: opts.Namespace,
			Labels:    resourceLabels(opts.Name),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: resourceLabels(opts.Name),
			Ports: []corev1.ServicePort{{
				Name:       "https",
				Port:       RegistryPort,
				TargetPort: intstr.FromInt32(RegistryPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

func buildConfigMap(opts Options, readOnly bool) *corev1.ConfigMap {
	config := `version: 0.1
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
      enabled: ` + strconv.FormatBool(readOnly) + `
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
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name + "-config",
			Namespace: opts.Namespace,
			Labels:    resourceLabels(opts.Name),
		},
		Data: map[string]string{"config.yml": config},
	}
}

func buildDeployment(opts Options, configHash string) *appsv1.Deployment {
	labels := resourceLabels(opts.Name)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: opts.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: map[string]string{configHashAnnotation: configHash},
				},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						Name:  "registry",
						Image: opts.Image,
						Args:  []string{"/etc/registry/config.yml"},
						Ports: []corev1.ContainerPort{{
							Name:          "https",
							ContainerPort: RegistryPort,
							Protocol:      corev1.ProtocolTCP,
						}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler:  corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/v2/", Port: intstr.FromInt32(RegistryPort), Scheme: corev1.URISchemeHTTPS}},
							PeriodSeconds: 5,
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/v2/", Port: intstr.FromInt32(RegistryPort), Scheme: corev1.URISchemeHTTPS}},
							InitialDelaySeconds: 10,
							PeriodSeconds:       10,
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config", MountPath: "/etc/registry", ReadOnly: true},
							{Name: "tls", MountPath: "/certs", ReadOnly: true},
							{Name: "data", MountPath: "/var/lib/registry"},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: opts.Name + "-config"}}}},
						{Name: "tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: opts.Name + "-tls"}}},
						{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: opts.Name}}},
					},
				},
			},
		},
	}
}

func buildRoute(opts Options, host string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: opts.Namespace,
			Labels:    resourceLabels(opts.Name),
		},
		Spec: routev1.RouteSpec{
			Host: host,
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: opts.Name,
			},
			Port: &routev1.RoutePort{TargetPort: intstr.FromString("https")},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
			},
		},
	}
}
