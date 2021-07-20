package assets

import (
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type HyperShiftWebhookDeployment struct {
	Namespace      *corev1.Namespace
	OperatorImage  string
	ServiceAccount *corev1.ServiceAccount
	Replicas       int32
}

func (o HyperShiftWebhookDeployment) Build() *appsv1.Deployment {
	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: appsv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "webhook",
			Namespace: o.Namespace.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &o.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "webhook",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": "webhook",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: o.ServiceAccount.Name,
					Containers: []corev1.Container{
						{
							Name:            "webhook",
							Image:           o.OperatorImage,
							ImagePullPolicy: corev1.PullAlways,
							Command:         []string{"/usr/bin/hypershift-webhook"},
							Args:            []string{"start"},
							Ports: []corev1.ContainerPort{
								{
									Name:          "webhook",
									ContainerPort: 6443,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									Name:          "metrics",
									ContainerPort: 8080,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "serving-cert",
									MountPath: "/var/run/secrets/serving-cert",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "serving-cert",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "webhook-serving-cert",
								},
							},
						},
					},
				},
			},
		},
	}
	return deployment
}

type HyperShiftWebhookService struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftWebhookService) Build() *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "webhook",
			Annotations: map[string]string{
				"prometheus.io/port":                                 "443",
				"prometheus.io/scheme":                               "https",
				"prometheus.io/scrape":                               "true",
				"service.beta.openshift.io/serving-cert-secret-name": "webhook-serving-cert",
			},
			Labels: map[string]string{
				"name": "webhook",
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"name": "webhook",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "webhook",
					Protocol:   corev1.ProtocolTCP,
					Port:       443,
					TargetPort: intstr.FromString("webhook"),
				},
				{
					Name:       "metrics",
					Protocol:   corev1.ProtocolTCP,
					Port:       8080,
					TargetPort: intstr.FromString("metrics"),
				},
			},
		},
	}
}

type HyperShiftWebhookServiceAccount struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftWebhookServiceAccount) Build() *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "webhook",
		},
	}
	return sa
}

type HyperShiftWebhookRole struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftWebhookRole) Build() *rbacv1.Role {
	role := &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "hypershift-webhook",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"hypershift.openshift.io"},
				Resources: []string{
					"hostedcluster",
				},
				Verbs: []string{"get", "list", "watch"},
			},
		},
	}
	return role
}

type HyperShiftWebhookRoleBinding struct {
	Role           *rbacv1.Role
	ServiceAccount *corev1.ServiceAccount
}

func (o HyperShiftWebhookRoleBinding) Build() *rbacv1.RoleBinding {
	binding := &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.ServiceAccount.Namespace,
			Name:      "hypershift-webhook",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     o.Role.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      o.ServiceAccount.Name,
				Namespace: o.ServiceAccount.Namespace,
			},
		},
	}
	return binding
}

type HyperShiftValidatingWebhookConfiguration struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftValidatingWebhookConfiguration) Build() *admissionregistrationv1.ValidatingWebhookConfiguration {
	scope := admissionregistrationv1.NamespacedScope
	path := "/validate-hypershift-openshift-io-v1alpha1-hostedcluster"
	sideEffects := admissionregistrationv1.SideEffectClassNone
	timeout := int32(10)
	validatingWebhookConfiguration := &admissionregistrationv1.ValidatingWebhookConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ValidatingWebhookConfiguration",
			APIVersion: admissionregistrationv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace.Name,
			Name:      "hypershift.openshift.io",
			Annotations: map[string]string{
				"service.beta.openshift.io/inject-cabundle": "true",
			},
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "hostedclusters.hypershift.openshift.io",
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Create,
							admissionregistrationv1.Update,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"hypershift.openshift.io"},
							APIVersions: []string{"v1alpha1"},
							Resources:   []string{"hostedclusters"},
							Scope:       &scope,
						},
					},
				},
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Namespace: "hypershift",
						Name:      "webhook",
						Path:      &path,
					},
				},
				SideEffects:             &sideEffects,
				AdmissionReviewVersions: []string{"v1"},
				TimeoutSeconds:          &timeout,
			},
		},
	}
	return validatingWebhookConfiguration
}

type HyperShiftWebhookServiceMonitor struct {
	Namespace *corev1.Namespace
}

func (o HyperShiftWebhookServiceMonitor) Build() *unstructured.Unstructured {
	serviceMonitorJSON := `
{
   "apiVersion": "monitoring.coreos.com/v1",
   "kind": "ServiceMonitor",
   "metadata": {
      "name": "webhook"
   },
   "spec": {
      "endpoints": [
         {
            "interval": "30s",
            "port": "metrics"
         }
      ],
      "jobLabel": "component",
      "selector": {
         "matchLabels": {
            "name": "webhook"
         }
      }
   }
}
`
	obj, err := runtime.Decode(unstructured.UnstructuredJSONScheme, []byte(serviceMonitorJSON))
	if err != nil {
		panic(err)
	}
	sm := obj.(*unstructured.Unstructured)
	sm.SetNamespace(o.Namespace.Name)
	return sm
}
