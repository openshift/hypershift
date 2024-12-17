package manifests

import (
	imagev1 "github.com/openshift/api/image/v1"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func CatalogsImageStream(ns string) *imagev1.ImageStream {
	return &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "catalogs",
			Namespace: ns,
		},
	}
}

// Certified Operators Catalog

func CertifiedOperatorsDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "certified-operators-catalog",
			Namespace: ns,
		},
	}
}

func CertifiedOperatorsService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "certified-operators",
			Namespace: ns,
		},
	}
}

// Community Operators Catalog

func CommunityOperatorsDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "community-operators-catalog",
			Namespace: ns,
		},
	}
}

func CommunityOperatorsService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "community-operators",
			Namespace: ns,
		},
	}
}

// RedHatMarketplace Operators Catalog

func RedHatMarketplaceOperatorsDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redhat-marketplace-catalog",
			Namespace: ns,
		},
	}
}

func RedHatMarketplaceOperatorsService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redhat-marketplace",
			Namespace: ns,
		},
	}
}

// RedHat Operators Catalog

func RedHatOperatorsDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redhat-operators-catalog",
			Namespace: ns,
		},
	}
}

func RedHatOperatorsService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redhat-operators",
			Namespace: ns,
		},
	}
}

// Catalog Operator

func CatalogOperatorMetricsService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "catalog-operator-metrics",
			Namespace: ns,
		},
	}
}

func CatalogOperatorDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "catalog-operator",
			Namespace: ns,
		},
	}
}

func CatalogOperatorServiceMonitor(ns string) *prometheusoperatorv1.ServiceMonitor {
	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "catalog-operator",
			Namespace: ns,
		},
	}
}

// OLM Operator

func OLMOperatorMetricsService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "olm-operator-metrics",
			Namespace: ns,
		},
	}
}

func OLMOperatorDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "olm-operator",
			Namespace: ns,
		},
	}
}

func OLMOperatorServiceMonitor(ns string) *prometheusoperatorv1.ServiceMonitor {
	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "olm-operator",
			Namespace: ns,
		},
	}
}

// Packageserver

func OLMPackageServerDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "packageserver",
			Namespace: ns,
		},
	}
}

// Collect Profiles
func CollectProfilesConfigMap(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "olm-collect-profiles",
			Namespace: ns,
		},
	}
}

func CollectProfilesCronJob(ns string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "olm-collect-profiles",
			Namespace: ns,
		},
	}
}

func CollectProfilesRole(ns string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "olm-collect-profiles",
			Namespace: ns,
		},
	}
}

func CollectProfilesRoleBinding(ns string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "olm-collect-profiles",
			Namespace: ns,
		},
	}
}

func CollectProfilesSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pprof-cert",
			Namespace: ns,
		},
	}
}

func CollectProfilesServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "olm-collect-profiles",
			Namespace: ns,
		},
	}
}
