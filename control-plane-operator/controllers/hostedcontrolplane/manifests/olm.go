package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Certified Operators Catalog

func CertifiedOperatorsCatalogSourceWorkerManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-olm-certified-operators-catalog-source",
			Namespace: ns,
		},
	}
}

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

func CertifiedOperatorsCronJob(ns string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "certified-operators-catalog-rollout",
			Namespace: ns,
		},
	}
}

// Community Operators Catalog

func CommunityOperatorsCatalogSourceWorkerManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-olm-community-operators-catalog-source",
			Namespace: ns,
		},
	}
}

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

func CommunityOperatorsCronJob(ns string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "community-operators-catalog-rollout",
			Namespace: ns,
		},
	}
}

// RedHatMarketplace Operators Catalog

func RedHatMarketplaceOperatorsCatalogSourceWorkerManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-olm-redhat-marketplace-operators-catalog-source",
			Namespace: ns,
		},
	}
}

func RedHatMarketplaceOperatorsDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redhat-marketplace-operators-catalog",
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

func RedHatMarketplaceOperatorsCronJob(ns string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redhat-marketplace-operators-catalog-rollout",
			Namespace: ns,
		},
	}
}

// RedHat Operators Catalog

func RedHatOperatorsCatalogSourceWorkerManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-olm-redhat-operators-catalog-source",
			Namespace: ns,
		},
	}
}

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

func RedHatOperatorsCronJob(ns string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redhat-operators-catalog-rollout",
			Namespace: ns,
		},
	}
}

// Catalog Rollout

func CatalogRolloutRole(ns string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "catalog-rollout",
			Namespace: ns,
		},
	}
}

func CatalogRolloutRoleBinding(ns string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "catalog-rollout",
			Namespace: ns,
		},
	}
}

func CatalogRolloutServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "catalog-rollout",
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

// Packageserver

func OLMPackageServerDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "packageserver",
			Namespace: ns,
		},
	}
}

func OLMPackageServerWorkerAPIServiceManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-olm-packageserver-apiservice",
			Namespace: ns,
		},
	}
}

func OLMPackageServerWorkerServiceManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-olm-packageserver-service",
			Namespace: ns,
		},
	}
}

func OLMPackageServerWorkerEndpointsManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-olm-packageserver-endpoints",
			Namespace: ns,
		},
	}
}

func OLMAlertRulesWorkerManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-olm-alert-rules",
			Namespace: ns,
		},
	}
}
