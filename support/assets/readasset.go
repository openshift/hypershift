package assets

import (
	"fmt"

	imagev1 "github.com/openshift/api/image/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	"github.com/openshift/hypershift/support/api"
)

type AssetReader func(name string) ([]byte, error)

func MustAsset(reader AssetReader, name string) []byte {
	b, err := reader(name)
	if err != nil {
		panic(err)
	}
	return b
}

func MustService(reader AssetReader, fileName string) *corev1.Service {
	svc := &corev1.Service{}
	deserializeResource(reader, fileName, svc)
	return svc
}

func MustServiceAccount(reader AssetReader, fileName string) *corev1.ServiceAccount {
	serviceAccount := &corev1.ServiceAccount{}
	deserializeResource(reader, fileName, serviceAccount)
	return serviceAccount
}

func MustSecret(reader AssetReader, fileName string) *corev1.Secret {
	secret := &corev1.Secret{}
	deserializeResource(reader, fileName, secret)
	return secret
}

func MustConfigMap(reader AssetReader, fileName string) *corev1.ConfigMap {
	configMap := &corev1.ConfigMap{}
	deserializeResource(reader, fileName, configMap)
	return configMap
}

func MustDeployment(reader AssetReader, fileName string) *appsv1.Deployment {
	deployment := &appsv1.Deployment{}
	deserializeResource(reader, fileName, deployment)
	return deployment
}

func MustCronJob(reader AssetReader, fileName string) *batchv1.CronJob {
	cronJob := &batchv1.CronJob{}
	deserializeResource(reader, fileName, cronJob)
	return cronJob
}

func MustImageStream(reader AssetReader, fileName string) *imagev1.ImageStream {
	imageStream := &imagev1.ImageStream{}
	deserializeResource(reader, fileName, imageStream)
	return imageStream
}

func MustRole(reader AssetReader, fileName string) *rbacv1.Role {
	role := &rbacv1.Role{}
	deserializeResource(reader, fileName, role)
	return role
}

func MustRoleBinding(reader AssetReader, fileName string) *rbacv1.RoleBinding {
	roleBinding := &rbacv1.RoleBinding{}
	deserializeResource(reader, fileName, roleBinding)
	return roleBinding
}

func MustAPIService(reader AssetReader, fileName string) *apiregistrationv1.APIService {
	apiService := &apiregistrationv1.APIService{}
	deserializeResource(reader, fileName, apiService)
	return apiService
}

func MustEndpoints(reader AssetReader, fileName string) *corev1.Endpoints {
	ep := &corev1.Endpoints{}
	deserializeResource(reader, fileName, ep)
	return ep
}

func deserializeResource(reader AssetReader, fileName string, obj runtime.Object) {
	data := MustAsset(reader, fileName)
	gvks, _, err := api.Scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		panic(fmt.Sprintf("cannot determine gvk of resource in %s: %v", fileName, err))
	}
	if _, _, err = api.YamlSerializer.Decode(data, &gvks[0], obj); err != nil {
		panic(fmt.Sprintf("cannot decode resource in %s: %v", fileName, err))
	}
}
