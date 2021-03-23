package machineconfigserver

import (
	"encoding/base64"
	"fmt"

	"github.com/blang/semver"
	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sutilspointer "k8s.io/utils/pointer"
)

type Deployment struct {
	Name           string
	Namespace      string
	ServiceAccount *corev1.ServiceAccount
	Images         map[string]string
}

func (d Deployment) mcoBootstrapArg() string {
	return fmt.Sprintf(`
mkdir -p /mcc-manifests/bootstrap/manifests
mkdir -p /mcc-manifests/manifests
exec machine-config-operator bootstrap \
--root-ca=/assets/manifests/root-ca.crt \
--kube-ca=/assets/manifests/combined-ca.crt \
--machine-config-operator-image=%s \
--machine-config-oscontent-image=%s \
--infra-image=%s \
--keepalived-image=%s \
--coredns-image=%s \
--mdns-publisher-image=%s \
--haproxy-image=%s \
--baremetal-runtimecfg-image=%s \
--infra-config-file=/assets/manifests/cluster-infrastructure-02-config.yaml \
--network-config-file=/assets/manifests/cluster-network-02-config.yaml \
--proxy-config-file=/assets/manifests/cluster-proxy-01-config.yaml \
--config-file=/assets/manifests/install-config.yaml \
--dns-config-file=/assets/manifests/cluster-dns-02-config.yaml \
--dest-dir=/mcc-manifests \
--pull-secret=/assets/manifests/pull-secret.yaml

# Use our own version of configpools that swap master and workers
mv /mcc-manifests/bootstrap/manifests /mcc-manifests/bootstrap/manifests.tmp
mkdir /mcc-manifests/bootstrap/manifests
cp /mcc-manifests/bootstrap/manifests.tmp/* /mcc-manifests/bootstrap/manifests/
cp /assets/manifests/*.machineconfigpool.yaml /mcc-manifests/bootstrap/manifests/`,
		d.Images["machine-config-operator"],
		d.Images["machine-os-content"],
		d.Images["pod"],
		d.Images["keepalived-ipfailover"],
		d.Images["coredns"],
		d.Images["mdns-publisher"],
		d.Images["haproxy-router"],
		d.Images["baremetal-runtimecfg"],
	)
}

var customMachineConfigArg = `
cat <<"EOF" > "./copy-ignition-config.sh"
#!/bin/bash
name="${1}"
oc get cm ${name} -n "${NAMESPACE}" -o jsonpath='{ .data.data }' > "/mcc-manifests/bootstrap/manifests/${name/#ignition-config-//}.yaml"
EOF
chmod +x ./copy-ignition-config.sh
oc get cm -l ignition-config="true" -n "${NAMESPACE}" --no-headers | awk '{ print $1 }' | xargs -n1 ./copy-ignition-config.sh`

func (o Deployment) Build() *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: appsv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("machine-config-server-%s", o.Name),
			Namespace: o.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: k8sutilspointer.Int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": fmt.Sprintf("machine-config-server-%s", o.Name),
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": fmt.Sprintf("machine-config-server-%s", o.Name),
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:            o.ServiceAccount.Name,
					TerminationGracePeriodSeconds: k8sutilspointer.Int64Ptr(10),
					Tolerations: []corev1.Toleration{
						{
							Key:      "multi-az-worker",
							Operator: "Equal",
							Value:    "true",
							Effect:   "NoSchedule",
						},
					},
					InitContainers: []corev1.Container{
						{
							Image: o.Images["machine-config-operator"],
							Name:  "machine-config-operator-bootstrap",
							Command: []string{
								"/bin/bash",
							},
							Args: []string{
								"-c",
								o.mcoBootstrapArg(),
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "mcc-manifests",
									MountPath: "/mcc-manifests",
								},
								{
									Name:      "config",
									MountPath: "/assets/manifests",
								},
							},
						},
						{
							Image:           o.Images["cli"],
							ImagePullPolicy: corev1.PullIfNotPresent,
							Name:            "inject-custom-machine-configs",
							Env: []corev1.EnvVar{
								{
									Name: "NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
							},
							WorkingDir: "/tmp",
							Command: []string{
								"/usr/bin/bash",
							},
							Args: []string{
								"-c",
								customMachineConfigArg,
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "mcc-manifests",
									MountPath: "/mcc-manifests",
								},
							},
						},
						{
							Image:           o.Images["machine-config-operator"],
							ImagePullPolicy: corev1.PullIfNotPresent,
							Name:            "machine-config-controller-bootstrap",
							Command: []string{
								"/usr/bin/machine-config-controller",
							},
							Args: []string{
								"bootstrap",
								"--manifest-dir=/mcc-manifests/bootstrap/manifests",
								"--pull-secret=/mcc-manifests/bootstrap/manifests/machineconfigcontroller-pull-secret",
								"--dest-dir=/mcs-manifests",
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "mcc-manifests",
									MountPath: "/mcc-manifests",
								},
								{
									Name:      "mcs-manifests",
									MountPath: "/mcs-manifests",
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Image:           o.Images["machine-config-operator"],
							ImagePullPolicy: corev1.PullIfNotPresent,
							Name:            "machine-config-server",
							Command: []string{
								"/usr/bin/machine-config-server",
							},
							Args: []string{
								"bootstrap",
								"--bootstrap-kubeconfig=/etc/openshift/kubeconfig",
								"--secure-port=8443",
								"--insecure-port=8080",
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: 8080,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									Name:          "https",
									ContainerPort: 8443,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "kubeconfig",
									ReadOnly:  true,
									MountPath: "/etc/openshift",
								},
								{
									Name:      "mcs-manifests",
									MountPath: "/etc/mcs/bootstrap",
								},
								{
									Name:      "mcc-manifests",
									MountPath: "/etc/mcc/bootstrap",
								},
								{
									Name:      "mcs-tls",
									MountPath: "/etc/ssl/mcs",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "kubeconfig",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "machine-config-server-kubeconfig",
								},
							},
						},
						{
							Name: "mcs-tls",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "machine-config-server",
								},
							},
						},
						{
							Name: "mcs-manifests",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "mcc-manifests",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "machine-config-server",
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

type ServiceAccount struct {
	Name      string
	Namespace string
}

func (o ServiceAccount) Build() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace,
			Name:      fmt.Sprintf("machine-config-server-%s", o.Name),
		},
	}
}

type RoleBinding struct {
	Name           string
	ServiceAccount *corev1.ServiceAccount
}

func (o RoleBinding) Build() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.ServiceAccount.Namespace,
			Name:      fmt.Sprintf("machine-config-server-%s", o.Name),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "edit",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      o.ServiceAccount.Name,
				Namespace: o.ServiceAccount.Namespace,
			},
		},
	}
}

type Service struct {
	Name      string
	Namespace string
}

func (o Service) Build() *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace,
			Name:      fmt.Sprintf("machine-config-server-%s", o.Name),
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt(8080),
					NodePort:   0,
				},
			},
			Selector: map[string]string{
				"app": fmt.Sprintf("machine-config-server-%s", o.Name),
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}
}

type IgnitionRoute struct {
	Name      string
	Namespace string
}

func (o IgnitionRoute) Build() *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace,
			Name:      fmt.Sprintf("ignition-provider-%s", o.Name),
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: fmt.Sprintf("machine-config-server-%s", o.Name),
			},
		},
	}
}

type Userdata struct {
	Name                 string
	Namespace            string
	Version              semver.Version
	IgnitionProviderAddr string
}

func (o Userdata) Build() *corev1.Secret {
	secret := &corev1.Secret{}
	secret.Name = fmt.Sprintf("user-data-%s", o.Name)
	secret.Namespace = o.Namespace

	disableTemplatingValue := []byte(base64.StdEncoding.EncodeToString([]byte("true")))
	var userDataValue []byte

	// Clear any version modifiers for this comparison
	o.Version.Pre = nil
	o.Version.Build = nil
	if o.Version.GTE(semver.MustParse("4.6.0")) {
		userDataValue = []byte(fmt.Sprintf(`{"ignition":{"config":{"merge":[{"source":"http://%s/config/master","verification":{}}]},"security":{},"timeouts":{},"version":"3.1.0"},"networkd":{},"passwd":{},"storage":{},"systemd":{}}`, o.IgnitionProviderAddr))
	} else {
		userDataValue = []byte(fmt.Sprintf(`{"ignition":{"config":{"append":[{"source":"http://%s/config/master","verification":{}}]},"security":{},"timeouts":{},"version":"2.2.0"},"networkd":{},"passwd":{},"storage":{},"systemd":{}}`, o.IgnitionProviderAddr))
	}

	secret.Data = map[string][]byte{
		"disableTemplating": disableTemplatingValue,
		"value":             userDataValue,
	}
	return secret
}
