package olm

import (
	"embed"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	"github.com/openshift/hypershift/support/api"
)

//go:embed assets/*
var content embed.FS

func AssetDir(name string) ([]string, error) {
	entries, err := content.ReadDir(name)
	if err != nil {
		panic(err)
	}
	var files []string
	for _, entry := range entries {
		files = append(files, entry.Name())
	}
	return files, nil
}

func MustAsset(name string) []byte {
	b, err := content.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return b
}

func MustService(fileName string) *corev1.Service {
	svc := &corev1.Service{}
	deserializeResource(fileName, svc)
	return svc
}

func MustServiceAccount(fileName string) *corev1.ServiceAccount {
	serviceAccount := &corev1.ServiceAccount{}
	deserializeResource(fileName, serviceAccount)
	return serviceAccount
}

func MustSecret(fileName string) *corev1.Secret {
	secret := &corev1.Secret{}
	deserializeResource(fileName, secret)
	return secret
}

func MustConfigMap(fileName string) *corev1.ConfigMap {
	configMap := &corev1.ConfigMap{}
	deserializeResource(fileName, configMap)
	return configMap
}

func MustDeployment(fileName string) *appsv1.Deployment {
	deployment := &appsv1.Deployment{}
	deserializeResource(fileName, deployment)
	return deployment
}

func MustCronJob(fileName string) *batchv1.CronJob {
	cronJob := &batchv1.CronJob{}
	deserializeResource(fileName, cronJob)
	return cronJob
}

func MustRole(fileName string) *rbacv1.Role {
	role := &rbacv1.Role{}
	deserializeResource(fileName, role)
	return role
}

func MustRoleBinding(fileName string) *rbacv1.RoleBinding {
	roleBinding := &rbacv1.RoleBinding{}
	deserializeResource(fileName, roleBinding)
	return roleBinding
}

func MustAPIService(fileName string) *apiregistrationv1.APIService {
	apiService := &apiregistrationv1.APIService{}
	deserializeResource(fileName, apiService)
	return apiService
}

func MustEndpoints(fileName string) *corev1.Endpoints {
	ep := &corev1.Endpoints{}
	deserializeResource(fileName, ep)
	return ep
}

func deserializeResource(fileName string, obj runtime.Object) {
	data := MustAsset(fileName)
	gvks, _, err := api.Scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		panic(fmt.Sprintf("cannot determine gvk of resource in %s: %v", fileName, err))
	}
	if _, _, err = api.YamlSerializer.Decode(data, &gvks[0], obj); err != nil {
		panic(fmt.Sprintf("cannot decode resource in %s: %v", fileName, err))
	}
}
